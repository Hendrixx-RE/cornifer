// Package bm25 defines the SparseIndex interface for lexical (BM25-style)
// retrieval over chunks, and implements it with an in-process, JSON-backed
// Okapi BM25 index (Index, in index.go), plus a code-aware tokenizer
// (Tokenize, in tokenize.go) that splits snake_case/camelCase/dotted
// identifiers while also keeping the unsplit identifier as a token.
//
// plan.md's "BM25" step offers a choice between this in-process index and
// Postgres FTS against the chunks.tsv column; this package picks the
// in-process option so the package has no database dependency and its
// tests run with no network access (see index.go's doc comment for the
// tradeoff). It depends on internal/model for the Chunk shape but is
// independent of internal/embed and internal/store; internal/retrieve is
// the only caller that combines this package's results with vector and
// graph signals.
package bm25
