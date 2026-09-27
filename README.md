# Cornifer

Repository intelligence engine: parses a real codebase into a **structural
index** (AST → symbols → import/call graph) and a **semantic index**
(AST-aware chunks → embeddings + BM25), answers queries by combining exact
structural lookups with hybrid retrieval, and exposes it all as an **MCP
server**.

See [plan.md](plan.md) for the full architecture, phased build plan, and
design decisions.

## Status

**Phase 0 (setup) is complete.** The module skeleton, domain model
(`internal/model`), interface stubs (`Embedder`, `Store`, `SparseIndex`,
`Parser`, `Chunker` — all returning `model.ErrNotImplemented`), Postgres
schema and migrations, CLI/MCP command scaffolding, and CI are in place.
Parsing, resolution, chunking, embeddings, retrieval, MCP handlers, and eval
logic are not yet implemented — see plan.md's "Phases" for what's next.

## Quickstart

```sh
make up           # start Postgres (pgvector) via docker compose
make migrate       # apply schema migrations
make fetch-repo     # clone the pinned target repo (see TARGET_REPO) into repos/
make test           # go test ./... (no database required)
```

`make index` / `make query` wire up the CLI (`cmd/cornifer`) but currently
return "not implemented" — later waves fill these in.

## Layout

See plan.md's "Proposed layout" for the full directory breakdown. Key
entry points:

- `cmd/cornifer` — CLI (`index`, `reindex`, `query`, `eval`)
- `cmd/cornifer-mcp` — MCP server (stdio transport)
- `cmd/migrate` — applies `migrations/` with goose
- `internal/model` — shared domain types (`Repo`, `File`, `Symbol`, `Edge`,
  `Chunk`, `UnresolvedRef`) that every other package imports
- `migrations/` — Postgres schema (see `migrations/doc.go`)

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `CORNIFER_DATABASE_URL` | `postgres://cornifer:cornifer@localhost:5433/cornifer?sslmode=disable` | Postgres connection string used by `cmd/migrate` (and, later, the rest of the CLI) |
| `CORNIFER_EMBEDDING_DIM` | `1024` (`model.DefaultEmbeddingDim`) | Dimension of the `chunks.embedding` pgvector column, read at migration time |

## Target repo

The indexing pipeline is exercised against [fastapi/fastapi](https://github.com/fastapi/fastapi)
pinned to a specific commit SHA (recorded in [TARGET_REPO](TARGET_REPO)) so
eval labels stay valid as the upstream repo changes. `make fetch-repo` clones
it into the gitignored `repos/` directory.
