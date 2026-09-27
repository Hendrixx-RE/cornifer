-- +goose Up
CREATE TABLE symbols (
    id             BIGSERIAL PRIMARY KEY,
    file_id        BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('module', 'class', 'function', 'method', 'variable')),
    name           TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    parent_id      BIGINT REFERENCES symbols(id) ON DELETE CASCADE,
    start_line     INTEGER NOT NULL,
    end_line       INTEGER NOT NULL,
    signature      TEXT NOT NULL DEFAULT '',
    docstring      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX symbols_qualified_name_idx ON symbols (qualified_name);
CREATE INDEX symbols_file_id_idx ON symbols (file_id);
CREATE INDEX symbols_parent_id_idx ON symbols (parent_id);

-- +goose Down
DROP TABLE symbols;
