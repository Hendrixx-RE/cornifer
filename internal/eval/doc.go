// Package eval implements the evaluation harness: loading hand-labeled
// queries from eval/queries.yaml, running each configured retrieval system
// (hybrid, hybrid-without-graph-boost, BM25-only, vector-only, a
// grep/ripgrep baseline) against the pinned target repo, and computing
// precision@5 / recall@5 / MRR broken out by query type (see plan.md
// "evaluation"). It depends on internal/retrieve, internal/graph, and
// internal/store to run systems under test, and writes results under
// eval/results/; it does not implement any retrieval logic itself.
package eval
