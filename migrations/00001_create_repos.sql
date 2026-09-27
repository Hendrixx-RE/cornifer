-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE repos (
    id          BIGSERIAL PRIMARY KEY,
    root        TEXT NOT NULL,
    commit_sha  TEXT NOT NULL,
    indexed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (root, commit_sha)
);

-- +goose Down
DROP TABLE repos;
