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
// Graph-adjacency boosting and optional cross-encoder rerank are out of
// scope for this wave (plan.md marks rerank optional/eval-gated, and graph
// boosting depends on internal/graph, built in parallel); BoostStage is the
// seam a future graph-aware boost stage plugs into, and NoBoost is the
// default no-op until one is wired in by the caller (internal/mcp or the
// CLI).
package retrieve
