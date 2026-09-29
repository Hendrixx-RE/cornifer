-- +goose Up
-- model.Chunk.StartLine/EndLine (added by the chunk PR) were never
-- persisted: InsertChunks silently dropped them and every read left them
-- zero-valued. Backfill the columns so a chunk's source span survives a
-- round trip. Existing rows (inserted before this migration) get 0/0,
-- which read as "unknown span" rather than a real 1-indexed line range.
ALTER TABLE chunks ADD COLUMN start_line INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN end_line INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chunks DROP COLUMN start_line;
ALTER TABLE chunks DROP COLUMN end_line;
