# Cornifer user guide

Cornifer now has a simple sequence: **repository URL → indexing → question →
answer or cited evidence**. Source and dependency details expand below the
result. Settings, MCP, saved snapshots, and explicit session memory are in the
secondary **Settings** menu.

This guide describes the unpublished `ao/cornifer-19/root` checkout including
the sequential UI, credential retry, generic lexical indexing, and shared
symbol-context corrections. Updated 4 October 2026. The product is not fully
provider-validated or production deployed; see the completion status below.

## 1. Use the current checkout

On this machine:

```sh
cd /home/hendrixx/.ao/data/worktrees/cornifer/cornifer-19
git branch --show-current
git log -1 --oneline
```

The branch should be `ao/cornifer-19/root`. These worker commits have not been
pushed, merged into main, or deployed. An older checkout or a fresh GitHub clone
may have a different website. Go embeds and serves the UI: no npm installation
or separate frontend server is needed.

Requirements: Go 1.26 or newer, Git, and a C compiler for tree-sitter
(`build-essential` on Ubuntu or Xcode Command Line Tools on macOS). The supplied
database uses Docker with Compose and Make. You can instead use an existing
Postgres with pgvector and the Cornifer migrations.

## 2. Database and startup

The user's `cornifer-postgres` container is on host port **5433**, migrated
through version **12**, with **1024**-dimensional embedding storage. One website
instance runs at **http://127.0.0.1:7788**. Stop that instance before starting
another on the same port.

For startup/restart from the checkout:

```sh
export CORNIFER_DATABASE_URL='postgres://cornifer:cornifer@127.0.0.1:5433/cornifer?sslmode=disable'
export CORNIFER_EMBEDDING_DIM=1024
export CORNIFER_COMPANION_DIR="$PWD/.cornifer-companion"
export CORNIFER_COMPANION_ADDR='127.0.0.1:7788'

go run ./cmd/migrate status
go run ./cmd/migrate up
go run ./cmd/cornifer-serve
```

Keep the terminal open. From a second terminal:

```sh
curl --fail http://127.0.0.1:7788/api/health
docker ps --filter name=cornifer-postgres
```

`embedding_configured` and `chat_configured` are configuration-presence flags,
not provider key validation. Keep the database URL and companion directory
stable: snapshots rely on both Postgres records and recorded local source/cache
paths. A new database has no indexed snapshots. The user's existing Cornifer
submission is currently waiting for embedding credentials; no fixture is added
to that database by verification.

### Optional fresh Compose database

If the desired local database does not already exist/running, set the variables
above, then use:

```sh
make up
go run ./cmd/migrate up
go run ./cmd/cornifer-serve
```

`make up` starts `pgvector/pgvector:pg16` and waits for database readiness.
Compose uses the fixed container name `cornifer-postgres` and port 5433, so it
cannot create a second database container on those same resources. Volumes are
Compose-project scoped: a different project/checkout can select another volume.
The supplied `cornifer:cornifer` credentials are local development defaults.

Migration 7 sets `chunks.embedding` to `vector(N)`, reading
`CORNIFER_EMBEDDING_DIM` (default 1024). Later migration runs do not resize it.
Keep the configured model output dimension and existing column equal. Inspect
without changing it:

```sh
docker exec -i cornifer-postgres psql -U cornifer -d cornifer -c \
  "SELECT format_type(atttypid, atttypmod) FROM pg_attribute WHERE attrelid='chunks'::regclass AND attname='embedding';"
```

Do not roll back migrations or delete a wanted volume to fix a dimension error.
Use a separate correctly migrated database when necessary.

## 3. Configure hosted services separately

Starting the website does not require or call a model provider. Ready indexes
can retrieve BM25 evidence without credentials. New companion URL ingestion
still requires hosted embedding configuration, including text-only repositories;
it never substitutes fake vectors or local inference.

### Voyage embeddings

Configure before URL submission when you intend to index with this hosted
service. Python indexing and configured hybrid queries can incur provider costs.

