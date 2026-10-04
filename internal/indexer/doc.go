// Package indexer orchestrates Cornifer's indexing pipeline end to end:
// internal/walker -> internal/parse -> internal/symbols -> internal/store
// (repo/files/symbols, with a repo-unique ID remap — see remap.go) ->
// internal/resolve -> internal/store (edges/unresolved refs) ->
// internal/chunk -> internal/embed -> internal/store (chunks) -> a
// persisted internal/bm25 index, per plan.md's Week 1/Week 2 exit
// criteria. cmd/cornifer is the only caller; keeping this out of package
// main makes the pipeline unit-testable without a CLI.
//
// # Read-side storage
//
// Store now supplies repo-scoped bulk files, symbols, edges, and chunks.
// OpenSession bulk-loads those rows each time a CLI command starts, so
// structural/catalog answers cannot drift from Postgres. Manifest remains an
// in-memory transport shape shared by existing command rendering code; its
// old JSON persistence is no longer read. The sole local cache is the
// persisted BM25 index, keyed by repo ID. Dense search is also repo-scoped.
package indexer
