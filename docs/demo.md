# Reproducible local demo

This is a command transcript rather than a recording because the current
workspace has no Docker daemon or Postgres service. It is reproducible once
those prerequisites are available.

```sh
make up
make migrate
make fetch-repo
go run ./cmd/cornifer index --repo repos/fastapi --embed-provider voyage
go run ./cmd/cornifer query "convert arbitrary models to JSON-safe data" --repo repos/fastapi
go run ./cmd/cornifer find-definition fastapi.routing.APIRoute.get_route_handler --repo repos/fastapi
go run ./cmd/cornifer callers fastapi.applications.FastAPI.add_api_route --repo repos/fastapi
go run ./cmd/cornifer blast-radius fastapi/routing.py --repo repos/fastapi
go run ./cmd/cornifer cycles --repo repos/fastapi
go run ./cmd/cornifer eval --repo repos/fastapi --output eval/results/fastapi-voyage.json
```

For an MCP demo, set `CORNIFER_REPO_ID` to the ID printed by `index` and
`CORNIFER_BM25_INDEX` to `.cornifer-cache/bm25-<repo-id>.json`, then run
`go run ./cmd/cornifer-mcp`. The seven protocol tools are exercised by the
in-memory transport test in `internal/mcp`.

Do not treat a run using the fake embedder as a semantic-retrieval demo. The
result JSON marks it non-semantic; use a real provider only after approving any
provider cost.
