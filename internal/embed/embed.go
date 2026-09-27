package embed

import (
	"context"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Embedder turns a batch of texts into dense vectors, one per input text in
// the same order. Implementations own batching limits, retry/backoff, and
// any on-disk cache keyed by content hash (see plan.md "embeddings and
// ANN"); callers may pass arbitrarily large slices. Every returned vector
// must have length equal to the configured embedding dimension
// (model.DefaultEmbeddingDim unless overridden by model.EmbeddingDimEnvVar).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// unimplemented is the Phase 0 stub Embedder. Later waves replace it with a
// Voyage API client and/or a local sidecar client.
type unimplemented struct{}

// New returns the Phase 0 stub Embedder, which always returns
// model.ErrNotImplemented.
func New() Embedder {
	return unimplemented{}
}

func (unimplemented) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, model.ErrNotImplemented
}
