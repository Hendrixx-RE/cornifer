package companion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("companion: not found")

// Store is the durable product-layer boundary. The engine's Store remains the
// authority for parsed symbols/chunks; this store owns registry, jobs, cache,
// and application memory only.
type Store interface {
	UpsertRepository(context.Context, Repository) (Repository, bool, error)
	GetRepository(context.Context, string) (Repository, error)
	ListRepositories(context.Context) ([]Repository, error)
	UpdateRepository(context.Context, Repository) error
	CreateJob(context.Context, Job) error
	GetJob(context.Context, string) (Job, error)
	LatestJob(context.Context, string) (Job, error)
	UpdateJob(context.Context, Job) error
	RequestCancel(context.Context, string) error
	CreateSession(context.Context, Session) error
	GetSession(context.Context, string) (Session, error)
	PutSessionEvent(context.Context, SessionEvent, int) error
	ListSessionEvents(context.Context, string, int) ([]SessionEvent, error)
	UpdateSession(context.Context, Session) error
	ClearSession(context.Context, string) error
	GetContextCache(context.Context, string, time.Time) (ContextPack, bool, error)
	PutContextCache(context.Context, string, Repository, ContextPack, time.Time, int) error
	Cleanup(context.Context, time.Time, int) error
	Close()
}

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("companion: connect product store: %w", err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("companion: ping product store: %w", err)
	}
	return &PostgresStore{pool: p}, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func scanRepo(row pgx.Row) (Repository, error) {
	var r Repository
	var caps []byte
	var engineID *int64
	err := row.Scan(&r.ID, &r.CanonicalURL, &r.RequestedRef, &r.ResolvedCommitSHA, &r.CheckoutPath, &r.CacheDir, &engineID, &r.Status, &caps, &r.ErrorCode, &r.SafeMessage, &r.IndexVersion, &r.ProviderFingerprint, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if engineID != nil {
		r.EngineRepoID = *engineID
	}
	if err := unmarshal(caps, &r.Capabilities); err != nil {
		return r, err
	}
	return r, nil
}

const repoColumns = `id, canonical_url, requested_ref, resolved_commit_sha, checkout_path, cache_dir, engine_repo_id, status, capabilities, error_code, safe_message, index_version, provider_fingerprint, created_at, updated_at`

func (s *PostgresStore) UpsertRepository(ctx context.Context, in Repository) (Repository, bool, error) {
	var existing Repository
	// Only work in progress is deduplicated. A completed record is a pinned
	// snapshot: ingesting a branch again intentionally creates a fresh row so a
	// moved ref cannot overwrite old context or memory.
	existing, err := scanRepo(s.pool.QueryRow(ctx, `SELECT `+repoColumns+` FROM companion_repositories WHERE canonical_url=$1 AND requested_ref=$2 AND status = ANY($3) ORDER BY updated_at DESC LIMIT 1`, in.CanonicalURL, in.RequestedRef, []string{string(StatusQueued), string(StatusCloning), string(StatusResolving), string(StatusIndexing), string(StatusAwaitingCredential)}))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Repository{}, false, fmt.Errorf("find repo: %w", err)
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.Status == "" {
		in.Status = StatusQueued
	}
	if in.IndexVersion == "" {
		in.IndexVersion = IndexVersion
	}
	if in.Capabilities == nil {
		in.Capabilities = []string{}
	}
	caps, err := marshal(in.Capabilities)
	if err != nil {
		return Repository{}, false, err
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO companion_repositories (id,canonical_url,requested_ref,resolved_commit_sha,checkout_path,cache_dir,engine_repo_id,status,capabilities,error_code,safe_message,index_version,provider_fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8,$9,$10,$11,$12,$13) RETURNING `+repoColumns,
		in.ID, in.CanonicalURL, in.RequestedRef, in.ResolvedCommitSHA, in.CheckoutPath, in.CacheDir, in.EngineRepoID, in.Status, caps, in.ErrorCode, in.SafeMessage, in.IndexVersion, in.ProviderFingerprint)
	r, err := scanRepo(row)
	if err != nil {
		return Repository{}, false, fmt.Errorf("create repo: %w", err)
	}
	return r, false, nil
}

func (s *PostgresStore) GetRepository(ctx context.Context, id string) (Repository, error) {
	r, err := scanRepo(s.pool.QueryRow(ctx, `SELECT `+repoColumns+` FROM companion_repositories WHERE id=$1`, id))
	if err != nil {
		return r, fmt.Errorf("get repo: %w", err)
	}
	return r, nil
}
func (s *PostgresStore) ListRepositories(ctx context.Context) ([]Repository, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+repoColumns+` FROM companion_repositories ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repository
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *PostgresStore) UpdateRepository(ctx context.Context, r Repository) error {
	caps, err := marshal(r.Capabilities)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM companion_repositories WHERE id=$1 FOR UPDATE`, r.ID).Scan(new(string)); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `UPDATE companion_repositories SET resolved_commit_sha=$2,checkout_path=$3,cache_dir=$4,engine_repo_id=NULLIF($5,0),status=$6,capabilities=$7,error_code=$8,safe_message=$9,index_version=$10,provider_fingerprint=$11,updated_at=now() WHERE id=$1`, r.ID, r.ResolvedCommitSHA, r.CheckoutPath, r.CacheDir, r.EngineRepoID, r.Status, caps, r.ErrorCode, r.SafeMessage, r.IndexVersion, r.ProviderFingerprint)
		if err != nil {
			return err
		}
		if ct.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
}

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.RepositoryID, &j.Phase, &j.FilesSeen, &j.FilesIndexed, &j.Chunks, &j.Edges, &j.Cancellable, &j.CancelRequested, &j.ErrorCode, &j.SafeMessage, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

const jobColumns = `id,repository_id,phase,files_seen,files_indexed,chunks,edges,cancellable,cancel_requested,error_code,safe_message,created_at,updated_at`

func (s *PostgresStore) CreateJob(ctx context.Context, j Job) error {
	if j.ID == "" {
		j.ID = newID()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO companion_jobs (id,repository_id,phase,files_seen,files_indexed,chunks,edges,cancellable,cancel_requested,error_code,safe_message) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, j.ID, j.RepositoryID, j.Phase, j.FilesSeen, j.FilesIndexed, j.Chunks, j.Edges, j.Cancellable, j.CancelRequested, j.ErrorCode, j.SafeMessage)
	return err
}
func (s *PostgresStore) GetJob(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM companion_jobs WHERE id=$1`, id))
	if err != nil {
		return j, fmt.Errorf("get job: %w", err)
	}
	return j, nil
}
func (s *PostgresStore) LatestJob(ctx context.Context, repositoryID string) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM companion_jobs WHERE repository_id=$1 ORDER BY updated_at DESC, created_at DESC LIMIT 1`, repositoryID))
	if err != nil {
		return j, fmt.Errorf("latest job: %w", err)
	}
	return j, nil
}
func (s *PostgresStore) UpdateJob(ctx context.Context, j Job) error {
	ct, err := s.pool.Exec(ctx, `UPDATE companion_jobs SET phase=$2,files_seen=$3,files_indexed=$4,chunks=$5,edges=$6,cancellable=$7,cancel_requested=$8,error_code=$9,safe_message=$10,updated_at=now() WHERE id=$1`, j.ID, j.Phase, j.FilesSeen, j.FilesIndexed, j.Chunks, j.Edges, j.Cancellable, j.CancelRequested, j.ErrorCode, j.SafeMessage)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) RequestCancel(ctx context.Context, id string) error {
	ct, err := s.pool.Exec(ctx, `UPDATE companion_jobs SET cancel_requested=true,updated_at=now() WHERE id=$1 AND cancellable=true`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func scanSession(row pgx.Row) (Session, error) {
	var v Session
	err := row.Scan(&v.ID, &v.RepositoryID, &v.CommitSHA, &v.RollingSummary, &v.Stale, &v.ExpiresAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CreateSession(ctx context.Context, v Session) error {
	if v.ID == "" {
		v.ID = newID()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO companion_sessions (id,repository_id,commit_sha,rolling_summary,stale,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, v.ID, v.RepositoryID, v.CommitSHA, v.RollingSummary, v.Stale, v.ExpiresAt)
	return err
}
func (s *PostgresStore) GetSession(ctx context.Context, id string) (Session, error) {
	v, err := scanSession(s.pool.QueryRow(ctx, `SELECT id,repository_id,commit_sha,rolling_summary,stale,expires_at,created_at,updated_at FROM companion_sessions WHERE id=$1`, id))
	if err != nil {
		return v, fmt.Errorf("get session: %w", err)
	}
	return v, nil
}
func (s *PostgresStore) UpdateSession(ctx context.Context, v Session) error {
	ct, err := s.pool.Exec(ctx, `UPDATE companion_sessions SET rolling_summary=$2,stale=$3,expires_at=$4,updated_at=now() WHERE id=$1`, v.ID, v.RollingSummary, v.Stale, v.ExpiresAt)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) PutSessionEvent(ctx context.Context, v SessionEvent, max int) error {
	b, err := marshal(v.Payload)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO companion_session_events (session_id,kind,payload) VALUES ($1,$2,$3)`, v.SessionID, v.Kind, b); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM companion_session_events WHERE session_id=$1 AND id NOT IN (SELECT id FROM companion_session_events WHERE session_id=$1 ORDER BY id DESC LIMIT $2)`, v.SessionID, max)
		return err
	})
}
func (s *PostgresStore) ListSessionEvents(ctx context.Context, id string, limit int) ([]SessionEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,session_id,kind,payload,created_at FROM companion_session_events WHERE session_id=$1 ORDER BY id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionEvent
	for rows.Next() {
		var v SessionEvent
		var b []byte
		if err := rows.Scan(&v.ID, &v.SessionID, &v.Kind, &b, &v.CreatedAt); err != nil {
			return nil, err
		}
		if err := unmarshal(b, &v.Payload); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ClearSession(ctx context.Context, id string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM companion_sessions WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetContextCache(ctx context.Context, key string, now time.Time) (ContextPack, bool, error) {
	var b []byte
	err := s.pool.QueryRow(ctx, `UPDATE companion_context_cache SET last_accessed_at=now() WHERE cache_key=$1 AND expires_at>$2 RETURNING payload`, key, now).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContextPack{}, false, nil
	}
	if err != nil {
		return ContextPack{}, false, err
	}
	var p ContextPack
	if err := unmarshal(b, &p); err != nil {
		return p, false, err
	}
	return p, true, nil
}
func (s *PostgresStore) PutContextCache(ctx context.Context, key string, r Repository, p ContextPack, expires time.Time, max int) error {
	b, err := marshal(p)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO companion_context_cache (cache_key,repository_id,commit_sha,index_version,provider_fingerprint,payload,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (cache_key) DO UPDATE SET payload=EXCLUDED.payload,expires_at=EXCLUDED.expires_at,last_accessed_at=now()`, key, r.ID, r.ResolvedCommitSHA, r.IndexVersion, r.ProviderFingerprint, b, expires)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM companion_context_cache WHERE cache_key IN (SELECT cache_key FROM companion_context_cache ORDER BY last_accessed_at DESC OFFSET $1)`, max)
		return err
	})
}
func (s *PostgresStore) Cleanup(ctx context.Context, now time.Time, maxSessions int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM companion_context_cache WHERE expires_at <= $1`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM companion_sessions WHERE expires_at <= $1`, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM companion_sessions WHERE id IN (SELECT id FROM companion_sessions ORDER BY updated_at DESC OFFSET $1)`, maxSessions)
		return err
	})
}
func (s *PostgresStore) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
