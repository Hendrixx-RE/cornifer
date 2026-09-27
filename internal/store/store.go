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

	// Files

	// UpsertFiles inserts or updates files by (repo_id, path), for initial
	// index and incremental reindex alike.
	UpsertFiles(ctx context.Context, files []*model.File) error
	// ListFiles returns every File belonging to repoID.
	ListFiles(ctx context.Context, repoID int64) ([]*model.File, error)
	// DeleteFile removes a File and, via ON DELETE CASCADE, its symbols,
	// edges, unresolved_refs, and chunks — the incremental-reindex path for
	// a file that no longer exists.
	DeleteFile(ctx context.Context, fileID int64) error

	// Symbols

	// InsertSymbols bulk-inserts symbols, expected to use pgx CopyFrom per
	// plan.md ("bulk insert symbols with pgx CopyFrom").
	InsertSymbols(ctx context.Context, symbols []*model.Symbol) error
	// DeleteSymbolsForFile removes every Symbol belonging to fileID, ahead
	// of re-inserting freshly parsed ones during incremental reindex.
	DeleteSymbolsForFile(ctx context.Context, fileID int64) error
	// FindSymbolByQualifiedName is the exact-match half of find_definition.
	FindSymbolByQualifiedName(ctx context.Context, repoID int64, qualifiedName string) (*model.Symbol, error)
	// FindSymbolsByName is the fuzzier half of find_definition: every
	// Symbol named exactly name, for disambiguation when multiple
	// candidates share a bare name.
	FindSymbolsByName(ctx context.Context, repoID int64, name string) ([]*model.Symbol, error)

	// Edges

	// InsertEdges bulk-inserts edges.
	InsertEdges(ctx context.Context, edges []*model.Edge) error
	// GetCallers returns edges whose DstSymbolID is symbolID — i.e. every
	// resolved reference to it, of any EdgeKind.
	GetCallers(ctx context.Context, symbolID int64) ([]*model.Edge, error)
	// GetCallees returns edges whose SrcSymbolID is symbolID — i.e. every
	// resolved reference it makes, of any EdgeKind.
	GetCallees(ctx context.Context, symbolID int64) ([]*model.Edge, error)

	// Unresolved refs

	// InsertUnresolvedRefs bulk-inserts unresolved_refs rows.
	InsertUnresolvedRefs(ctx context.Context, refs []*model.UnresolvedRef) error

	// Chunks

	// InsertChunks bulk-inserts chunks, including their embeddings when
	// already computed (Embedding may be nil to insert text first and embed
	// later).
	InsertChunks(ctx context.Context, chunks []*model.Chunk) error
	// VectorSearch returns the limit chunks with embeddings nearest to
	// query, ordered closest first, using the HNSW index on
	// chunks.embedding. len(query) must equal the configured embedding
	// dimension.
	VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error)

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

func (unimplemented) UpsertFiles(ctx context.Context, files []*model.File) error {
	return model.ErrNotImplemented
}

func (unimplemented) ListFiles(ctx context.Context, repoID int64) ([]*model.File, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) DeleteFile(ctx context.Context, fileID int64) error {
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

func (unimplemented) InsertEdges(ctx context.Context, edges []*model.Edge) error {
	return model.ErrNotImplemented
}

func (unimplemented) GetCallers(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) GetCallees(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) InsertUnresolvedRefs(ctx context.Context, refs []*model.UnresolvedRef) error {
	return model.ErrNotImplemented
}

func (unimplemented) InsertChunks(ctx context.Context, chunks []*model.Chunk) error {
	return model.ErrNotImplemented
}

func (unimplemented) VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}

func (unimplemented) Close() error {
	return nil
}
