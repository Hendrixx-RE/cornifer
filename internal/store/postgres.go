package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// ErrNotFound is returned by lookup methods (GetRepoByCommit,
// FindSymbolByQualifiedName) when no row matches. This is store's own
// "not found" convention — model.ErrNotImplemented only covers the Phase 0
// stub, so a real not-found case is a distinct, store-specific sentinel.
// Callers should check with errors.Is(err, store.ErrNotFound).
var ErrNotFound = errors.New("cornifer/store: not found")

// pgStore is the pgx/v5-backed Store implementation.
type pgStore struct {
	pool         *pgxpool.Pool
	embeddingDim int
	distanceOp   string
}

// NewPostgres connects to Postgres per cfg and returns a Store backed by a
// pgxpool connection pool. It pings the database before returning, so a
// misconfigured or unreachable database fails fast here rather than on the
// first query.
//
// This is the real constructor; New() in store.go remains the Phase 0
// unimplemented stub and is unaffected by this addition.
func NewPostgres(ctx context.Context, cfg Config) (Store, error) {
	if cfg.DSN == "" {
		cfg.DSN = DefaultDatabaseURL
	}
	if cfg.DistanceOperator == "" {
		cfg.DistanceOperator = DistanceCosine
	}
	if !validDistanceOperator(cfg.DistanceOperator) {
		return nil, fmt.Errorf("store: unsupported distance operator %q", cfg.DistanceOperator)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("store: parse connection string: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	} else {
		poolCfg.MaxConns = DefaultMaxConns
	}
	poolCfg.AfterConnect = registerVectorType

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("store: create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: could not reach postgres (is `make up` running? DSN host/port: %s): %w", poolCfg.ConnConfig.Host+":"+strconv.Itoa(int(poolCfg.ConnConfig.Port)), err)
	}

	dim, err := embeddingDimension()
	if err != nil {
		pool.Close()
		return nil, err
	}

	return &pgStore{pool: pool, embeddingDim: dim, distanceOp: cfg.DistanceOperator}, nil
}

// embeddingDimension resolves the configured pgvector dimension the same
// way migrations/00007_add_chunks_embedding.go does, so InsertChunks and
// VectorSearch validate against whatever dimension the `chunks.embedding`
// column was actually created with.
func embeddingDimension() (int, error) {
	v := os.Getenv(model.EmbeddingDimEnvVar)
	if v == "" {
		return model.DefaultEmbeddingDim, nil
	}
	dim, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("store: %s=%q is not an integer: %w", model.EmbeddingDimEnvVar, v, err)
	}
	if dim <= 0 {
		return 0, fmt.Errorf("store: %s=%d must be positive", model.EmbeddingDimEnvVar, dim)
	}
	return dim, nil
}

