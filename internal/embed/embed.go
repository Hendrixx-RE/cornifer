package embed

import (
	"context"
	"fmt"
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

// New builds an Embedder from cfg, selected by cfg.Provider:
//
//   - ProviderGemini wraps native Gemini embedContent, one request/input.
//   - ProviderVoyage wraps the Voyage voyage-code-3 HTTP API.
//   - ProviderSidecar wraps a local HTTP sidecar (sentence-transformers or
//     text-embeddings-inference) exposing an /embed-style endpoint.
//   - ProviderFake returns deterministic, hash-derived vectors with no
//     network access, for tests and CI.
//
// The returned Embedder always validates vector dimensions against the
// resolved embedding dimension (see ResolveDimension) and fails fast on
// mismatch. Non-fake providers are wrapped with batching and exponential
// backoff-with-jitter retry on 429/5xx/network errors. If cfg.CacheDir is
// set, the result is further wrapped in an on-disk cache keyed by a hash of
// (model identifier, dimension, text).
func New(cfg Config) (Embedder, error) {
	dim, err := ResolveDimension(cfg.Dimension)
	if err != nil {
		return nil, err
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
		if cfg.Provider == ProviderSidecar {
			batchSize = DefaultSidecarBatchSize
		}
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	switch cfg.Provider {
	case ProviderGemini:
		client, err := newGeminiEmbedder(cfg.Gemini, dim, maxRetries)
		if err != nil {
			return nil, err
		}
		identity, _ := GeminiIdentity(cfg.Gemini, dim)
		// Input role is independent of model space: document/query vectors
		// must never share cache entries, even for identical source text.
		return maybeCache(client, cfg.CacheDir, "gemini;"+identity+";input_type="+client.inputType, dim), nil
	case ProviderVoyage:
		client, err := newVoyageClient(cfg.Voyage, dim)
		if err != nil {
			return nil, err
		}
		be := &batchingEmbedder{client: client, batchSize: batchSize, maxRetries: maxRetries, dim: dim}
		return maybeCache(be, cfg.CacheDir, client.model, dim), nil

	case ProviderSidecar:
		client, err := newSidecarClient(cfg.Sidecar)
		if err != nil {
			return nil, err
		}
		be := &batchingEmbedder{client: client, batchSize: batchSize, maxRetries: maxRetries, dim: dim}
		return maybeCache(be, cfg.CacheDir, client.model, dim), nil

	case ProviderFake:
		fe := newFakeEmbedder(dim)
		return maybeCache(fe, cfg.CacheDir, "fake", dim), nil

	default:
		return nil, fmt.Errorf("embed: unknown provider %q", cfg.Provider)
	}
}

func maybeCache(inner Embedder, dir, modelID string, dim int) Embedder {
	if dir == "" {
		return inner
	}
	return &cachingEmbedder{inner: inner, dir: dir, model: modelID, dim: dim}
}
