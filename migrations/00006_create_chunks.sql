-- +goose Up
-- The `embedding vector(N)` column is added by the next migration
-- (00007_add_chunks_embedding.go), which reads its dimension from the
-- CORNIFER_EMBEDDING_DIM environment variable at migration time.
CREATE TABLE chunks (
    id             BIGSERIAL PRIMARY KEY,
    symbol_id      BIGINT REFERENCES symbols(id) ON DELETE CASCADE,
    file_id        BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    text           TEXT NOT NULL,
    context_header TEXT NOT NULL DEFAULT '',
    token_count    INTEGER NOT NULL,
    tsv            tsvector GENERATED ALWAYS AS (
                       to_tsvector('english', context_header || ' ' || text)
                   ) STORED
);

CREATE INDEX chunks_symbol_id_idx ON chunks (symbol_id);
CREATE INDEX chunks_file_id_idx ON chunks (file_id);
CREATE INDEX chunks_tsv_idx ON chunks USING GIN (tsv);

-- +goose Down
DROP TABLE chunks;
