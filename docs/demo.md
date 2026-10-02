# Reproducible local demo

This is a command transcript rather than a recording. The preferred production
path uses user-authorized hosted Voyage embeddings. The committed historical
raw result was produced with an isolated user-owned PostgreSQL/pgvector database
and an optional local Jina sidecar. It intentionally uses a sparse checkout
containing only the pinned FastAPI `fastapi/` source package; see
[evaluation-fastapi.md](evaluation-fastapi.md) for that boundary and metrics.

```sh
make fetch-repo
# Provision Postgres+pgvector and apply migrations at 1024 dimensions first.
# Supply the chosen hosted account's key through the shell; do not commit it.
export VOYAGE_API_KEY='…'

# A sparse worktree preserves the upstream commit while limiting this demo to
# the source package that contains all 22 labels.
git -C repos/fastapi worktree add --detach .cornifer-local/fastapi-source 40e33e492dbf4af6172997f4e3238a32e56cbe26
git -C .cornifer-local/fastapi-source sparse-checkout init --no-cone
git -C .cornifer-local/fastapi-source sparse-checkout set fastapi

go run ./cmd/cornifer index --repo .cornifer-local/fastapi-source --embed-provider voyage --cache-dir .cornifer-local/cache
go run ./cmd/cornifer query "convert arbitrary models to JSON-safe data" --repo .cornifer-local/fastapi-source --embed-provider voyage --cache-dir .cornifer-local/cache
go run ./cmd/cornifer find-definition fastapi.routing.APIRoute.get_route_handler --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer callers fastapi.applications.FastAPI.add_api_route --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer blast-radius fastapi/routing.py --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer cycles --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer eval --repo .cornifer-local/fastapi-source --embed-provider voyage --cache-dir .cornifer-local/cache --output eval/results/fastapi-voyage.json
```

For an MCP demo, set `CORNIFER_REPO_ID` to the ID printed by `index` and
`CORNIFER_BM25_INDEX` to `.cornifer-local/cache/bm25-<repo-id>.json`, retain
`VOYAGE_API_KEY` in that process environment, then run `go run ./cmd/cornifer-mcp`. The actual stdio transport can be checked
through all seven tools with `python3 tools/mcp_stdio_smoke.py`; it requires
the same database, BM25 cache, and Voyage credential environment.

Do not treat a run using the fake embedder as a semantic-retrieval demo. The
result JSON marks it non-semantic. The local Jina path is optional and no
further local model run is required for a hosted evaluation.
