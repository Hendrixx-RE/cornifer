// Package retrieve combines internal/bm25 lexical search with dense vector
// search into a single ranked result list via Reciprocal Rank Fusion
// (plan.md "Days 13-14: hybrid fusion"), with per-retriever provenance
// (which retriever(s) surfaced a hit, and at what rank) preserved through
// fusion for the eval wave.
//
// This package depends on internal/bm25 and internal/embed directly, but
// deliberately not on internal/store or internal/graph: vector search is
// expressed as the narrow VectorSearcher interface (see hybrid.go), which
// internal/store's Store satisfies without an adapter, so this package
// builds and tests independently of the store package's development.
// GraphBoost is the built-in, configurable graph-adjacency post-fusion stage;
// callers supply repo-scoped chunk metadata and graph rows. Cross-encoder
// reranking remains optional and out of scope.
package retrieve
