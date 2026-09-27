// Package retrieve combines internal/bm25 lexical search, internal/store
// vector search, and internal/graph proximity into a single ranked result
// list: Reciprocal Rank Fusion across BM25 and vector hits, then an
// optional graph-adjacency boost, then an optional cross-encoder rerank of
// the top candidates (see plan.md "hybrid fusion"). It is the only package
// that depends on bm25, embed, store, and graph together; internal/mcp is
// its caller, not the other way around.
package retrieve
