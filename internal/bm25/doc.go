// Package bm25 defines the SparseIndex interface for lexical (BM25-style)
// retrieval over chunks, plus (in later waves) its implementation — either
// an in-process, disk-persisted BM25 index or Postgres full-text search
// (see plan.md "BM25": "pick one, keep it behind an interface"). It depends
// on internal/model for the Chunk shape but is independent of
// internal/embed and internal/store; internal/retrieve is the only caller
// that combines this package's results with vector and graph signals.
package bm25