// reserveIDs solves the CopyFrom ID-backfill problem: pgx's CopyFrom sends
// rows over the COPY wire protocol and never returns generated IDs the way
// a normal INSERT ... RETURNING would. Later waves (resolve, chunk) need
// symbol/chunk IDs back immediately to wire ParentID and edges, so instead
// of letting Postgres assign IDs implicitly, we reserve a batch of IDs from
// the table's own BIGSERIAL sequence up front (via nextval, looked up
// through pg_get_serial_sequence so this isn't hardcoded to a naming
// convention), assign them to the in-memory structs, and then COPY the rows
// in with an explicit "id" column. This keeps a single COPY round trip
// (no per-row RETURNING) while still handing callers real IDs synchronously.
//
// nextval() is safe to call concurrently — each call atomically advances the
// sequence — so this is safe under concurrent InsertSymbols/InsertEdges/etc.
// calls; concurrent reservations simply get disjoint, possibly interleaved,
// ranges rather than strictly contiguous ones, which callers don't rely on.
func (s *pgStore) reserveIDs(ctx context.Context, table string, n int) ([]int64, error) {
	if n == 0 {
		return nil, nil
	}

	var seq string
	if err := s.pool.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, 'id')`, table).Scan(&seq); err != nil {
		return nil, fmt.Errorf("resolve id sequence for %s: %w", table, err)
	}

	rows, err := s.pool.Query(ctx, `SELECT nextval($1::regclass) FROM generate_series(1, $2)`, seq, n)
	if err != nil {
		return nil, fmt.Errorf("reserve %d ids from %s: %w", n, seq, err)
	}
	defer rows.Close()

	ids := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reserve ids from %s: %w", seq, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reserve ids from %s: %w", seq, err)
	}
	return ids, nil
}

// Repos

func (s *pgStore) CreateRepo(ctx context.Context, repo *model.Repo) (int64, error) {
	if repo.EmbeddingProvider == "" {
		repo.EmbeddingProvider = "unknown"
	}
	if repo.EmbeddingModel == "" {
		repo.EmbeddingModel = "unknown"
	}
	var id int64
	var indexedAt time.Time
	var err error
	if repo.IndexedAt.IsZero() {
		err = s.pool.QueryRow(ctx,
			`INSERT INTO repos (root, commit_sha, embedding_provider, embedding_model)
			 VALUES ($1, $2, $3, $4) RETURNING id, indexed_at`,
			repo.Root, repo.CommitSHA, repo.EmbeddingProvider, repo.EmbeddingModel,
		).Scan(&id, &indexedAt)
	} else {
		err = s.pool.QueryRow(ctx,
			`INSERT INTO repos (root, commit_sha, embedding_provider, embedding_model, indexed_at)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id, indexed_at`,
			repo.Root, repo.CommitSHA, repo.EmbeddingProvider, repo.EmbeddingModel, repo.IndexedAt,
		).Scan(&id, &indexedAt)
	}
	if err != nil {
		return 0, fmt.Errorf("store: create repo: %w", err)
	}
	repo.ID = id
	repo.IndexedAt = indexedAt
	return id, nil
}

func (s *pgStore) GetRepoByCommit(ctx context.Context, root, commitSHA string) (*model.Repo, error) {
	var r model.Repo
	err := s.pool.QueryRow(ctx,
		`SELECT id, root, commit_sha, embedding_provider, embedding_model, indexed_at
		 FROM repos WHERE root = $1 AND commit_sha = $2`,
		root, commitSHA,
	).Scan(&r.ID, &r.Root, &r.CommitSHA, &r.EmbeddingProvider, &r.EmbeddingModel, &r.IndexedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get repo by commit: %w", err)
	}
	return &r, nil
}

func (s *pgStore) GetRepoByID(ctx context.Context, repoID int64) (*model.Repo, error) {
	var r model.Repo
	err := s.pool.QueryRow(ctx,
		`SELECT id, root, commit_sha, embedding_provider, embedding_model, indexed_at FROM repos WHERE id = $1`, repoID,
	).Scan(&r.ID, &r.Root, &r.CommitSHA, &r.EmbeddingProvider, &r.EmbeddingModel, &r.IndexedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get repo by id: %w", err)
	}
	return &r, nil
}

func (s *pgStore) UpdateRepoEmbeddingProvenance(ctx context.Context, repoID int64, provider, model string) error {
	if provider == "" {
		provider = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE repos SET embedding_provider = $2, embedding_model = $3 WHERE id = $1`,
		repoID, provider, model,
	); err != nil {
		return fmt.Errorf("store: update repo embedding provenance: %w", err)
	}
	return nil
}

// Files

