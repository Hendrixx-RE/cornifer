# Cornifer architecture

Cornifer indexes one Python repository snapshot at a time. A `repos` row
records the absolute checkout path, commit SHA, and the embedding provider/model
used to create its chunks. Provider metadata is part of correctness: vector
queries are only valid in the same embedding space as their indexed chunks.

`index` walks Python files, extracts symbols, resolves imports/calls, chunks
source at AST boundaries, embeds chunks, and writes files, symbols, edges,
unresolved references, and vectors to Postgres. It writes a BM25 index keyed
by repository ID under `.cornifer-cache/`; structural reads no longer consume
the old JSON manifest cache.

Read commands open a session by resolving the checkout's current commit and
bulk-loading files, symbols, edges, and chunk metadata from Postgres. Vector
search is explicitly scoped to that repository ID, preventing results from a
different repository or snapshot in the same database. Hybrid search fuses
BM25 and vector ranks with RRF, then optionally applies a small graph-adjacency
and same-file boost. The CLI and MCP enable this boost by default; evaluation
also runs the no-boost ablation.

`reindex` is content-hash incremental within an existing root+commit snapshot:
added and changed files are reparsed/rechunked/reembedded, deleted files are
removed, edges and unresolved references are rebuilt across all current parse
trees, and BM25 is rebuilt from the authoritative chunk rows. New commit SHAs
remain independent snapshots and use a clean index today.

MCP exposes `search_code`, `find_definition`, `find_references`,
`get_dependencies`, `get_call_graph`, `get_blast_radius`, and `find_cycles`.
All outputs are bounded and hydrate source locations through Store catalog
lookups.

For sidecar-backed snapshots, `CORNIFER_SIDECAR_ENDPOINT` and an immutable
`CORNIFER_SIDECAR_MODEL` identity are required at index time and checked at
query/eval/MCP time. `CORNIFER_SIDECAR_BATCH_SIZE` defaults to a bounded eight
texts for CPU sidecars; `CORNIFER_SIDECAR_TIMEOUT_SECONDS` can extend only the
local sidecar deadline without weakening hosted-provider timeouts. This avoids
silently querying a different vector space or retrying a long CPU request as
if it had failed.
