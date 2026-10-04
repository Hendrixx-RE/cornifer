-- +goose Up
-- Product-layer records sit beside immutable engine snapshots. The current
-- registry tracks the latest resolved commit for a URL+ref; every session and
-- cache row retains that commit and sessions are marked stale when it changes.
CREATE TABLE companion_repositories (
    id                  TEXT PRIMARY KEY,
    canonical_url       TEXT NOT NULL,
    requested_ref       TEXT NOT NULL DEFAULT '',
    resolved_commit_sha TEXT NOT NULL DEFAULT '',
    checkout_path       TEXT NOT NULL DEFAULT '',
    cache_dir           TEXT NOT NULL DEFAULT '',
    engine_repo_id      BIGINT REFERENCES repos(id) ON DELETE SET NULL,
    status              TEXT NOT NULL,
    capabilities        JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_code          TEXT NOT NULL DEFAULT '',
    safe_message        TEXT NOT NULL DEFAULT '',
    index_version       TEXT NOT NULL DEFAULT '',
    provider_fingerprint TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (canonical_url, requested_ref)
);

CREATE TABLE companion_jobs (
    id                  TEXT PRIMARY KEY,
    repository_id       TEXT NOT NULL REFERENCES companion_repositories(id) ON DELETE CASCADE,
    phase               TEXT NOT NULL,
    files_seen          INTEGER NOT NULL DEFAULT 0,
    files_indexed       INTEGER NOT NULL DEFAULT 0,
    chunks              INTEGER NOT NULL DEFAULT 0,
    edges               INTEGER NOT NULL DEFAULT 0,
    cancellable         BOOLEAN NOT NULL DEFAULT true,
    cancel_requested    BOOLEAN NOT NULL DEFAULT false,
    error_code          TEXT NOT NULL DEFAULT '',
    safe_message        TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX companion_jobs_repository_updated_idx ON companion_jobs (repository_id, updated_at DESC);

CREATE TABLE companion_sessions (
    id                  TEXT PRIMARY KEY,
    repository_id       TEXT NOT NULL REFERENCES companion_repositories(id) ON DELETE CASCADE,
    commit_sha          TEXT NOT NULL,
    rolling_summary     TEXT NOT NULL DEFAULT '',
    stale               BOOLEAN NOT NULL DEFAULT false,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX companion_sessions_scope_idx ON companion_sessions (repository_id, commit_sha, updated_at DESC);
CREATE INDEX companion_sessions_expiry_idx ON companion_sessions (expires_at);

CREATE TABLE companion_session_events (
    id                  BIGSERIAL PRIMARY KEY,
    session_id          TEXT NOT NULL REFERENCES companion_sessions(id) ON DELETE CASCADE,
    kind                TEXT NOT NULL,
    payload             JSONB NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX companion_session_events_session_idx ON companion_session_events (session_id, id DESC);

CREATE TABLE companion_context_cache (
    cache_key           TEXT PRIMARY KEY,
    repository_id       TEXT NOT NULL REFERENCES companion_repositories(id) ON DELETE CASCADE,
    commit_sha          TEXT NOT NULL,
    index_version       TEXT NOT NULL,
    provider_fingerprint TEXT NOT NULL,
    payload             JSONB NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    last_accessed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX companion_context_cache_lru_idx ON companion_context_cache (last_accessed_at);

-- +goose Down
DROP TABLE companion_context_cache;
DROP TABLE companion_session_events;
DROP TABLE companion_sessions;
DROP TABLE companion_jobs;
DROP TABLE companion_repositories;
