-- +goose Up
ALTER TABLE repos
    ADD COLUMN embedding_provider TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN embedding_model TEXT NOT NULL DEFAULT 'unknown';

-- +goose Down
ALTER TABLE repos
    DROP COLUMN embedding_model,
    DROP COLUMN embedding_provider;
