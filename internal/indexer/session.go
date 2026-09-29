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
// callers, callees, blast-radius, cycles) needs for one already-indexed
// repo: the live Store (for vector search and any future point lookups),
// the cached Manifest (see cache.go), and a Graph built from it.
type Session struct {
	Store    store.Store
	Manifest *Manifest
	Graph    *graph.Graph

	symbolByID map[int64]*model.Symbol
	fileByID   map[int64]*model.File
	chunkByID  map[int64]ChunkMeta
}

// OpenSession resolves repoRoot's most recently indexed Repo (matching the
// commit currently checked out there) via st.GetRepoByCommit, then loads its
// Manifest from cacheDir and builds a Graph from it.
func OpenSession(ctx context.Context, st store.Store, repoRoot, cacheDir string) (*Session, error) {
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

	dir, err := resolveCacheDir(cacheDir)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(dir, repo.ID)
	if err != nil {
		return nil, err
	}

	g := graph.Build(manifest.Edges)

	return &Session{
		Store:      st,
		Manifest:   manifest,
		Graph:      g,
		symbolByID: manifest.BySymbolID(),
		fileByID:   manifest.ByFileID(),
		chunkByID:  manifest.ByChunkID(),
	}, nil
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
	dir, err := resolveCacheDir(cacheDir)
	if err != nil {
		return nil, err
	}
	sparse, err := bm25.Load(bm25Path(dir, s.Manifest.RepoID))
	if err != nil {
		return nil, fmt.Errorf("indexer: load bm25 index (run `cornifer index` first?): %w", err)
	}
	return retrieve.NewHybridSearcher(sparse, s.Store, embedder, cfg), nil
}
