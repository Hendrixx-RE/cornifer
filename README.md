# Cornifer

Repository intelligence engine: parses a real codebase into a **structural
index** (AST → symbols → import/call graph) and a **semantic index**
(AST-aware chunks → embeddings + BM25), answers queries by combining exact
structural lookups with hybrid retrieval, and exposes it all as an **MCP
server**.

See [plan.md](plan.md) for the full architecture, phased build plan, and
design decisions.

## Status

**The core indexing, retrieval, MCP, and evaluation paths are implemented.** `cornifer index`
runs the full pipeline (walk → parse → symbols → resolve → chunk → embed →
store → BM25) against a real repo; `cornifer query`, `find-definition`,
`callers`, `callees`, `blast-radius`, and `cycles` answer structural and
hybrid-semantic questions against the result. **Week 3 evaluation is now
implemented:** `cornifer eval` validates pinned YAML labels, runs graph-boosted hybrid and its no-boost ablation, BM25-only, vector-only, and ripgrep, calculates precision@5 / recall@5 / MRR
overall and by query type, and writes the raw ranks and metadata to JSON.

The checked-in real result indexes the pinned FastAPI **`fastapi/` source
package only** (44 Python files, 716 symbols, 524 edges, and 402 chunks), not
tests or `docs_src/`. It used a local CPU sidecar with the real Jina code model
and completed in 47.175s (46.426s embedding time). The corpus boundary is
important: these numbers are a reproducible source-package benchmark, not a
claim about a full 1,306-file FastAPI checkout.

### Evaluation status

The committed [evaluation set](eval/queries.yaml) contains **22 FastAPI
queries** at pinned commit `40e33e492dbf4af6172997f4e3238a32e56cbe26`:
7 structural, 7 semantic, and 8 identifier tasks. Every current label is
marked **source-verified** and points to a source span that `cornifer eval`
checks against the pinned checkout. There are **zero IDE-verified labels** in
this first set: no Pylance/Pyright "find references" result is asserted or
inferred.

The raw [real result](eval/results/fastapi-40e33e492db-jina-code-source-128.json)
uses `jinaai/jina-embeddings-v2-base-code` revision
`516f4baf13dec4ddddda8631e019b5737c8bc250`, 768 dimensions, model mean
pooling, cosine similarity, symmetric query/document encoding, and a 128-token
input cap. It was run against an isolated local PostgreSQL 16.6 + pgvector
0.8.1 database. See [the evaluation note](docs/evaluation-fastapi.md) for
per-type metrics, the graph ablation, and failure analysis.

| Retrieval system | P@5 | R@5 | MRR |
| --- | ---: | ---: | ---: |
| Hybrid with graph boost | 0.173 | 0.864 | 0.642 |
| Hybrid without graph boost | 0.164 | 0.818 | 0.678 |
| BM25 | 0.118 | 0.591 | 0.470 |
| Vector | 0.173 | 0.864 | 0.600 |
| Ripgrep | 0.018 | 0.091 | 0.061 |

## Quickstart

```sh
make up           # start Postgres (pgvector) via docker compose
make migrate       # apply schema migrations
make fetch-repo     # clone the pinned target repo (see TARGET_REPO) into repos/
make test           # go test ./... (no database required; DB-backed tests skip cleanly)

go run ./cmd/cornifer index --repo repos/fastapi         # build the index (defaults to the fake embedder without VOYAGE_API_KEY)
go run ./cmd/cornifer query "rate limiting" --repo repos/fastapi
go run ./cmd/cornifer find-definition fastapi.routing.APIRoute
go run ./cmd/cornifer callers fastapi.applications.FastAPI.add_api_route
go run ./cmd/cornifer blast-radius fastapi/routing.py
go run ./cmd/cornifer cycles --repo repos/fastapi
go run ./cmd/cornifer reindex --repo repos/fastapi # content-hash incremental update

# After index has completed against the same cache/database:
go run ./cmd/cornifer eval --repo repos/fastapi
# writes eval/results/fastapi-<commit>-<provider>.json by default
```

Every read command (`query`, `find-definition`, `callers`, `callees`,
`blast-radius`, `cycles`) needs `cornifer index` to have run first for that
repo. Structural/catalog data is reloaded directly from Postgres for the
matching commit; only the BM25 index is a local cache.

## Architecture

```text
Python repo ──> walker / tree-sitter ──> symbols + resolver ──> Postgres
                      │                       │                 │
                      └──> AST chunks ──> embeddings ───────────┤
                                              │                  │
                                    persisted BM25 cache          │
                                                                 ▼
CLI / MCP ──> repo-scoped Store snapshot ──> BM25 + vector ──> RRF
                                                        │          │
                                                        └─ graph boost
```

See [docs/architecture.md](docs/architecture.md) for storage boundaries,
provenance, and incremental-update behavior.

## Layout

See plan.md's "Proposed layout" for the full directory breakdown. Key
entry points:

- `cmd/cornifer` — CLI (`index`, `reindex`, `query`, `find-definition`,
  `callers`, `callees`, `blast-radius`, `cycles`, `eval`)
- `cmd/cornifer-mcp` — MCP server (stdio transport)
- `cmd/migrate` — applies `migrations/` with goose
- `internal/indexer` — orchestrates the indexing pipeline end to end and
  loads it back for read commands (see `internal/indexer/doc.go`)
- `internal/model` — shared domain types (`Repo`, `File`, `Symbol`, `Edge`,
  `Chunk`, `UnresolvedRef`) that every other package imports
- `migrations/` — Postgres schema (see `migrations/doc.go`)

## Configuration

