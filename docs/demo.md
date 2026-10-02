# Reproducible local demo

This is a command transcript rather than a recording. The committed raw result
was produced with an isolated user-owned PostgreSQL/pgvector database and the
local Jina sidecar. It intentionally uses a sparse checkout containing only
the pinned FastAPI `fastapi/` source package; see
[evaluation-fastapi.md](evaluation-fastapi.md) for that boundary and metrics.

```sh
make fetch-repo
# Provision a local Postgres+pgvector database and apply migrations first.
# Start tools/local_embed_sidecar.py with the pinned Jina model, then set:
export CORNIFER_SIDECAR_ENDPOINT=http://127.0.0.1:18080/embed
export CORNIFER_SIDECAR_MODEL='jinaai/jina-embeddings-v2-base-code@516f4baf13dec4ddddda8631e019b5737c8bc250;pooling=mean;contract=symmetric;max_length=128'
export CORNIFER_SIDECAR_BATCH_SIZE=8 CORNIFER_SIDECAR_TIMEOUT_SECONDS=600

# A sparse worktree preserves the upstream commit while limiting this demo to
# the source package that contains all 22 labels.
git -C repos/fastapi worktree add --detach .cornifer-local/fastapi-source 40e33e492dbf4af6172997f4e3238a32e56cbe26
git -C .cornifer-local/fastapi-source sparse-checkout init --no-cone
git -C .cornifer-local/fastapi-source sparse-checkout set fastapi

go run ./cmd/cornifer index --repo .cornifer-local/fastapi-source --embed-provider sidecar --cache-dir .cornifer-local/cache
go run ./cmd/cornifer query "convert arbitrary models to JSON-safe data" --repo .cornifer-local/fastapi-source --embed-provider sidecar --cache-dir .cornifer-local/cache
go run ./cmd/cornifer find-definition fastapi.routing.APIRoute.get_route_handler --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer callers fastapi.applications.FastAPI.add_api_route --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer blast-radius fastapi/routing.py --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer cycles --repo .cornifer-local/fastapi-source --cache-dir .cornifer-local/cache
go run ./cmd/cornifer eval --repo .cornifer-local/fastapi-source --embed-provider sidecar --cache-dir .cornifer-local/cache --output eval/results/fastapi-40e33e492db-jina-code-source-128.json
```

For an MCP demo, set `CORNIFER_REPO_ID` to the ID printed by `index` and
`CORNIFER_BM25_INDEX` to `.cornifer-local/cache/bm25-<repo-id>.json`, then
run `go run ./cmd/cornifer-mcp`. The actual stdio transport can be checked
through all seven tools with `python3 tools/mcp_stdio_smoke.py`; it requires
the same database, BM25, and sidecar environment.

Do not treat a run using the fake embedder as a semantic-retrieval demo. The
result JSON marks it non-semantic. This local Jina path has no paid API use.