```sh
export CORNIFER_EMBEDDING_PROVIDER=voyage
export CORNIFER_EMBEDDING_MODEL=voyage-code-3
export CORNIFER_EMBEDDING_DIM=1024
```

Read the key without entering its value in shell history. Bash:

```bash
read -r -s -p 'Voyage API key: ' CORNIFER_EMBEDDING_API_KEY
printf '\n'
export CORNIFER_EMBEDDING_API_KEY
```

Zsh (this machine's shell):

```zsh
read -r -s 'CORNIFER_EMBEDDING_API_KEY?Voyage API key: '
printf '\n'
export CORNIFER_EMBEDDING_API_KEY
```

`VOYAGE_API_KEY` is the fallback when `CORNIFER_EMBEDDING_API_KEY` is empty.
Voyage is the companion's supported embedding provider. Model default is
`voyage-code-3`; endpoint default is
`https://api.voyageai.com/v1/embeddings`. Optional
`CORNIFER_EMBEDDING_BASE_URL` overrides the full endpoint. The engine CLI has
other provider options, but they do not enable local inference in the website.

Stop/restart the service in this configured terminal and reload the page.
Changing another terminal's environment cannot update the running process.
Keep keys out of browser inputs, files you commit, screenshots, command
arguments, and printed environment dumps. Hidden input avoids literal keys in
history; keys still live in the process environment.

### Optional hosted chat

```sh
export CORNIFER_CHAT_PROVIDER=openai_compatible
export CORNIFER_CHAT_BASE_URL='https://provider.example/v1/chat/completions'
export CORNIFER_CHAT_MODEL='replace-with-your-provider-model'
```

The endpoint/model above are placeholders. Read/export `CORNIFER_CHAT_API_KEY`
using the same hidden-input pattern. Restart/reload. `openai` is also accepted.
The URL must be the full chat completions endpoint; the adapter does not append
paths. It must support OpenAI-compatible messages and JSON object responses.

Without chat, the result is clearly labelled **Cited source evidence**. With
chat, a valid response is labelled **Grounded answer**. Source identifiers are
validated, but citation validity does not prove the truth of every generated
claim. No-evidence queries return an insufficient-evidence state without
calling the chat provider. Chat failures display an error; there is no fabricated
answer or automatic provider fallback.

To start without hosted configuration, stop the service, then:

```sh
unset CORNIFER_EMBEDDING_PROVIDER CORNIFER_EMBEDDING_MODEL CORNIFER_EMBEDDING_API_KEY
unset CORNIFER_EMBEDDING_BASE_URL VOYAGE_API_KEY
unset CORNIFER_CHAT_PROVIDER CORNIFER_CHAT_BASE_URL CORNIFER_CHAT_MODEL CORNIFER_CHAT_API_KEY
go run ./cmd/cornifer-serve
```

## 4. Repository URL → indexing

The entry screen has a Cornifer wordmark and one **Public GitHub repository
URL** input. Enter `https://github.com/owner/repository`, then press Enter or
the submit arrow. Use a public repository root URL; `.git` is accepted.
HTTP, credentials, query strings, fragments, and file/tree URLs are rejected.
Private/authenticated ingestion is not supported.

For a specific branch, tag, or commit, first set **Git ref for the next intake**
in Settings. Blank resolves remote HEAD. The runner shallow-clones and fetches
the ref, resolves a SHA, then checks out that commit detached. Git hooks and
recursive submodule checkout are disabled; repository application/build code
is not executed.

Loading is a subtle flat dot animation plus real job phase text. There is no
percentage, ETA, or artificial delay.

| Job phase/state | What is happening |
| --- | --- |
| `queued` | Waiting for this process's worker. |
| `cloning` | Downloading the public repository. |
| `resolving` | Resolving the requested ref to a pinned commit. |
| `indexing` | Preparing the index. |
| `parse` | Indexing files and parsing Python structural source. |
| `graph` | Resolving Python relationships; omitted when no Python inputs exist. |
| `embed` | Creating Python source embeddings; omitted when no Python chunks need vectors. |
| `store` | Saving chunks and lexical retrieval index. |
| `ready` | Reveals the question screen with repository and short SHA. |
| `awaiting_credentials` | Embedding configuration was absent after clone/ref resolution. |
| `failed` / `cancelled` | Job stopped; an actionable message and Retry indexing appear. |
| `stale` | Contract-supported unavailable state; remote branch movement does not automatically set it. |

**Cancel indexing** appears only for a cancellable job. Cancellation is a stop
request, not a guarantee of rolling back all partial writes. Counters reflect
real boundaries and may remain unchanged during a long stage. Changing repo
leaves background work running; cancel it first if you want it stopped.

For `awaiting_credentials`: configure Voyage in the server environment,
restart/reload, open that snapshot in Settings, then **Retry indexing**. This
explicitly resubmits the same original URL/ref, creates a new job on the same
waiting snapshot, and retains its resolved SHA. Credentials alone never resume
jobs automatically. With credentials still absent it returns to waiting.

Active same-URL/ref submissions reuse the job. Failed/cancelled retries create
another request, not a saved-step continuation. Repeating a completed identical
URL/ref/SHA still has a known uniqueness/finalization limitation. Interrupted
active jobs are not recovered/reconciled at server startup. Before a planned
restart, cancel and wait for a terminal state. Refresh/reopen checks status,
not worker liveness. A polling error stops page polling; reopen the snapshot
in Settings to check again. Provider HTTP retries for transient network/429/5xx
errors are bounded and separate from these job lifecycle limits.

Ready saved snapshots are available through **Use an indexed snapshot**, or
**Settings → Choose a snapshot → Open snapshot**. This lets you return to a
pinned version without starting another index. Other saved states can be opened
there to inspect status/retry. There is no snapshot comparison or reindex button.

## 5. Question → answer and progressively expanded details

The ready screen has a centered question input and a small repo/SHA indicator.
Ask natural-language code questions, then press Enter or the arrow. Shift+Enter
adds a newline. **Change repo** returns to URL intake. Follow-up questions reuse
the current application session ID for the same snapshot.

Results begin with the question and either a grounded hosted answer or an
explicit retrieval-only explanation. Without generated chat, source excerpts
are expanded so you can read the actual evidence immediately. No matches is
an honest empty state; try terms present in the code. When embeddings are
absent, existing indexes use BM25. Configured hybrid queries may contact Voyage
for a query embedding, even in text-only repositories.

Below the answer:

- Expand a relevant **class/function** to see its exact recorded signature,
  source location, and counts of callers, callees, and other dependencies
  within this context. A merged source chunk can represent multiple functions;
  shared metadata includes its recorded owner and fully enclosed exact
  definitions. It does not invent a unique answer symbol from query words.
- Expand **Dependencies and relationships** for named incoming/outgoing links,
  relation kinds and confidence. These are resolved static Python edges;
  confidence is not runtime certainty. There are at most 32 immediate
  relationships in the shared pack, so it is not a complete dependency inventory.
- The graph inside that section includes only context symbols and their
  relationships. Direct evidence symbols use the main accent. Select a node
  or relation symbol to open source. Drag to pan, scroll/zoom controls to zoom,
  **Reset view** to reset, or use arrow keys while the graph is focused to pan.
- Expand **Cited source excerpts** for `[eN]`, normalized path/lines, actual
  snippet, retrieval provenance and excerpt SHA-256. Click a citation, symbol
  source link, or answer citation to open numbered source from the pinned
  checkout. The full snapshot SHA appears below every result. Excerpt hashes
  differ from the commit SHA; truncation is labelled.
- **Ask a follow-up** returns focus to a cleared question input.

This is the **same ContextPack returned by companion MCP `get_context`**. Shared
`symbols` include IDs, kind/name, qualified name, signature, path, lines and an
`evidence` marker. Relationships include source/target symbol IDs, direction,
kind and confidence. Both website and MCP use the same retrieval, bounds,
provenance and session boundary. The UI does not run a second retrieval path or
build a fabricated graph. Context cache versioning prevents older packs without
symbol metadata from being reused by this UI.

Defaults: eight evidence hits, 18,000 bytes of excerpt budget, ten-minute cached
packs, up to 256 cache entries. MCP can request other evidence/context limits;
evidence count is capped at 20. Omission/truncation metadata is preserved. The
source API allows a range of at most 501 lines. Large/bounded context does not
establish complete whole-repository coverage.

### Language coverage and reindex

Python has AST chunks and structural symbols/relationships. Common supported
Markdown/text and Go, JS/TS, Java, Rust, C/C++, Ruby, PHP, shell, JSON, YAML,
TOML and other walker-supported source formats have **generic lexical chunks**,
with no vectors or structural graph. The result explains unsupported structural
coverage in-place. Generic chunks are limited to 4,096 bytes and 128 lines,
targeting 512 estimated tokens under default settings. Oversized single lines
split on UTF-8 boundaries with the same original line number. Empty files have
no chunks; generic text must be valid UTF-8. Excluded directories/extensions and
unresolved/external dependencies are not promised coverage.

Full and incremental indexing include generic text. Added/changed/deleted files
update stored chunks and BM25; unchanged content retains chunk IDs/cache. Engine
reindex backfills nonempty legacy generic file records with missing chunks.
Restarting the service alone does not rebuild an index. Use the same original
checkout/cache/database/provider/dimension for engine reindex; Python changes
may call a hosted provider. Do not use fake vectors to repair a hosted index.
Pinned companion checkouts should not be edited in place: same-commit working
tree reindex can leave a question's context cached for ten minutes and cannot
rewrite already saved memory events. New code should get a new pinned snapshot.

## 6. Settings: explicit session memory

The first successful question creates an application session automatically.
Settings shows its stable ID, snapshot scope/expiry, recent searches/notes, and
**Remember a decision → Save note**. There is no entry-screen memory panel.

- IDs and events live in Postgres; the browser remembers its ID per repository
  in origin/profile-specific localStorage. Same port/profile can reconnect
  after reload. The UI has no manual session-ID import; MCP/API can supply one.
- Scope is snapshot ID **and** resolved SHA. Another snapshot, a stale session,
  or expiry rejects context reuse. A moved ref creates another snapshot and
  keeps old notes attached to the original one.
- Default expiry is seven days from creation, not extended by normal requests.
  Recent events are bounded to 64; the menu displays the most recent 12.
  Notes are capped to 2,000 bytes by the service and 2,000 characters in the UI.
- Notes are explicitly persisted, but are **not automatically recalled into
  answers**. There is no rolling summary generator. MCP clients may deliberately
  read and use them.
- **Clear session** immediately deletes that session and its events, with no
  undo. The next question creates another. It does not remove indexes/jobs or
  the separate context cache.
- Expiry is enforced for reuse, but cleanup is not scheduled. Do not assume
  expired records are deleted or global session storage is capped at 100.

## 7. Settings: MCP connection

Streamable HTTP endpoint:

```text
http://127.0.0.1:7788/mcp
```

Settings has **Copy endpoint** and **Copy config**. Config copies a representative
`mcpServers` entry with a URL, not credentials:

```json
{"mcpServers":{"cornifer-companion":{"url":"http://127.0.0.1:7788/mcp"}}}
```

Client formats differ; some need an explicit HTTP transport setting. On clipboard
failure the menu displays text to copy manually. The server owns provider keys.
There is no client pairing/connection-test feature. A client's own container or
remote computer's `127.0.0.1` is not this host; authenticated remote operation
is not implemented as a production flow.

Server identity: `cornifer-companion`, version `0.2.0`. Use companion repository
IDs, not numeric engine IDs. Exact tools:

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_repositories` | `{}` | Snapshot records/status. |
| `get_index_status` | `{"repository_id":"<snapshot-id>"}` | Repository status; not the separate Job object. |
| `ingest_repository` | `{"url":"https://github.com/owner/repo","ref":"<ref>"}` (`ref` optional) | Repository, job, reused; same retry/lifecycle contract as UI. |
| `get_context` | `{"repository_id":"<snapshot-id>","question":"Where is routing handled?","session_id":"<id>","evidence_limit":8,"context_bytes":18000}` (last three optional) | Shared context plus session; retain `session.id` for follow-ups. |
| `remember_context` | `{"session_id":"<id>","note":"Check routing before changes."}` | Persisted explicit note and updated session. |
| `get_session_context` | `{"session_id":"<id>","limit":16}` (`limit` optional, default/max 64) | Session and recent events. |
| `clear_context` | `{"session_id":"<id>"}` | Deletes session/events, returns `cleared: true`. |

The application ID is not the MCP transport session ID. Reconnection does not
select your memory. `get_context` creates/records local events despite its MCP
read-only annotation. Companion MCP has no chat-generation/cancel/source-read
or graph-navigation tool; the context pack already contains the bounded
structural graph metadata. Website source/cancel operations use the HTTP API.

For stdio-only clients, configure the same environment/working directory and
let the client launch:

```sh
go run ./cmd/cornifer-companion-mcp
```

This serves the same companion tools, without a website; stdout is protocol,
stderr is logs. Avoid duplicate indexing from separate processes because jobs
and cancellation are process-local. `go run ./cmd/cornifer-mcp` is the older
engine MCP with different tools (`search_code`, `find_definition`, etc.), not
the companion registry/session-memory interface.

## 8. Data and cost

| Destination | Data |
| --- | --- |
| Local Postgres | Registry/jobs, files/symbols/edges, source chunks, Python vectors, context cache and session events. |
| Companion directory | Local shallow checkouts, BM25 indexes and embedding cache. |
| Browser localStorage | Last application session ID per repository; no provider keys. |
| GitHub | Public URL/ref fetches before readiness or credential waiting. |
| Voyage when configured | Python indexing chunks and uncached query questions plus model/request metadata. Generic indexing chunks remain local/lexical. |
| Hosted chat when configured | Shared bounded context: question, repository URL/SHA, cited snippets, paths/lines/hashes, symbol signatures and immediate relationships/confidence. Saved notes are not automatically included. |

Charges depend on your provider/account/model. No paid calls or local inference
were used for worker verification. Local disk grows with sources, embeddings,
snapshots and memory. There is no repository-delete/storage-quota UI, automatic
clone eviction, or scheduled expiry purge. Back up both Postgres and the
companion directory. Removing source/cache files alone breaks usable snapshots
while registry status may still say ready.

## 9. Stop, restart and troubleshoot

Press Ctrl+C in a service terminal. For the current worker-started service,
identify its owner on Linux before stopping it:

```sh
ss -ltnp 'sport = :7788'
```

Use `kill -INT <pid>` only for the identified intended Cornifer process.
Keep Postgres running for website restarts. `make down` stops the owning Compose
project without deleting its volume. For the named existing container,
`docker stop cornifer-postgres` / `docker start cornifer-postgres` preserve data.
Do not use `down -v` for data you want to keep.

UI assets are embedded: stop and rebuild/restart Go after edits, then reload.
The simplified UI uses asset version `sequential-3`. Refreshing an old binary
cannot install new assets.

| Problem | Action |
| --- | --- |
| Port already used | Open the existing app or stop the verified old process; alternatively deliberately set a different loopback `CORNIFER_COMPANION_ADDR`. |
| Database refused/missing tables | Check container/readiness/host5433, same DSN, and `migrate status`/`migrate up`. |
| Dimension mismatch | Inspect column and match settings; rerunning migrations does not resize it. |
| URL rejected | Use public HTTPS GitHub owner/repo root without query/auth/tree path. |
| Waiting for credentials | Configure, restart/reload, reopen snapshot in Settings, Retry indexing. |
| Stuck active job after crash | No automatic startup recovery; reopening shows persisted state, not worker liveness. |
| Hosted answer error | Check all chat variables and complete compatible endpoint/model; restart/reload. No fabricated fallback answer. |
| No evidence/graph | Query actual terms; check ready source/cache and language coverage. Non-Python evidence has no structural graph. |
| Source unavailable | Restore recorded checkout/cache alongside database; changing the base directory does not migrate old paths. |
| Memory absent | Check same browser origin/profile, snapshot ID/SHA, expiry or prior Clear. Use MCP for a known ID. |
| Clipboard denied | Copy the displayed endpoint/config text manually. |
| Old page | Restart correct checkout/binary on intended port, then reload/cache-bust. |

All actions are keyboard reachable, with visible focus. Enter submits URL/question;
Shift+Enter adds a question newline; Escape closes Settings. Small-screen content
stacks in the same sequence. System/browser Reduce motion disables loading and
transition animation. Monkeytype Gruvbox Dark tokens, self-hosted Roboto Mono,
and flat fills are preserved throughout; no gradients are used.

## Completion and verification status

- **Built:** sequential UI; honest URL/job/retry/cancel states; answer-first cited
  results with progressive source/symbol/dependency details; focused shared
  context graph; discreet saved snapshots/MCP/session controls.
- **Backend verified offline:** generic docs/source retrieval and incremental
  changes/cache/backfill; real Python edges; shared HTTP/MCP pack parity including
  signatures, relation IDs/confidence; snapshot/session isolation; actual phase
  reporting; no-evidence chat makes no provider request. Tests use isolated
  Postgres and clearly labelled fake embeddings only within test fixtures.
- **AO Browser verified on isolated data:** invalid URL; real public Cornifer
  clone resolving to the credentials-required state; public clone cancellation;
  nonexistent-repository failure and retry controls; explicit credentials retry
  creating a new job while retaining the pinned SHA; labelled offline indexed
  fixture questions; exact method signatures; three indexed call relationships;
  graph zoom, keyboard pan and reset; node/citation navigation to numbered
  pinned source; TypeScript lexical evidence without structural metadata;
  no-match response; follow-up session reuse, explicit note persistence, session
  clear and MCP configuration copy. Browser and user database verification
  are separate; fixture rows are never added to the user's database.
- **Visual verification limits:** AO screenshot capture timed out with the
  Browser panel hidden. Responsive CSS and reduced-motion rules are inspected,
  but painted mobile viewport and pointer-drag checks remain unverified. Native
  AO navigation needs a fresh snapshot after the preview finishes rebinding;
  stale selectors are not evidence of an application error.
- **User corpus provider-blocked:** the current Cornifer submission awaits Voyage
  credentials. No full hosted URL→embedding→ready run, live hybrid provider
  query, or hosted chat E2E has been executed. Controlled adapter responses and
  offline fixture evidence are distinct from hosted validation.
- **Remaining implementation limits:** interrupted-job recovery, same-SHA repeated
  intake finalization, automatic memory recall/summary, scheduled cleanup,
  whole-repo graph coverage, authenticated/private repositories, additional
  structural languages and production deployment are not delivered.
- **Scope:** local unpublished worker commits; user data preserved; no pushes,
  PRs, deployments, paid calls, or local inference.

Primary implementation references: [website](../cmd/cornifer-serve/web/app.js),
[shared context](../internal/companion/context.go),
[jobs/sessions](../internal/companion/service.go),
[MCP](../internal/companion/mcp.go), [configuration](../internal/companion/runner.go),
[chat](../internal/companion/chat.go), and
[offline context/transport parity tests](../internal/companion/text_context_integration_test.go).
