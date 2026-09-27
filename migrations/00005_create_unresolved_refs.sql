-- +goose Up
CREATE TABLE unresolved_refs (
    id            BIGSERIAL PRIMARY KEY,
    src_symbol_id BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('imports', 'calls', 'inherits', 'implements'))
);

CREATE INDEX unresolved_refs_src_symbol_id_idx ON unresolved_refs (src_symbol_id);

-- +goose Down
DROP TABLE unresolved_refs;
