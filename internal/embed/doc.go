// Package embed defines the Embedder interface used to turn chunk text into
// dense vectors, plus its concrete implementations: a hosted API client
// (Gemini native embedContent or Voyage voyage-code-3), a local HTTP sidecar client (e.g.
// text-embeddings-inference serving jina-embeddings-v2-base-code), and a
// deterministic fake for tests/CI (see plan.md "Embedding bridge
// (Go-specific)"). New selects among them via Config.Provider.
//
// Every Embedder returned by New batches requests, retries 429/5xx/network
// failures with exponential backoff and jitter, validates returned vector
// lengths against the configured dimension (model.DefaultEmbeddingDim
// unless overridden by model.EmbeddingDimEnvVar; see ResolveDimension), and
// — if Config.CacheDir is set — caches results on disk keyed by a hash of
// (model identifier, dimension, text). Gemini additionally namespaces the
// task/format, endpoint, title and document/query role; each distinct input
// is requested separately, with bounded worker/rate control and Retry-After.
//
// This package depends on internal/model for the vector dimension contract
// but not on internal/chunk or internal/store — callers own turning
// []model.Chunk into []string and writing the resulting vectors back
// (persistence belongs to internal/store, not here).
package embed
