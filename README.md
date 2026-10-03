# Cornifer

Repository intelligence engine: parses a real codebase into a **structural
index** (AST → symbols → import/call graph) and a **semantic index**
(AST-aware chunks → embeddings + BM25), answers queries by combining exact
structural lookups with hybrid retrieval, and exposes it all as an **MCP
server**.

See [plan.md](plan.md) for the full architecture, phased build plan, and
design decisions.

The implemented engine also backs a local companion product: `cornifer-serve`
hosts a loopback website and Streamable HTTP MCP endpoint over the same
repository snapshots and cited context packs. The current product contract and
limitations are in [docs/product-plan.md](docs/product-plan.md).

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
7 structural, 7 semantic, and 8 identifier tasks. The seven structural labels
are **IDE-verified** by Pyright 1.1.412 LSP definition and reference requests;
the durable [verification evidence](eval/verification/fastapi-pyright-lsp-40e33e492.json)
records every returned target. The other 15 labels remain **source-verified**
and are not presented as IDE checks.

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

# Preferred semantic path: set the secret only in your shell/environment.
# voyage-code-3 uses 1024-d float vectors; migrate with that dimension first.
export VOYAGE_API_KEY='…' # never commit this value
# Use a new/empty database migrated at 1024 dimensions. Existing 768-d
# sidecar databases cannot hold Voyage's 1024-d vectors.
go run ./cmd/cornifer index --repo repos/fastapi --embed-provider voyage
go run ./cmd/cornifer query "rate limiting" --repo repos/fastapi --embed-provider voyage
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

## Local companion (website + MCP)

`cornifer-serve` is a localhost-only companion. It accepts public GitHub HTTPS
URLs, clones a detached commit without running repository code or hooks, and
keeps provider keys in the Go process rather than browser JavaScript.

```sh
export CORNIFER_DATABASE_URL='postgres://…/cornifer_hosted?sslmode=disable'
export CORNIFER_EMBEDDING_DIM=1024
export CORNIFER_COMPANION_DIR="$PWD/.cornifer-companion"

# Embeddings and chat are independently configured. Voyage is the current
# hosted embedding adapter; use a new database migrated at its output dimension.
export CORNIFER_EMBEDDING_PROVIDER=voyage
export CORNIFER_EMBEDDING_MODEL=voyage-code-3
export CORNIFER_EMBEDDING_API_KEY='…'

# Optional cited answer generation through an OpenAI-compatible endpoint.
export CORNIFER_CHAT_PROVIDER=openai_compatible
export CORNIFER_CHAT_BASE_URL='https://provider.example/v1/chat/completions'
export CORNIFER_CHAT_MODEL='chosen-small-model'
export CORNIFER_CHAT_API_KEY='…'

go run ./cmd/migrate up
go run ./cmd/cornifer-serve
# Website: http://127.0.0.1:7788  ·  MCP: http://127.0.0.1:7788/mcp
```

