// Package indexer orchestrates Cornifer's indexing pipeline end to end:
// internal/walker -> internal/parse -> internal/symbols -> internal/store
// (repo/files/symbols, with a repo-unique ID remap — see remap.go) ->
// internal/resolve -> internal/store (edges/unresolved refs) ->
// internal/chunk -> internal/embed -> internal/store (chunks) -> a
// persisted internal/bm25 index, per plan.md's Week 1/Week 2 exit
// criteria. cmd/cornifer is the only caller; keeping this out of package
// main makes the pipeline unit-testable without a CLI.
//
// # Why a local cache, not just Postgres
//
// internal/store's Store interface (as merged for this wave) offers
// point lookups (FindSymbolByQualifiedName, FindSymbolsByName,
// GetCallers/GetCallees by one symbol ID) but no bulk "every symbol/edge/
// chunk in this repo" query — no LoadEdges(repo), no GetSymbols/GetChunks
// by file, no ListEdges. That is expected: a parallel wave is adding those
// (see the Store doc comment), and this package was told not to modify
// internal/store to add them itself.
//
// Structural queries (find-definition, callers, callees, blast-radius,
// cycles) and query's chunk-metadata display need exactly that bulk
// access, so Index additionally writes a Manifest (see cache.go) — every
// File, Symbol, Edge, and a lightweight per-Chunk record (without
// embeddings, which stay in Postgres and are searched via
// Store.VectorSearch) — to a JSON file on disk, keyed by repo ID. Query
// commands load the Manifest instead of re-deriving this from Postgres.
// Once the parallel Store work merges, the Manifest and this indirection
// can be dropped in favor of loading directly from Store; see cache.go's
// doc comment for the exact shape kept in sync with that expectation.
package indexer
