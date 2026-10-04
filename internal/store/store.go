package store

import (
	"context"

	"github.com/pgvector/pgvector-go"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// EncodeVector adapts a model.Chunk's embedding to the pgvector wire type
// pgx needs to bind a `vector(N)` column parameter. Kept here, not in
// model, so model stays free of storage-layer dependencies.
func EncodeVector(embedding []float32) pgvector.Vector {
	return pgvector.NewVector(embedding)
}

// DecodeVector is the inverse of EncodeVector, for reading a `vector(N)`
// column value back into a model.Chunk's embedding.
func DecodeVector(v pgvector.Vector) []float32 {
	return v.Slice()
}

// Store is all Postgres access needed by the indexing pipeline and the
// query layer. Methods are grouped by the table they primarily touch; see
// migrations/ for the schema. Implementations must be safe for concurrent
// use.
type Store interface {
	// Repos

	// CreateRepo inserts repo and returns its assigned ID.
	CreateRepo(ctx context.Context, repo *model.Repo) (int64, error)
	// GetRepoByCommit looks up a previously indexed Repo by root + commit
	// SHA. Returns model.ErrNotImplemented's caller-visible "not found"
	// convention once implemented (documented on the concrete type).
	GetRepoByCommit(ctx context.Context, root, commitSHA string) (*model.Repo, error)
	// GetRepoByID loads snapshot metadata for an MCP server configured with a
	// numeric repository ID.
	GetRepoByID(ctx context.Context, repoID int64) (*model.Repo, error)
	// UpdateRepoEmbeddingProvenance records the vector-space metadata used by
	// a complete (re)index of repoID.
	UpdateRepoEmbeddingProvenance(ctx context.Context, repoID int64, provider, embeddingModel string) error

	// Files

	// UpsertFiles inserts or updates files by (repo_id, path), for initial
	// index and incremental reindex alike.
	UpsertFiles(ctx context.Context, files []*model.File) error
	// ListFiles returns every File belonging to repoID.
	ListFiles(ctx context.Context, repoID int64) ([]*model.File, error)
	// GetFiles looks up files by ID. Returns a map keyed by File.ID rather
	// than a slice: order is not preserved, and any ID with no matching row
	// is simply absent from the result (not an error) — the convention the
	// MCP catalog's ID-hydration needs (internal/mcp.Catalog).
	GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error)
	// DeleteFile removes a File and, via ON DELETE CASCADE, its symbols,
	// edges, unresolved_refs, and chunks — the incremental-reindex path for
	// a file that no longer exists.
	DeleteFile(ctx context.Context, fileID int64) error
	// DeleteFileContents transactionally deletes fileID's chunks and symbols
	// (which cascades to their edges and unresolved_refs), leaving the File
	// row itself untouched — the incremental-reindex path for a file whose
	// content_hash changed and is about to be re-parsed and re-upserted in
	// place. Chunks are deleted first and by file_id directly, since
	// module-level chunks (Chunk.SymbolID nil) have no symbol row to cascade
	// from.
	DeleteFileContents(ctx context.Context, fileID int64) error

	// Symbols

	// InsertSymbols bulk-inserts symbols, expected to use pgx CopyFrom per
	// plan.md ("bulk insert symbols with pgx CopyFrom").
	InsertSymbols(ctx context.Context, symbols []*model.Symbol) error
	// DeleteSymbolsForFile removes every Symbol belonging to fileID, ahead
	// of re-inserting freshly parsed ones during incremental reindex.
	DeleteSymbolsForFile(ctx context.Context, fileID int64) error
	// FindSymbolByQualifiedName is the exact-match half of find_definition.
	//
	// AMBIGUITY WARNING: qualified names are not unique (overload stubs,
	// conditional redefinitions, and same-named nested defs share one), yet
	// this returns a single Symbol. When several match, it silently returns
	// only one: deterministically the lowest (file_id, start_line, id), i.e.
	// the first definition in the earliest-inserted file. That is typically
	// an @overload stub, not the implementation. Callers that need to know
	// whether the name is ambiguous, or need every candidate, must not rely on
	// this method; use FindSymbolsByName and filter on QualifiedName.
	FindSymbolByQualifiedName(ctx context.Context, repoID int64, qualifiedName string) (*model.Symbol, error)
	// FindSymbolsByName is the fuzzier half of find_definition: every
	// Symbol named exactly name, for disambiguation when multiple
	// candidates share a bare name.
	FindSymbolsByName(ctx context.Context, repoID int64, name string) ([]*model.Symbol, error)
	// GetSymbols looks up symbols by ID. Same map-keyed, missing-is-absent
	// convention as GetFiles.
	GetSymbols(ctx context.Context, ids []int64) (map[int64]*model.Symbol, error)
	// ListSymbols returns every symbol belonging to repoID. It powers current
	// structural snapshots without relying on a stale local manifest.
	ListSymbols(ctx context.Context, repoID int64) ([]*model.Symbol, error)

	// Edges

	// InsertEdges bulk-inserts edges.
	InsertEdges(ctx context.Context, edges []*model.Edge) error
	// GetCallers returns edges whose DstSymbolID is symbolID — i.e. every
	// resolved reference to it, of any EdgeKind.
	GetCallers(ctx context.Context, symbolID int64) ([]*model.Edge, error)
	// GetCallees returns edges whose SrcSymbolID is symbolID — i.e. every
	// resolved reference it makes, of any EdgeKind.
	GetCallees(ctx context.Context, symbolID int64) ([]*model.Edge, error)
	// LoadEdges returns every edge whose source symbol belongs to repoID, in
	// one query — the bulk load internal/graph.Build needs to construct a
	// whole-repo Graph. It does not itself implement graph.EdgeLoader
	// (that interface's LoadEdges takes no repoID); callers bind repoID
	// with a closure or graph.EdgeLoaderFunc, e.g.
	// graph.EdgeLoaderFunc(func(ctx) { return st.LoadEdges(ctx, repoID) }).
	LoadEdges(ctx context.Context, repoID int64) ([]*model.Edge, error)
	// DeleteEdgesForRepo removes all edges sourced from repoID. It supports a
	// full re-resolution after a file-level incremental update.
	DeleteEdgesForRepo(ctx context.Context, repoID int64) error

	// Unresolved refs

	// InsertUnresolvedRefs bulk-inserts unresolved_refs rows.
	InsertUnresolvedRefs(ctx context.Context, refs []*model.UnresolvedRef) error
	// ListUnresolvedRefs returns every unresolved_refs row whose source
	// symbol belongs to repoID, so recall gaps (see model.UnresolvedRef) can
	// be inspected or reported per repo.
	ListUnresolvedRefs(ctx context.Context, repoID int64) ([]*model.UnresolvedRef, error)
	// DeleteUnresolvedRefsForRepo removes all resolution leftovers for repoID
	// before rebuilding them from a complete resolver pass.
	DeleteUnresolvedRefsForRepo(ctx context.Context, repoID int64) error

	// Chunks

	// InsertChunks bulk-inserts chunks, including their embeddings when
	// already computed (Embedding may be nil to insert text first and embed
	// later).
	InsertChunks(ctx context.Context, chunks []*model.Chunk) error
	// GetChunks looks up chunks by ID. Same map-keyed, missing-is-absent
	// convention as GetFiles.
	GetChunks(ctx context.Context, ids []int64) (map[int64]*model.Chunk, error)
	// ListChunks returns every chunk belonging to repoID without embeddings,
	// for display and evaluation metadata hydration.
	ListChunks(ctx context.Context, repoID int64) ([]*model.Chunk, error)
	// VectorSearch returns the limit chunks with embeddings nearest to
	// query, ordered closest first, using the HNSW index on
	// chunks.embedding. len(query) must equal the configured embedding
	// dimension.
	VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error)
	// VectorSearchByRepo is VectorSearch scoped to a single indexed snapshot.
	// Callers serving a repository must use this method to prevent cross-repo
	// retrieval when the database contains several snapshots.
	VectorSearchByRepo(ctx context.Context, repoID int64, query []float32, limit int) ([]*model.Chunk, error)

	// Close releases the underlying connection pool.
	Close() error
}

