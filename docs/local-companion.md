# Run the Cornifer website locally

Run the commands below from the repository root containing `go.mod`. The
website is served by Go; it needs no npm install or separate frontend server.
Use the checkout containing the redesigned UI. The worker's local branch is
`ao/cornifer-19/root`; these changes have not been pushed to GitHub.

## Requirements

- Go 1.26 or newer, Git, and a C compiler for the tree-sitter dependency
  (`build-essential` on Ubuntu, or Xcode Command Line Tools on macOS).
- Docker with Docker Compose, running before you start the database.
- Make for the convenience commands below.

## Start the database and website

For a new local database, these settings prepare 1024-dimensional storage for
the supported Voyage embedding adapter. If reusing an existing indexed
database, keep its original embedding dimension instead; rerunning migrations
does not resize existing vectors.

```sh
export CORNIFER_DATABASE_URL='postgres://cornifer:cornifer@127.0.0.1:5433/cornifer?sslmode=disable'
export CORNIFER_EMBEDDING_DIM=1024
export CORNIFER_COMPANION_DIR="$PWD/.cornifer-companion"
export CORNIFER_COMPANION_ADDR='127.0.0.1:7788'

make up
go run ./cmd/migrate up
go run ./cmd/cornifer-serve
```

Open **http://127.0.0.1:7788**. The MCP endpoint is
**http://127.0.0.1:7788/mcp**. Keep the terminal running. To choose another
website port, change `CORNIFER_COMPANION_ADDR` before starting the Go command.
The UI's MCP copy controls use the active website port automatically.

You can check the server from a second terminal:

```sh
curl http://127.0.0.1:7788/api/health
```

These startup commands do not configure or call a model provider. A new
database starts with an empty repository list; worker verification fixtures
use a separate isolated database and are never installed in the user database. Existing
ready companion snapshots in the same database can be explored and searched
with lexical retrieval when hosted credentials are absent. Their checkouts
and local index cache must still be available at their recorded paths.

## Enable repository indexing when needed

New GitHub URL indexing requires hosted embedding credentials. Without them,
the UI displays `awaiting_credentials`. The companion does not run local
inference. Configure these variables in the server's terminal and restart it
only when you want to use that hosted service:

```sh
export CORNIFER_EMBEDDING_PROVIDER=voyage
export CORNIFER_EMBEDDING_MODEL=voyage-code-3
# Type the key at the silent prompt, then press Enter (bash or zsh):
read -r -s CORNIFER_EMBEDDING_API_KEY
export CORNIFER_EMBEDDING_API_KEY
# CORNIFER_EMBEDDING_DIM must match the database: 1024 for the new setup above.
go run ./cmd/cornifer-serve
```

For a snapshot already in `awaiting_credentials`, reload the page after the
server restart, open the snapshot from Settings, and choose **Retry indexing**.
You can also resubmit the same URL and original ref. This explicitly
creates a new job for the existing pinned snapshot; there is no automatic resume.

Adding a repository then calls the embedding provider and may incur its
usage charges. Keep keys in the server environment, never in browser code or
committed files. Add a public `https://github.com/owner/repository` URL in the
website, optionally set the ref in Settings, and wait for indexing to finish.
The simple sequence is URL → indexing → question → answer or cited evidence;
function/source/dependency details expand below results, while MCP and session
memory controls stay in Settings.
Python has structural graph coverage. Other recognized docs/source extensions
receive bounded generic text chunks for BM25 retrieval and cited source
inspection, without structural symbols, edges, or embedding vectors. The
companion URL intake still requires hosted embedding configuration even for a
text-only repository; it does not switch to fake or local inference.

Hosted chat is configured independently and is optional. Without it, questions
return cited source evidence instead of generated answers. For chat
configuration, see the [README companion setup](../README.md#local-companion-website--mcp).

## Restart, stop, and troubleshooting

- Press **Ctrl+C** in the server terminal to stop the website. Start it again
  with `go run ./cmd/cornifer-serve`; rebuild/restart after editing embedded UI
  assets. If the browser retains old assets, reload the page.
- Run `make down` to stop Postgres. The Compose volume keeps its data.
- For a database connection error, run `docker compose ps` and confirm Postgres
  is healthy and the URL uses host port **5433**.
- For missing tables, run `go run ./cmd/migrate up` with the same database URL
  as the server.
- For a port conflict, select another loopback port in
  `CORNIFER_COMPANION_ADDR`. The database's host port is configured separately
  in `docker-compose.yml`.
- Enable your system/browser **Reduce motion** preference to disable loading
  and transition animations. Graph pan/zoom changes are immediate.

For controls, MCP/session examples, retry limits, and verified completion status,
see the [Cornifer user guide](user-guide.md).