Without hosted embedding credentials, indexing enters an explicit
`awaiting_credentials` state; it never substitutes fake or local vectors.
`get_context` returns bounded evidence with commit SHA, normalized path/lines,
excerpt hash, retrieval provenance, and graph relationships. Its explicit
application `session_id` is persisted, bounded, and repo+commit scoped; a
moved ref receives a separate snapshot rather than overwriting it. Python is the only structural-graph language
today. The companion also chunks common docs and source-text extensions for
lexical retrieval, but those files have no symbols or graph edges and must not
be presented as complete structural coverage.

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
- `cmd/cornifer-serve` — localhost companion website, API, and Streamable HTTP MCP
- `cmd/cornifer-companion-mcp` — companion registry/context/session MCP over stdio
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
| `VOYAGE_API_KEY` | unset | Preferred hosted semantic provider: enables Voyage `voyage-code-3` (1024-d float vectors). Indexing sends `input_type=document`; query/eval/MCP search send `input_type=query`. Use `--embed-provider voyage` explicitly; without a key the safe default is fake. |
| `CORNIFER_SIDECAR_ENDPOINT` | unset | Required `/embed` endpoint when `--embed-provider sidecar` is used |
| `CORNIFER_SIDECAR_MODEL` | unset | Required immutable sidecar model identity (include revision and contract); persisted with the snapshot and checked by query/eval/MCP |
| `CORNIFER_SIDECAR_BATCH_SIZE` | `8` for sidecars | Bounds a sidecar request; `tools/local_embed_sidecar.py` uses the same value for its CPU model batch |
| `CORNIFER_SIDECAR_TIMEOUT_SECONDS` | `60` | Optional sidecar-only HTTP deadline; set it for a slow CPU model without changing hosted-provider timeouts |
| `--cache-dir` | `.cornifer-cache/` | Where the persisted BM25 index is written/read; structural data stays in Postgres |
| `--max-embed-tokens` | `8000` | Per-chunk embedding-input token ceiling; oversized chunks (plan.md's "one FastAPI chunk is ~50K tokens" case) are truncated for embedding, with a warning, not skipped |

### Hosted Voyage setup

Hosted Voyage is the preferred path for a new semantic benchmark; Cornifer does
not make API calls until an index/query/eval command is run. Supply only these
values through the calling environment (never a file committed to this repo):

```sh
export CORNIFER_DATABASE_URL='postgres://USER:PASSWORD@HOST:PORT/DATABASE?sslmode=require'
export CORNIFER_EMBEDDING_DIM=1024
export VOYAGE_API_KEY='user-provided Voyage API key'
make migrate
go run ./cmd/cornifer index --repo repos/fastapi --embed-provider voyage
go run ./cmd/cornifer eval --repo repos/fastapi --embed-provider voyage
```

Use an empty database (or a database whose `chunks.embedding` migration was
created at 1024 dimensions) for Voyage. Do not reuse the historical local-Jina
database, whose pgvector column is 768-dimensional. Cornifer persists
`voyage-code-3;input_type=document` for indexed chunks and sends
`input_type=query` for query/eval/MCP requests; a hosted report will record
both roles. `voyage-code-3` is intentionally pinned by the plan; selecting a
different paid model requires an explicit user choice and a clean reindex.

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
retrieval quality. Prefer user-authorized Voyage for a semantic comparison;
the local sidecar is an optional offline fallback. Preserve the resulting raw JSON under
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
  `VOYAGE_API_KEY` and select `--embed-provider voyage` to see hosted
  semantic recall. The local sidecar remains optional and was used only for
  the recorded offline experiment.
- **Graph boosting is heuristic.** Graph-adjacent/same-file chunks receive a
  small configurable post-RRF score (`--graph-boost-weight`, default
  `0.002`). The real source-package run records both rankings: boosting raised
  recall@5 (0.864 vs. 0.818) but reduced MRR (0.642 vs. 0.678). It is an
  observed tradeoff on this corpus, not a general performance claim.
- **IDE verification is deliberately narrow.** Pyright 1.1.412 confirmed the
  seven structural declaration labels in
  `eval/verification/fastapi-pyright-lsp-40e33e492.json`; semantic and
  identifier labels remain source-verified. This does not establish complete
  Python call resolution, which is still heuristic and tracked separately.
- **The committed result is source-package scoped.** All 22 labels fall under
  `fastapi/`, but the benchmark excludes FastAPI's tests and examples. A full
  1,306-file checkout local-Jina run was intentionally stopped before it
  produced chunks or results when hosted embeddings became the preferred path;
  it must not be compared directly with this result.
- **CPU sidecar truncation is material.** Jina's card documents support up to
  8,192 positions and training at 512; this resource-bounded run uses 128.
  One oversized `FastAPI.__init__` chunk was still truncated by Cornifer's
  8,000-token pre-embedding ceiling before the model cap.

## Remaining plan gaps

The remaining required work is a user-authorized full-checkout benchmark with
a selected hosted embedding account. The structural labels now have a recorded
Pyright LSP verification pass. Incremental updates, graph boosting/ablation,
persisted embedding provenance, a real source-package result, and all seven
MCP tools are implemented. Optional REST and reranking remain intentionally
out of scope.

## Target repo

The indexing pipeline is exercised against [fastapi/fastapi](https://github.com/fastapi/fastapi)
pinned to a specific commit SHA (recorded in [TARGET_REPO](TARGET_REPO)) so
eval labels stay valid as the upstream repo changes. `make fetch-repo` clones
it into the gitignored `repos/` directory.
