package bm25

import (
	"context"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Result is one SparseIndex search hit.
type Result struct {
	ChunkID int64
	Score   float64
}

// SparseIndex is a lexical (BM25-style) index over chunk text. Index is
// called with the full set of chunks to (re-)index for incremental reindex
// support; implementations decide whether that means an in-place update or
// a full rebuild.
type SparseIndex interface {
	Index(ctx context.Context, chunks []*model.Chunk) error
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}
