# Tuning notes

Started per plan.md ("Days 13-14: hybrid fusion": "record what was tried").
This file accumulates defaults and their rationale as each wave lands;
later waves (graph boosting, cross-encoder rerank, eval-driven weight
tuning) should append, not rewrite.

## Reciprocal Rank Fusion (internal/retrieve)

**k = 60 (`retrieve.DefaultRRFK`).** This is plan.md's specified default
and the value from the original RRF paper (Cormack et al., 2009), which
found k=60 robust across a range of retrieval tasks without per-collection
tuning. It is exposed as `Config.RRFK` so the eval wave can sweep it
against the hand-labeled query set once available; no sweep has been run
yet.

**Per-retriever weights default to 1 (equal contribution).** With no eval
data yet to justify favoring BM25 or vector search, equal weighting is the
least-biased starting point. `Config.WeightBM25` / `Config.WeightVector`
are the knobs; note that `Fuse` treats an explicit weight of exactly `0` as
"unset" and substitutes `1` (see rrf.go), so down-weighting a retriever to
near-zero without disabling it requires a small positive value like
`0.01`, not `0`.

**Tie-break: descending fused score, then ascending chunk ID.** RRF
produces exact ties whenever chunks appear at the same set of ranks across
retrievers (very common for the many chunks that appear in exactly one
retriever's results, all contributing `weight / (k + rank)` for the same
rank). Breaking ties by chunk ID rather than, say, insertion order keeps
`HybridSearcher.Search` deterministic across repeated runs and across
process restarts (verified by `TestFuse_Determinism` and
`TestHybridSearch_Determinism`), which matters because the eval wave
compares metrics across runs and a shuffled tie order would look like
retrieval noise.

**Candidate pool size = the caller's requested `limit`.** Both retrievers
are asked for exactly `limit` results, then fused and re-truncated to
`limit`. This is the simplest correct approach and avoids introducing an
extra untuned parameter; if eval shows that fusing a wider pool (e.g. top
50 from each retriever, then truncating to the requested top 10) recovers
hits that fall just outside one retriever's narrow window, that should
become a documented, tuned parameter here rather than a silent default
change.

## Graph boosting and rerank (deferred)

Not implemented in this wave. `retrieve.BoostStage` is the seam a future
graph-adjacency boost (and, later, cross-encoder rerank) plugs into — see
the interface doc in `internal/retrieve/hybrid.go`. Once internal/graph
lands, the expected shape of that work and its tuning notes belong here.

## Retriever failure handling

BM25 and vector search run concurrently and independently; if one errors,
`HybridSearcher.Search` returns the surviving retriever's fused results
plus a `RetrieverError` in `Result.Failed`, rather than failing the whole
query. Only a simultaneous failure of every configured retriever surfaces
as an error return. This is a reliability default, not a tuned one, but is
recorded here because it affects how eval failures should be interpreted:
a `Result.Failed` entry means degraded (single-retriever) recall, not a
query-layer bug.
