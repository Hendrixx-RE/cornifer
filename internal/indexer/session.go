package indexer

import (
	"context"
	"fmt"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

// Session bundles everything a read-only CLI command (query, find-definition,
// callers, callees, blast-radius, cycles) needs for one already-indexed repo.
// Its in-memory Manifest-shaped snapshot is hydrated from Store at open time,
// never from a stale JSON cache; the persisted cache is now BM25-only.
type Session struct {
	Store    store.Store
	Repo     *model.Repo
	Manifest *Manifest
	Graph    *graph.Graph

	symbolByID map[int64]*model.Symbol
	fileByID   map[int64]*model.File
	chunkByID  map[int64]ChunkMeta
}

// OpenSession resolves repoRoot's indexed snapshot matching its checked-out
// commit, bulk-loads current structural/catalog rows from Store, and builds a
// Graph. cacheDir is retained in the signature for callers that also load the
// BM25 cache, but it is not used to source structural data.
func OpenSession(ctx context.Context, st store.Store, repoRoot, _ string) (*Session, error) {
	absRoot, err := absPath(repoRoot)
	if err != nil {
		return nil, err
	}
	commitSHA, err := resolveCommitSHA(absRoot)
	if err != nil {
		return nil, fmt.Errorf("indexer: resolve commit sha: %w", err)
	}

	repo, err := st.GetRepoByCommit(ctx, absRoot, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("indexer: no indexed repo found for %s @ %s (run `cornifer index` first): %w", absRoot, commitSHA, err)
	}

	files, err := st.ListFiles(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: load files: %w", err)
	}
	symbols, err := st.ListSymbols(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: load symbols: %w", err)
	}
	edges, err := st.LoadEdges(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: load edges: %w", err)
	}
	chunks, err := st.ListChunks(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: load chunks: %w", err)
	}
	manifest := &Manifest{RepoID: repo.ID, Root: repo.Root, CommitSHA: repo.CommitSHA, Files: files, Symbols: symbols, Edges: edges}
	manifest.Chunks = make([]ChunkMeta, len(chunks))
	for i, c := range chunks {
		manifest.Chunks[i] = ChunkMeta{ID: c.ID, SymbolID: c.SymbolID, FileID: c.FileID, StartLine: c.StartLine, EndLine: c.EndLine, Text: c.Text, ContextHeader: c.ContextHeader, TokenCount: c.TokenCount}
	}

	g := graph.Build(edges)

	return &Session{
		Store:      st,
		Repo:       repo,
		Manifest:   manifest,
		Graph:      g,
		symbolByID: manifest.BySymbolID(),
		fileByID:   manifest.ByFileID(),
		chunkByID:  manifest.ByChunkID(),
	}, nil
}

// VectorSearcher scopes dense retrieval to this session's repository. The
// Store's legacy VectorSearch remains useful for administrative callers, but
// user-facing CLI/MCP paths must not return chunks from another snapshot.
func (s *Session) VectorSearcher() retrieve.VectorSearcher {
	return repoVectorSearcher{store: s.Store, repoID: s.Manifest.RepoID}
}

// GraphBoost returns a repo-scoped graph-adjacency boost stage. A
// non-positive weight disables it, which is useful for evaluation ablations.
func (s *Session) GraphBoost(weight float64) retrieve.BoostStage {
	if weight <= 0 {
		return nil
	}
	chunkSymbols := make(map[int64]*int64, len(s.Manifest.Chunks))
	chunkFiles := make(map[int64]int64, len(s.Manifest.Chunks))
	for _, chunk := range s.Manifest.Chunks {
		chunkSymbols[chunk.ID] = chunk.SymbolID
		chunkFiles[chunk.ID] = chunk.FileID
	}
	return retrieve.GraphBoost{Graph: s.Graph, ChunkSymbols: chunkSymbols, ChunkFiles: chunkFiles, Weight: weight}
}

type repoVectorSearcher struct {
	store  store.Store
	repoID int64
}

func (v repoVectorSearcher) VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error) {
	return v.store.VectorSearchByRepo(ctx, v.repoID, query, limit)
}

// Symbol looks up a symbol by its repo-unique ID.
func (s *Session) Symbol(id int64) (*model.Symbol, bool) {
	sym, ok := s.symbolByID[id]
	return sym, ok
}

// File looks up a file by its repo-unique ID.
func (s *Session) File(id int64) (*model.File, bool) {
	f, ok := s.fileByID[id]
	return f, ok
}

// Chunk looks up a chunk's cached metadata by its repo-unique ID.
func (s *Session) Chunk(id int64) (ChunkMeta, bool) {
	c, ok := s.chunkByID[id]
	return c, ok
}

// Location renders "path:line" for a symbol, or "symbol#<id>" if the
// symbol's file is unknown.
func (s *Session) Location(sym *model.Symbol) string {
	if f, ok := s.File(sym.FileID); ok {
		return fmt.Sprintf("%s:%d", f.Path, sym.StartLine)
	}
	return fmt.Sprintf("file#%d:%d", sym.FileID, sym.StartLine)
}

// NewHybridSearcher builds a HybridSearcher over the session's cached BM25
// index (loaded fresh from disk) and the live Store's vector search.
func (s *Session) NewHybridSearcher(cacheDir string, embedder embed.Embedder, cfg retrieve.Config) (*retrieve.HybridSearcher, error) {
	sparse, err := s.LoadBM25(cacheDir)
	if err != nil {
		return nil, err
	}
	return retrieve.NewHybridSearcher(sparse, s.VectorSearcher(), embedder, cfg), nil
}

// LoadBM25 opens the persisted lexical index associated with this session.
// Evaluation needs the sparse-only ranking in addition to hybrid search, so
// exposing this narrow loader avoids duplicating cache-path knowledge in the
// CLI package.
func (s *Session) LoadBM25(cacheDir string) (bm25.SparseIndex, error) {
	dir, err := resolveCacheDir(cacheDir)
	if err != nil {
		return nil, err
	}
	sparse, err := bm25.Load(bm25Path(dir, s.Manifest.RepoID))
	if err != nil {
		return nil, fmt.Errorf("indexer: load bm25 index (run `cornifer index` first?): %w", err)
	}
	return sparse, nil
}
