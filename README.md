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
hybrid-semantic questions against the result. `cornifer eval` (the Week 3
evaluation harness) is still a stub returning `model.ErrNotImplemented`.

Indexing the pinned FastAPI checkout (1306 Python files, including its
`tests/` and `docs_src/` trees) took **~12s wall-clock** on this machine —
well inside the "under a few minutes" target — producing 9,846 symbols,
7,535 edges, and 5,499 embedded chunks. See the PR description for the full
numbers and spot-checked command output.

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

## Target repo

The indexing pipeline is exercised against [fastapi/fastapi](https://github.com/fastapi/fastapi)
pinned to a specific commit SHA (recorded in [TARGET_REPO](TARGET_REPO)) so
eval labels stay valid as the upstream repo changes. `make fetch-repo` clones
it into the gitignored `repos/` directory.
