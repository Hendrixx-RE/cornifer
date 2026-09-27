-- +goose Up
-- Store.InsertChunks documents that Embedding may be nil ("insert text
-- first and embed later"), but 00007_add_chunks_embedding.go created the
-- column NOT NULL, which made that flow impossible. Relax the constraint to
-- match the documented contract; embedding presence is enforced instead at
-- read time by VectorSearch (`WHERE embedding IS NOT NULL`).
ALTER TABLE chunks ALTER COLUMN embedding DROP NOT NULL;

-- +goose Down
ALTER TABLE chunks ALTER COLUMN embedding SET NOT NULL;