| Env var / flag | Default | Purpose |
|---|---|---|
| `CORNIFER_DATABASE_URL` | `postgres://cornifer:cornifer@localhost:5433/cornifer?sslmode=disable` | Postgres connection string used by every command |
| `CORNIFER_EMBEDDING_DIM` | `1024` (`model.DefaultEmbeddingDim`) | Dimension of the `chunks.embedding` pgvector column, read at migration time |
| `VOYAGE_API_KEY` | unset | If set, `index`/`query` default to the real Voyage `voyage-code-3` embedder; otherwise they default to a deterministic, network-free fake so the pipeline runs anywhere. Override explicitly with `--embed-provider voyage\|sidecar\|fake` |
| `CORNIFER_SIDECAR_ENDPOINT` | unset | Required `/embed` endpoint when `--embed-provider sidecar` is used |
| `CORNIFER_SIDECAR_MODEL` | unset | Required immutable sidecar model identity (include revision and contract); persisted with the snapshot and checked by query/eval/MCP |
| `CORNIFER_SIDECAR_BATCH_SIZE` | `8` for sidecars | Bounds a sidecar request; `tools/local_embed_sidecar.py` uses the same value for its CPU model batch |
| `CORNIFER_SIDECAR_TIMEOUT_SECONDS` | `60` | Optional sidecar-only HTTP deadline; set it for a slow CPU model without changing hosted-provider timeouts |
| `--cache-dir` | `.cornifer-cache/` | Where the persisted BM25 index is written/read; structural data stays in Postgres |
| `--max-embed-tokens` | `8000` | Per-chunk embedding-input token ceiling; oversized chunks (plan.md's "one FastAPI chunk is ~50K tokens" case) are truncated for embedding, with a warning, not skipped |

## Evaluation methodology

`cornifer eval` loads `eval/queries.yaml`, refuses an index whose commit does
not match the dataset's pinned target, validates all source-verified spans,
and writes a JSON report containing exact top-five source locations and
per-query metrics, aggregate precision@5 / recall@5 / MRR (with structural /
semantic / identifier breakouts), target and indexed commit SHA, embedding
provider/model metadata, label provenance counts, and graph-boost status.

The ripgrep baseline runs case-insensitive whole-word searches for the
code-aware query tokens and ranks matching lines by the number of distinct
query tokens. Its raw results remain line-level; Cornifer's other systems
return AST chunk spans. Precision@5 has a fixed denominator of five, recall
deduplicates labelled source spans, and MRR uses the first relevant result.

The default fake provider is useful for an offline pipeline check only. Its
hash-derived vectors are explicitly tagged `semantically_meaningful: false`
in results, so any vector/hybrid number from it is not evidence of semantic
retrieval quality. Use Voyage or a correctly configured local sidecar for a
semantic comparison, and preserve the resulting raw JSON under
`eval/results/`.

Embedding provider/model are persisted on each indexed repository snapshot.
`query`, `eval`, and MCP use that provenance to avoid mixing vector spaces;
an explicit incompatible `--embed-provider` fails rather than producing a
plausible but invalid ranking. `--indexed-embed-provider` remains a legacy
override for snapshots indexed before migration 00010.

## Known limitations

- **Incremental indexing is snapshot-local.** For an already-indexed
  root+commit, `reindex` compares content hashes and only re-parses,
  re-chunks, and re-embeds added/changed files; it deletes removed files and
  re-resolves the complete graph for correctness. A new commit remains a new
  immutable snapshot and currently takes the clean full-index path rather
  than copying unchanged rows across snapshots.
- **Call/import resolution is heuristic**, not type inference — see
  `internal/resolve/doc.go`. `cornifer index` logs per-kind resolution
  ratios every run.
- **The fake embedder is not semantically meaningful.** Without
  `VOYAGE_API_KEY`, vector-search results are deterministic but
  hash-derived, not based on code meaning; BM25 still works normally. Set
  `VOYAGE_API_KEY` (or point `--embed-provider sidecar` at a local model)
  to see real semantic recall.
- **Graph boosting is heuristic.** Graph-adjacent/same-file chunks receive a
  small configurable post-RRF score (`--graph-boost-weight`, default
  `0.002`). The real source-package run records both rankings: boosting raised
  recall@5 (0.864 vs. 0.818) but reduced MRR (0.642 vs. 0.678). It is an
  observed tradeoff on this corpus, not a general performance claim.
- **The FastAPI ground truth is source-verified, not IDE-verified.** The
  committed labels are direct pinned-source targets. A future structural
  evaluation pass should independently record Pyright/Pylance reference
  checks, with tooling/version and the exact source commit, rather than
  relabelling these existing entries.
- **The committed result is source-package scoped.** All 22 labels fall under
  `fastapi/`, but the benchmark excludes FastAPI's tests and examples. A full
  1,306-file checkout with this CPU-only Jina setup at a 512-token model cap
  was not completed; it must not be compared directly with this result.
- **CPU sidecar truncation is material.** Jina's card documents support up to
  8,192 positions and training at 512; this resource-bounded run uses 128.
  One oversized `FastAPI.__init__` chunk was still truncated by Cornifer's
  8,000-token pre-embedding ceiling before the model cap.

## Remaining plan gaps

The remaining required work is an independently recorded IDE-verified
structural label pass and a clearly separated full-checkout benchmark on
adequate local compute. Incremental updates, graph boosting/ablation,
persisted embedding provenance, a real source-package result, and all seven
MCP tools are implemented. Optional REST and reranking remain intentionally
out of scope.

## Target repo

The indexing pipeline is exercised against [fastapi/fastapi](https://github.com/fastapi/fastapi)
pinned to a specific commit SHA (recorded in [TARGET_REPO](TARGET_REPO)) so
eval labels stay valid as the upstream repo changes. `make fetch-repo` clones
it into the gitignored `repos/` directory.