// UpsertFiles runs inside a single transaction: either every file in the
// batch is applied or none are, so a partial parse failure upstream can't
// leave a repo's file table half-updated.
func (s *pgStore) UpsertFiles(ctx context.Context, files []*model.File) error {
	if len(files) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert files: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	batch := &pgx.Batch{}
	for _, f := range files {
		batch.Queue(
			`INSERT INTO files (repo_id, path, language, content_hash, module_name)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (repo_id, path) DO UPDATE SET
			     language = EXCLUDED.language,
			     content_hash = EXCLUDED.content_hash,
			     module_name = EXCLUDED.module_name
			 RETURNING id`,
			f.RepoID, f.Path, f.Language, f.ContentHash, f.ModuleName,
		)
	}

	br := tx.SendBatch(ctx, batch)
	for _, f := range files {
		if err := br.QueryRow().Scan(&f.ID); err != nil {
			br.Close()
			return fmt.Errorf("store: upsert file %q: %w", f.Path, err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("store: upsert files: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: upsert files: commit: %w", err)
	}
	return nil
}

func (s *pgStore) ListFiles(ctx context.Context, repoID int64) ([]*model.File, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, repo_id, path, language, content_hash, module_name FROM files WHERE repo_id = $1`,
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list files: %w", err)
	}
	defer rows.Close()

	var files []*model.File
	for rows.Next() {
		f := &model.File{}
		if err := rows.Scan(&f.ID, &f.RepoID, &f.Path, &f.Language, &f.ContentHash, &f.ModuleName); err != nil {
			return nil, fmt.Errorf("store: list files: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list files: %w", err)
	}
	return files, nil
}

// GetFiles queries with `id = ANY($1)`, one round trip regardless of len(ids).
// The result map is keyed by File.ID; requesting an ID that isn't in the
// database (or passing an empty/nil ids) simply omits it, matching
// internal/mcp.Catalog's ID-hydration contract.
func (s *pgStore) GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error) {
	if len(ids) == 0 {
		return map[int64]*model.File{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, repo_id, path, language, content_hash, module_name FROM files WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get files: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]*model.File, len(ids))
	for rows.Next() {
		f := &model.File{}
		if err := rows.Scan(&f.ID, &f.RepoID, &f.Path, &f.Language, &f.ContentHash, &f.ModuleName); err != nil {
			return nil, fmt.Errorf("store: get files: %w", err)
		}
		out[f.ID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get files: %w", err)
	}
	return out, nil
}

func (s *pgStore) DeleteFile(ctx context.Context, fileID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM files WHERE id = $1`, fileID); err != nil {
		return fmt.Errorf("store: delete file: %w", err)
	}
	return nil
}

// DeleteFileContents deletes fileID's chunks and symbols in one transaction,
// leaving the File row itself in place. Chunks are deleted first and by
// file_id directly (not via a cascade off symbols), because module-level
// chunks (Chunk.SymbolID nil, per plan.md "AST-aware chunking") have no
// symbol row to cascade from. Deleting symbols afterwards cascades their
// edges and unresolved_refs per the FKs in migrations/00004 and 00005.
func (s *pgStore) DeleteFileContents(ctx context.Context, fileID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: delete file contents: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(ctx, `DELETE FROM chunks WHERE file_id = $1`, fileID); err != nil {
		return fmt.Errorf("store: delete file contents: delete chunks: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM symbols WHERE file_id = $1`, fileID); err != nil {
		return fmt.Errorf("store: delete file contents: delete symbols: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: delete file contents: commit: %w", err)
	}
	return nil
}

// Symbols

func (s *pgStore) InsertSymbols(ctx context.Context, symbols []*model.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}
	for _, sym := range symbols {
		if !sym.Kind.Valid() {
			return fmt.Errorf("store: insert symbols: invalid kind %q for %q", sym.Kind, sym.QualifiedName)
		}
	}

	ids, err := s.reserveIDs(ctx, "symbols", len(symbols))
	if err != nil {
		return fmt.Errorf("store: insert symbols: %w", err)
	}

	rows := make([][]any, len(symbols))
	for i, sym := range symbols {
		sym.ID = ids[i]
		rows[i] = []any{
			sym.ID, sym.FileID, string(sym.Kind), sym.Name, sym.QualifiedName,
			sym.ParentID, sym.StartLine, sym.EndLine, sym.Signature, sym.Docstring,
		}
	}

	_, err = s.pool.CopyFrom(ctx, pgx.Identifier{"symbols"},
		[]string{"id", "file_id", "kind", "name", "qualified_name", "parent_id", "start_line", "end_line", "signature", "docstring"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("store: insert symbols: %w", err)
	}
	return nil
}

func (s *pgStore) DeleteSymbolsForFile(ctx context.Context, fileID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM symbols WHERE file_id = $1`, fileID); err != nil {
		return fmt.Errorf("store: delete symbols for file: %w", err)
	}
	return nil
}

func (s *pgStore) FindSymbolByQualifiedName(ctx context.Context, repoID int64, qualifiedName string) (*model.Symbol, error) {
	sym := &model.Symbol{}
	err := s.pool.QueryRow(ctx,
		`SELECT s.id, s.file_id, s.kind, s.name, s.qualified_name, s.parent_id, s.start_line, s.end_line, s.signature, s.docstring
		 FROM symbols s
		 JOIN files f ON f.id = s.file_id
		 WHERE f.repo_id = $1 AND s.qualified_name = $2
		 ORDER BY s.file_id, s.start_line, s.id
		 LIMIT 1`,
		repoID, qualifiedName,
	).Scan(&sym.ID, &sym.FileID, &sym.Kind, &sym.Name, &sym.QualifiedName, &sym.ParentID, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Docstring)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: find symbol by qualified name: %w", err)
	}
	return sym, nil
}

func (s *pgStore) FindSymbolsByName(ctx context.Context, repoID int64, name string) ([]*model.Symbol, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.file_id, s.kind, s.name, s.qualified_name, s.parent_id, s.start_line, s.end_line, s.signature, s.docstring
		 FROM symbols s
		 JOIN files f ON f.id = s.file_id
		 WHERE f.repo_id = $1 AND s.name = $2`,
		repoID, name,
	)
	if err != nil {
		return nil, fmt.Errorf("store: find symbols by name: %w", err)
	}
	defer rows.Close()

	var symbols []*model.Symbol
	for rows.Next() {
		sym := &model.Symbol{}
		if err := rows.Scan(&sym.ID, &sym.FileID, &sym.Kind, &sym.Name, &sym.QualifiedName, &sym.ParentID, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Docstring); err != nil {
			return nil, fmt.Errorf("store: find symbols by name: %w", err)
		}
		symbols = append(symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: find symbols by name: %w", err)
	}
	return symbols, nil
}

// GetSymbols looks up symbols by ID with one `id = ANY($1)` query. The
// result map is keyed by Symbol.ID; an ID with no matching row is simply
// absent, matching internal/mcp.Catalog's ID-hydration contract.
func (s *pgStore) GetSymbols(ctx context.Context, ids []int64) (map[int64]*model.Symbol, error) {
	if len(ids) == 0 {
		return map[int64]*model.Symbol{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, file_id, kind, name, qualified_name, parent_id, start_line, end_line, signature, docstring
		 FROM symbols WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get symbols: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]*model.Symbol, len(ids))
	for rows.Next() {
		sym := &model.Symbol{}
		if err := rows.Scan(&sym.ID, &sym.FileID, &sym.Kind, &sym.Name, &sym.QualifiedName, &sym.ParentID, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Docstring); err != nil {
			return nil, fmt.Errorf("store: get symbols: %w", err)
		}
		out[sym.ID] = sym
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get symbols: %w", err)
	}
	return out, nil
}

func (s *pgStore) ListSymbols(ctx context.Context, repoID int64) ([]*model.Symbol, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.file_id, s.kind, s.name, s.qualified_name, s.parent_id, s.start_line, s.end_line, s.signature, s.docstring
		 FROM symbols s JOIN files f ON f.id = s.file_id
		 WHERE f.repo_id = $1 ORDER BY s.file_id, s.start_line, s.id`, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list symbols: %w", err)
	}
	defer rows.Close()
	var symbols []*model.Symbol
	for rows.Next() {
		sym := &model.Symbol{}
		if err := rows.Scan(&sym.ID, &sym.FileID, &sym.Kind, &sym.Name, &sym.QualifiedName, &sym.ParentID, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Docstring); err != nil {
			return nil, fmt.Errorf("store: list symbols: %w", err)
		}
		symbols = append(symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list symbols: %w", err)
	}
	return symbols, nil
}

// Edges

func (s *pgStore) InsertEdges(ctx context.Context, edges []*model.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	for _, e := range edges {
		if !e.Kind.Valid() {
			return fmt.Errorf("store: insert edges: invalid kind %q", e.Kind)
		}
	}

	ids, err := s.reserveIDs(ctx, "edges", len(edges))
	if err != nil {
		return fmt.Errorf("store: insert edges: %w", err)
	}

	rows := make([][]any, len(edges))
	for i, e := range edges {
		e.ID = ids[i]
		rows[i] = []any{e.ID, e.SrcSymbolID, e.DstSymbolID, string(e.Kind), float32(e.Confidence)}
	}

	_, err = s.pool.CopyFrom(ctx, pgx.Identifier{"edges"},
		[]string{"id", "src_symbol_id", "dst_symbol_id", "kind", "confidence"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("store: insert edges: %w", err)
	}
	return nil
}

func (s *pgStore) GetCallers(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return s.queryEdges(ctx, `SELECT id, src_symbol_id, dst_symbol_id, kind, confidence FROM edges WHERE dst_symbol_id = $1`, symbolID)
}

func (s *pgStore) GetCallees(ctx context.Context, symbolID int64) ([]*model.Edge, error) {
	return s.queryEdges(ctx, `SELECT id, src_symbol_id, dst_symbol_id, kind, confidence FROM edges WHERE src_symbol_id = $1`, symbolID)
}

func (s *pgStore) queryEdges(ctx context.Context, query string, symbolID int64) ([]*model.Edge, error) {
	rows, err := s.pool.Query(ctx, query, symbolID)
	if err != nil {
		return nil, fmt.Errorf("store: query edges: %w", err)
	}
	defer rows.Close()

	var edges []*model.Edge
	for rows.Next() {
		e := &model.Edge{}
		var confidence float32
		if err := rows.Scan(&e.ID, &e.SrcSymbolID, &e.DstSymbolID, &e.Kind, &confidence); err != nil {
			return nil, fmt.Errorf("store: query edges: %w", err)
		}
		e.Confidence = model.Confidence(confidence)
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query edges: %w", err)
	}
	return edges, nil
}

// LoadEdges returns every edge whose source symbol belongs to repoID in one
// query, joining through symbols to files to scope by repo (edges itself
// carries no repo_id). This is the bulk load internal/graph.Build needs;
// GetCallers/GetCallees stay single-symbol for the MCP handlers that don't
// need a whole-repo graph in memory. It does not implement graph.EdgeLoader
// directly since that interface's method takes no repoID — bind repoID with
// graph.EdgeLoaderFunc at the call site.
func (s *pgStore) LoadEdges(ctx context.Context, repoID int64) ([]*model.Edge, error) {
	return s.queryEdges(ctx,
		`SELECT e.id, e.src_symbol_id, e.dst_symbol_id, e.kind, e.confidence
		 FROM edges e
		 JOIN symbols s ON s.id = e.src_symbol_id
		 JOIN files f ON f.id = s.file_id
		 WHERE f.repo_id = $1`,
		repoID,
	)
}

func (s *pgStore) DeleteEdgesForRepo(ctx context.Context, repoID int64) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM edges e USING symbols s, files f
		 WHERE e.src_symbol_id = s.id AND s.file_id = f.id AND f.repo_id = $1`, repoID); err != nil {
		return fmt.Errorf("store: delete edges for repo: %w", err)
	}
	return nil
}

// Unresolved refs

func (s *pgStore) InsertUnresolvedRefs(ctx context.Context, refs []*model.UnresolvedRef) error {
	if len(refs) == 0 {
		return nil
	}
	for _, r := range refs {
		if !r.Kind.Valid() {
			return fmt.Errorf("store: insert unresolved refs: invalid kind %q", r.Kind)
		}
	}

	ids, err := s.reserveIDs(ctx, "unresolved_refs", len(refs))
	if err != nil {
		return fmt.Errorf("store: insert unresolved refs: %w", err)
	}

	rows := make([][]any, len(refs))
	for i, r := range refs {
		r.ID = ids[i]
		rows[i] = []any{r.ID, r.SrcSymbolID, r.Name, string(r.Kind)}
	}

	_, err = s.pool.CopyFrom(ctx, pgx.Identifier{"unresolved_refs"},
		[]string{"id", "src_symbol_id", "name", "kind"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("store: insert unresolved refs: %w", err)
	}
	return nil
}

// ListUnresolvedRefs returns every unresolved_refs row whose source symbol
// belongs to repoID, joining through symbols to files to scope by repo
// (unresolved_refs itself carries no repo_id), so recall gaps can be
// inspected or reported per repo (see model.UnresolvedRef).
func (s *pgStore) ListUnresolvedRefs(ctx context.Context, repoID int64) ([]*model.UnresolvedRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.src_symbol_id, r.name, r.kind
		 FROM unresolved_refs r
		 JOIN symbols s ON s.id = r.src_symbol_id
		 JOIN files f ON f.id = s.file_id
		 WHERE f.repo_id = $1`,
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list unresolved refs: %w", err)
	}
	defer rows.Close()

	var refs []*model.UnresolvedRef
	for rows.Next() {
		r := &model.UnresolvedRef{}
		if err := rows.Scan(&r.ID, &r.SrcSymbolID, &r.Name, &r.Kind); err != nil {
			return nil, fmt.Errorf("store: list unresolved refs: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list unresolved refs: %w", err)
	}
	return refs, nil
}

func (s *pgStore) DeleteUnresolvedRefsForRepo(ctx context.Context, repoID int64) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM unresolved_refs r USING symbols s, files f
		 WHERE r.src_symbol_id = s.id AND s.file_id = f.id AND f.repo_id = $1`, repoID); err != nil {
		return fmt.Errorf("store: delete unresolved refs for repo: %w", err)
	}
	return nil
}

// Chunks

func (s *pgStore) InsertChunks(ctx context.Context, chunks []*model.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	for _, c := range chunks {
		if c.Embedding != nil && len(c.Embedding) != s.embeddingDim {
			return fmt.Errorf("store: insert chunks: embedding has %d dims, want %d (see %s)",
				len(c.Embedding), s.embeddingDim, model.EmbeddingDimEnvVar)
		}
	}

	ids, err := s.reserveIDs(ctx, "chunks", len(chunks))
	if err != nil {
		return fmt.Errorf("store: insert chunks: %w", err)
	}

	rows := make([][]any, len(chunks))
	for i, c := range chunks {
		c.ID = ids[i]
		var embedding any
		if c.Embedding != nil {
			embedding = EncodeVector(c.Embedding)
		}
		rows[i] = []any{c.ID, c.SymbolID, c.FileID, c.Text, c.ContextHeader, c.TokenCount, c.StartLine, c.EndLine, embedding}
	}

	_, err = s.pool.CopyFrom(ctx, pgx.Identifier{"chunks"},
		[]string{"id", "symbol_id", "file_id", "text", "context_header", "token_count", "start_line", "end_line", "embedding"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("store: insert chunks: %w", err)
	}
	return nil
}

// GetChunks looks up chunks by ID with one `id = ANY($1)` query. The result
// map is keyed by Chunk.ID; an ID with no matching row is simply absent,
// matching internal/mcp.Catalog's ID-hydration contract. Embedding is nil in
// the result for chunks whose embedding column is NULL (not yet embedded),
// same as VectorSearch never returning those rows in the first place.
func (s *pgStore) GetChunks(ctx context.Context, ids []int64) (map[int64]*model.Chunk, error) {
	if len(ids) == 0 {
		return map[int64]*model.Chunk{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol_id, file_id, text, context_header, token_count, start_line, end_line, embedding
		 FROM chunks WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get chunks: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]*model.Chunk, len(ids))
	for rows.Next() {
		c := &model.Chunk{}
		var vec *pgvector.Vector
		if err := rows.Scan(&c.ID, &c.SymbolID, &c.FileID, &c.Text, &c.ContextHeader, &c.TokenCount, &c.StartLine, &c.EndLine, &vec); err != nil {
			return nil, fmt.Errorf("store: get chunks: %w", err)
		}
		if vec != nil {
			c.Embedding = DecodeVector(*vec)
		}
		out[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get chunks: %w", err)
	}
	return out, nil
}

func (s *pgStore) ListChunks(ctx context.Context, repoID int64) ([]*model.Chunk, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.symbol_id, c.file_id, c.text, c.context_header, c.token_count, c.start_line, c.end_line
		 FROM chunks c JOIN files f ON f.id = c.file_id
		 WHERE f.repo_id = $1 ORDER BY c.file_id, c.start_line, c.id`, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list chunks: %w", err)
	}
	defer rows.Close()
	var chunks []*model.Chunk
	for rows.Next() {
		c := &model.Chunk{}
		if err := rows.Scan(&c.ID, &c.SymbolID, &c.FileID, &c.Text, &c.ContextHeader, &c.TokenCount, &c.StartLine, &c.EndLine); err != nil {
			return nil, fmt.Errorf("store: list chunks: %w", err)
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list chunks: %w", err)
	}
	return chunks, nil
}

// VectorSearch validates query against the configured embedding dimension
// before ever sending it to Postgres, per the store contract that a
// dimension mismatch must fail fast rather than silently corrupting the
// HNSW index (see model.DefaultEmbeddingDim's doc comment). Only chunks
// with a non-null embedding are eligible.
func (s *pgStore) VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error) {
	return s.vectorSearch(ctx, "", 0, query, limit)
}

func (s *pgStore) VectorSearchByRepo(ctx context.Context, repoID int64, query []float32, limit int) ([]*model.Chunk, error) {
	if repoID <= 0 {
		return nil, fmt.Errorf("store: vector search by repo: repoID must be positive")
	}
	return s.vectorSearch(ctx, "f.repo_id = $2", repoID, query, limit)
}

func (s *pgStore) vectorSearch(ctx context.Context, repoFilter string, repoID int64, query []float32, limit int) ([]*model.Chunk, error) {
	if len(query) != s.embeddingDim {
		return nil, fmt.Errorf("store: vector search: query has %d dims, want %d (see %s)",
			len(query), s.embeddingDim, model.EmbeddingDimEnvVar)
	}

	where := "embedding IS NOT NULL"
	if repoFilter != "" {
		where += " AND " + repoFilter
	}
	join := ""
	if repoFilter != "" {
		join = " JOIN files f ON f.id = chunks.file_id"
	}
	paramLimit := 2
	if repoFilter != "" {
		paramLimit = 3
	}
	sql := fmt.Sprintf(
		`SELECT chunks.id, chunks.symbol_id, chunks.file_id, chunks.text, chunks.context_header, chunks.token_count, chunks.start_line, chunks.end_line, chunks.embedding
		 FROM chunks%s
		 WHERE %s
		 ORDER BY chunks.embedding %s $1
		 LIMIT $%d`,
		join, where, s.distanceOp, paramLimit,
	)
	args := []any{EncodeVector(query)}
	if repoFilter != "" {
		args = append(args, repoID)
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("store: vector search: %w", err)
	}
	defer rows.Close()

	var chunks []*model.Chunk
	for rows.Next() {
		c := &model.Chunk{}
		var vec pgvector.Vector
		if err := rows.Scan(&c.ID, &c.SymbolID, &c.FileID, &c.Text, &c.ContextHeader, &c.TokenCount, &c.StartLine, &c.EndLine, &vec); err != nil {
			return nil, fmt.Errorf("store: vector search: %w", err)
		}
		c.Embedding = DecodeVector(vec)
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: vector search: %w", err)
	}
	return chunks, nil
}

func (s *pgStore) Close() error {
	s.pool.Close()
	return nil
}
