// Package embed defines the Embedder interface used to turn chunk text into
// dense vectors, plus (in later waves) its concrete implementations: a
// hosted API client (Voyage) and/or a local sidecar (see plan.md "Embedding
// bridge (Go-specific)"). It depends on internal/model for the vector
// dimension contract (model.DefaultEmbeddingDim /
// model.EmbeddingDimEnvVar) but not on internal/chunk or internal/store —
// callers own the batching between chunking and storage.
package embed
