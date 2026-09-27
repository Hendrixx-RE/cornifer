-- +goose Up
CREATE TABLE edges (
    id            BIGSERIAL PRIMARY KEY,
    src_symbol_id BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    dst_symbol_id BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('imports', 'calls', 'inherits', 'implements')),
    confidence    REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1)
);

CREATE INDEX edges_src_symbol_id_idx ON edges (src_symbol_id);
CREATE INDEX edges_dst_symbol_id_idx ON edges (dst_symbol_id);

-- +goose Down
DROP TABLE edges;
