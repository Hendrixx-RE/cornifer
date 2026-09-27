-- +goose Up
CREATE TABLE files (
    id            BIGSERIAL PRIMARY KEY,
    repo_id       BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    path          TEXT NOT NULL,
    language      TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    module_name   TEXT NOT NULL DEFAULT '',
    UNIQUE (repo_id, path)
);

CREATE INDEX files_repo_id_idx ON files (repo_id);

-- +goose Down
DROP TABLE files;
