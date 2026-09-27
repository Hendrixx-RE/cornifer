// Package migrations holds goose migrations for the Postgres schema
// described in plan.md ("Data model (Postgres)"): repos, files, symbols,
// edges, unresolved_refs, and chunks, plus their indexes. Most migrations
// are plain .sql files; 00007_add_chunks_embedding.go is a Go migration
// because the `chunks.embedding` column's dimension must be read from the
// CORNIFER_EMBEDDING_DIM environment variable at migration time (pgvector
// fixes a column's dimension at creation — see model.DefaultEmbeddingDim).
//
// This package owns schema only. internal/store owns all runtime reads and
// writes against that schema; nothing outside cmd/migrate should import
// this package directly.
//
// Run migrations with `make migrate` (see cmd/migrate).
package migrations
