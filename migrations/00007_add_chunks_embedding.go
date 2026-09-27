package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/pressly/goose/v3"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func init() {
	goose.AddMigrationContext(upAddChunksEmbedding, downAddChunksEmbedding)
}

// embeddingDim resolves the pgvector column width for chunks.embedding from
// model.EmbeddingDimEnvVar, falling back to model.DefaultEmbeddingDim. See
// internal/model/chunk.go for why this is fixed at migration time rather
// than read at query time.
func embeddingDim() (int, error) {
	v := os.Getenv(model.EmbeddingDimEnvVar)
	if v == "" {
		return model.DefaultEmbeddingDim, nil
	}
	dim, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not an integer: %w", model.EmbeddingDimEnvVar, v, err)
	}
	if dim <= 0 {
		return 0, fmt.Errorf("%s=%d must be positive", model.EmbeddingDimEnvVar, dim)
	}
	return dim, nil
}

func upAddChunksEmbedding(ctx context.Context, tx *sql.Tx) error {
	dim, err := embeddingDim()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`ALTER TABLE chunks ADD COLUMN embedding vector(%d) NOT NULL`, dim,
	)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`CREATE INDEX chunks_embedding_hnsw_idx ON chunks USING hnsw (embedding vector_cosine_ops)`,
	)
	return err
}

func downAddChunksEmbedding(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS chunks_embedding_hnsw_idx`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE chunks DROP COLUMN IF EXISTS embedding`)
	return err
}