// unimplemented is the Phase 0 stub Store. Later waves replace it with a
// pgx-backed implementation.
type unimplemented struct{}

// New returns the Phase 0 stub Store, whose methods all return
// model.ErrNotImplemented.
func New() Store {
	return unimplemented{}
}

func (unimplemented) CreateRepo(ctx context.Context, repo *model.Repo) (int64, error) {
	return 0, model.ErrNotImplemented
}

func (unimplemented) GetRepoByCommit(ctx context.Context, root, commitSHA string) (*model.Repo, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) GetRepoByID(ctx context.Context, repoID int64) (*model.Repo, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) UpdateRepoEmbeddingProvenance(ctx context.Context, repoID int64, provider, embeddingModel string) error {
	return model.ErrNotImplemented
}

func (unimplemented) UpsertFiles(ctx context.Context, files []*model.File) error {
	return model.ErrNotImplemented
}

func (unimplemented) ListFiles(ctx context.Context, repoID int64) ([]*model.File, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) DeleteFile(ctx context.Context, fileID int64) error {
	return model.ErrNotImplemented
}

func (unimplemented) DeleteFileContents(ctx context.Context, fileID int64) error {
	return model.ErrNotImplemented
}

func (unimplemented) InsertSymbols(ctx context.Context, symbols []*model.Symbol) error {
	return model.ErrNotImplemented
}

func (unimplemented) DeleteSymbolsForFile(ctx context.Context, fileID int64) error {
	return model.ErrNotImplemented
}

func (unimplemented) FindSymbolByQualifiedName(ctx context.Context, repoID int64, qualifiedName string) (*model.Symbol, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) FindSymbolsByName(ctx context.Context, repoID int64, name string) ([]*model.Symbol, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) GetSymbols(ctx context.Context, ids []int64) (map[int64]*model.Symbol, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) ListSymbols(ctx context.Context, repoID int64) ([]*model.Symbol, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) InsertEdges(ctx context.Context, edges []*model.Edge) error {
	return model.ErrNotImplemented
}

func (unimplemented) GetCallers(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) GetCallees(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) LoadEdges(ctx context.Context, repoID int64) ([]*model.Edge, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) DeleteEdgesForRepo(ctx context.Context, repoID int64) error {
	return model.ErrNotImplemented
}

func (unimplemented) InsertUnresolvedRefs(ctx context.Context, refs []*model.UnresolvedRef) error {
	return model.ErrNotImplemented
}

func (unimplemented) ListUnresolvedRefs(ctx context.Context, repoID int64) ([]*model.UnresolvedRef, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) DeleteUnresolvedRefsForRepo(ctx context.Context, repoID int64) error {
	return model.ErrNotImplemented
}

func (unimplemented) InsertChunks(ctx context.Context, chunks []*model.Chunk) error {
	return model.ErrNotImplemented
}

func (unimplemented) GetChunks(ctx context.Context, ids []int64) (map[int64]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) ListChunks(ctx context.Context, repoID int64) ([]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) VectorSearchByRepo(ctx context.Context, repoID int64, query []float32, limit int) ([]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) Close() error {
	return nil
}
