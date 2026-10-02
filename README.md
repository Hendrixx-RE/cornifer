# Cornifer

Repository intelligence engine: parses a real codebase into a **structural
index** (AST → symbols → import/call graph) and a **semantic index**
(AST-aware chunks → embeddings + BM25), answers queries by combining exact
structural lookups with hybrid retrieval, and exposes it all as an **MCP
server**.

See [plan.md](plan.md) for the full architecture, phased build plan, and
design decisions.

## Status

**Week 1 and Week 2 exit criteria are met end to end.** `cornifer index`
runs the full pipeline (walk → parse → symbols → resolve → chunk → embed →
store → BM25) against a real repo; `cornifer query`, `find-definition`,
`callers`, `callees`, `blast-radius`, and `cycles` answer structural and
hybrid-semantic questions against the result. **Week 3 evaluation is now
implemented:** `cornifer eval` validates pinned YAML labels, runs hybrid,
BM25-only, vector-only, and ripgrep, calculates precision@5 / recall@5 / MRR
overall and by query type, and writes the raw ranks and metadata to JSON.

Indexing the pinned FastAPI checkout (1306 Python files, including its
`tests/` and `docs_src/` trees) took **~12s wall-clock** on this machine —
well inside the "under a few minutes" target — producing 9,846 symbols,
7,535 edges, and 5,499 embedded chunks. See the PR description for the full
numbers and spot-checked command output.

### Evaluation status

The committed [evaluation set](eval/queries.yaml) contains **22 FastAPI
queries** at pinned commit `40e33e492dbf4af6172997f4e3238a32e56cbe26`:
7 structural, 7 semantic, and 8 identifier tasks. Every current label is
marked **source-verified** and points to a source span that `cornifer eval`
checks against the pinned checkout. There are **zero IDE-verified labels** in
this first set: no Pylance/Pyright "find references" result is asserted or
inferred.

There is intentionally no target-repo metric table committed yet. This
worktree has the pinned FastAPI source but no reachable Docker daemon/Postgres,
so a real FastAPI index and evaluation could not be run locally. Inventing
metrics (especially from the deterministic fake embedder) would make the
comparison misleading. The evaluator has an end-to-end fixture test that runs
when Postgres is available; the target command below produces the durable raw
result once its prerequisites are present.

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

# After index has completed against the same cache/database:
go run ./cmd/cornifer eval --repo repos/fastapi --embed-provider voyage --indexed-embed-provider voyage
# writes eval/results/fastapi-40e33e492db-voyage.json by default
```

Every read command (`query`, `find-definition`, `callers`, `callees`,
`blast-radius`, `cycles`) needs `cornifer index` to have run first for that
repo — see "Known limitations" below for why (a local cache, not just
Postgres, currently backs these reads).

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
| `--cache-dir` | `.cornifer-cache/` | Where the local manifest + BM25 index (see "Known limitations") are written/read |
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

Current index manifests do not persist the corpus embedding provider. Pass
`--indexed-embed-provider` when evaluating a real index; it must match the
query provider. If omitted, the report defaults it to the query provider but
marks that value as an unverified assumption rather than observed provenance,
and will not mark vector/hybrid rows semantically meaningful.

## Known limitations

- **No bulk Store reads yet.** `internal/store`'s `Store` interface (this
  wave) has point lookups (`FindSymbolByQualifiedName`,
  `FindSymbolsByName`, `GetCallers`/`GetCallees` by one ID) but no
  "every symbol/edge/chunk in this repo" query. `cornifer index` works
  around this by additionally writing a JSON manifest (files, symbols,
  edges, and chunk metadata) under `--cache-dir`, which every read command
  loads instead of querying Postgres for graph/structural data (vector
  search still goes through `Store.VectorSearch`). This means read
  commands only work against a repo that has been indexed by the same
  machine/cache-dir since the last `index` run. See
  `internal/indexer/doc.go` for the exact plan to drop this once bulk Store
  methods land.
- **`reindex` is a full rebuild, not an incremental diff.** It reuses the
  existing `Repo` row for an unchanged commit but re-walks, re-parses, and
  re-resolves everything (skipping the run entirely unless `--force` is
  given, or the commit changed); the `content_hash`-diff incremental path
  plan.md describes for Week 3 is not yet implemented.
- **Call/import resolution is heuristic**, not type inference — see
  `internal/resolve/doc.go`. `cornifer index` logs per-kind resolution
  ratios every run.
- **The fake embedder is not semantically meaningful.** Without
  `VOYAGE_API_KEY`, vector-search results are deterministic but
  hash-derived, not based on code meaning; BM25 still works normally. Set
  `VOYAGE_API_KEY` (or point `--embed-provider sidecar` at a local model)
  to see real semantic recall.
- **Graph-adjacency boosting is not implemented.** The retrieval API exposes
  a post-fusion boost seam, but production wiring supplies the no-op default.
  Therefore an eval report records graph boost as unavailable and does not
  fabricate a redundant "hybrid without graph boost" row. Add a real graph
  boost before making that comparison.
- **The FastAPI ground truth is source-verified, not IDE-verified.** The
  committed labels are direct pinned-source targets. A future structural
  evaluation pass should independently record Pyright/Pylance reference
  checks, with tooling/version and the exact source commit, rather than
  relabelling these existing entries.
- **A target evaluation needs a live indexed database and real embeddings.**
  `cornifer eval` loads BM25 from the local cache and vectors from Postgres;
  it cannot generate honest FastAPI metrics until `make up`, `make migrate`,
  and `cornifer index` have succeeded for the same pinned checkout.

## Remaining plan gaps

The Week 3 evaluation milestone is available, but the following plan items
remain incomplete: graph boost (and its ablation), IDE-verified structural
ground truth, a checked-in real-embedding FastAPI result, genuinely
incremental reindexing, the optional REST layer, and the demo recording.

## Target repo

The indexing pipeline is exercised against [fastapi/fastapi](https://github.com/fastapi/fastapi)
pinned to a specific commit SHA (recorded in [TARGET_REPO](TARGET_REPO)) so
eval labels stay valid as the upstream repo changes. `make fetch-repo` clones
it into the gitignored `repos/` directory.
