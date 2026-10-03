# Cornifer user guide

This guide describes the local companion website and its companion MCP tools
as implemented in the unpublished `ao/cornifer-19/root` checkout, based on
commit `7ae1154` plus the focused credential-retry correction accompanying this guide. It was checked against the code and running local service on
4 October 2026. See [completion status](#completion-status-and-known-limits)
before treating it as a finished production product.

## 1. Use the checkout containing the new website

On this machine, the redesigned website is in:

```sh
cd /home/hendrixx/.ao/data/worktrees/cornifer/cornifer-19
git branch --show-current
git log -1 --oneline
```

The branch should be `ao/cornifer-19/root`. These worker commits have not been
pushed, merged into main, or deployed. A new clone from GitHub or an older
checkout may therefore show a different website. There is no npm setup: the
Go server embeds and serves the HTML, CSS, JavaScript, and self-hosted font.

You need Go 1.26 or newer, Git, and a C compiler for tree-sitter. On Ubuntu the
compiler is supplied by `build-essential`; on macOS use Xcode Command Line
Tools. Docker with Compose is needed for the supplied Postgres setup, and Make
is needed for the convenience commands. An existing suitable Postgres can be
used without Docker; it must support the pgvector extension and migrations.

## 2. Start with the existing local database

The user's `cornifer-postgres` container is already running on host port
**5433**, and its database was successfully migrated through version **12**
with embedding dimension **1024**. The latest animated website is already
running at **http://127.0.0.1:7788**. Older Cornifer web listeners were stopped.

To check the current service, without contacting model providers:

```sh
curl --fail http://127.0.0.1:7788/api/health
docker ps --filter name=cornifer-postgres
```

The health response currently reports `status: "ok"`,
`embedding_configured: false`, and `chat_configured: false`. Configuration
flags indicate that required environment values exist; they do not test a
provider key or prove that indexing will succeed. A new database has no ready
snapshots, so an empty repository list is expected.

For a later restart, first stop the existing website process rather than
launching a second copy on the same port. In the checkout above:

```sh
export CORNIFER_DATABASE_URL='postgres://cornifer:cornifer@127.0.0.1:5433/cornifer?sslmode=disable'
export CORNIFER_EMBEDDING_DIM=1024
export CORNIFER_COMPANION_DIR="$PWD/.cornifer-companion"
export CORNIFER_COMPANION_ADDR='127.0.0.1:7788'

go run ./cmd/migrate status
go run ./cmd/migrate up
go run ./cmd/cornifer-serve
```

Keep the service terminal open. Visit **http://127.0.0.1:7788**. Keep the same
database URL and data directory across restarts: ready snapshots depend on
both database records and their recorded local checkout/cache paths.

### Optional: create a fresh Compose database

Use this only when you do not already have the desired database running.
The supplied Compose file uses the fixed container name `cornifer-postgres`
and port 5433; it cannot start a second independent database on those same
resources. Compose volumes are also tied to the Compose project, so changing
checkout/project can select a different volume.

After setting the environment above, run:

```sh
make up
go run ./cmd/migrate up
go run ./cmd/cornifer-serve
```

`make up` starts `pgvector/pgvector:pg16` and waits for Postgres readiness.
It does not index a repository or configure a provider. The supplied
`cornifer:cornifer` database credentials are local development defaults.

### Embedding dimension must match storage

Migration 7 creates `chunks.embedding` as `vector(N)`, using
`CORNIFER_EMBEDDING_DIM` (default 1024 in this checkout). Subsequent `migrate up`
runs do **not** resize the existing column. Keep the server's embedding
dimension equal to the database column and the embedding model's output.
Do not roll back migrations or delete a volume just to resolve a mismatch in
a database containing work you want to keep.

If using the supplied container, inspect the column without changing it:

```sh
docker exec -i cornifer-postgres psql -U cornifer -d cornifer -c \
  "SELECT format_type(atttypid, atttypmod) FROM pg_attribute WHERE attrelid='chunks'::regclass AND attname='embedding';"
```

## 3. Configure hosted services only when needed

Embeddings and chat are separate. Neither is required to start the website.
Graph/source browsing makes no model calls. Previously indexed snapshots can
use BM25 lexical retrieval without embedding credentials, provided their
local caches and checkouts remain available. The companion does not substitute
fake vectors or run local inference for new URL indexing.

### Voyage embeddings: required for new companion indexes

Configure these **before submitting a URL**. Adding or searching a
repository with hosted embeddings configured can make billable requests.

In the terminal that will start the service:

```sh
export CORNIFER_EMBEDDING_PROVIDER=voyage
export CORNIFER_EMBEDDING_MODEL=voyage-code-3
export CORNIFER_EMBEDDING_DIM=1024
```

Read the key without typing its value into shell history. For Bash:

```bash
read -r -s -p 'Voyage API key: ' CORNIFER_EMBEDDING_API_KEY
printf '\n'
export CORNIFER_EMBEDDING_API_KEY
```

For Zsh (the current machine's shell):

```zsh
read -r -s 'CORNIFER_EMBEDDING_API_KEY?Voyage API key: '
printf '\n'
export CORNIFER_EMBEDDING_API_KEY
```

The adapter also accepts `VOYAGE_API_KEY` as a fallback when
`CORNIFER_EMBEDDING_API_KEY` is unset or empty. The default embedding endpoint
is `https://api.voyageai.com/v1/embeddings`; the optional
`CORNIFER_EMBEDDING_BASE_URL` overrides that complete endpoint URL. Voyage is
the only supported companion embedding provider. Engine CLI sidecar/fake
options do not enable those providers in the website.

Restart `go run ./cmd/cornifer-serve` after changing configuration, then reload
the page. Environment changes in another terminal do not update a running
server. Keep keys out of the website, committed files, command arguments,
screenshots, and printed environment dumps. Hidden input prevents the key's
literal value entering command history; it remains in the process environment.

### Optional hosted chat

Without chat configuration, **Ask Cornifer → Search** returns cited evidence;
it does not generate an answer. With chat configured, the same Search button
requests a hosted answer grounded in that evidence.

```sh
export CORNIFER_CHAT_PROVIDER=openai_compatible
export CORNIFER_CHAT_BASE_URL='https://provider.example/v1/chat/completions'
export CORNIFER_CHAT_MODEL='replace-with-your-provider-model'
```

The URL and model above are placeholders: supply your chosen hosted provider's
full **chat completions endpoint**, not just its API root. The adapter does not
append `/chat/completions`. Read `CORNIFER_CHAT_API_KEY` with the same hidden
input pattern used above, then export it and restart the server.

The provider value `openai` is also supported. The endpoint must accept an
OpenAI-compatible chat completions request with `response_format` set to
`json_object`. The adapter rejects empty answers and evidence IDs not present
in the retrieved pack. This checks citation identifiers, not the factual truth
of every claim; inspect the linked source yourself.

To return to retrieval without hosted calls, stop the server, unset both sets
of configuration and the Voyage fallback key, then restart:

```sh
unset CORNIFER_EMBEDDING_PROVIDER CORNIFER_EMBEDDING_MODEL CORNIFER_EMBEDDING_API_KEY
unset CORNIFER_EMBEDDING_BASE_URL VOYAGE_API_KEY
unset CORNIFER_CHAT_PROVIDER CORNIFER_CHAT_BASE_URL CORNIFER_CHAT_MODEL CORNIFER_CHAT_API_KEY
go run ./cmd/cornifer-serve
```

## 4. Add a repository and follow its job

1. Enter `https://github.com/owner/repository` in **Add public GitHub repository**.
   Repository URLs with `.git` are accepted; file/tree URLs, credentials, query
   strings, and fragments are rejected. Public GitHub is the supported intake.
2. In **Git ref, optional**, enter a branch, tag, or commit SHA. Leave it blank
   to resolve the remote HEAD. There is no authenticated private-repository UI.
3. Activate the **＋ / Index repository** button.
4. Follow the repository entry's state and select it to inspect the snapshot.

The runner shallow-clones, fetches the requested ref, resolves it to a commit,
and checks out that commit detached. It disables Git hooks, does not run the
repository's application or build, and does not recursively check out
submodules. The displayed SHA identifies the snapshot; subsequent remote
changes do not update that checkout automatically.

| State | Meaning and next step |
| --- | --- |
| `queued` | A background job has been created in this server process. |
| `cloning` | Fetching the public repository. |
| `resolving` | Fetching/resolving the ref to its pinned commit, not resolving graph edges yet. |
| `indexing` | Parsing, chunking, storing graph/text data and creating embeddings. |
| `ready` | Select the snapshot to explore and retrieve evidence. |
| `awaiting_credentials` | Clone/ref resolution finished, but embedding credentials were missing. Configure/restart, then explicitly resubmit the same URL/ref to start a new job for that pinned snapshot. |
| `failed` | Indexing failed; the UI displays a safe message. Correct the cause before a new submission. |
| `cancelled` | Cancellation ended that job. Submit again after addressing the reason for cancellation. |
| `stale` | A recognized status in the data contract; the current runner does not automatically set it when a remote branch moves. |

The page polls a submitted job about every 1.1 seconds while active.
**Cancel indexing** appears only while the page knows a cancellable job.
Cancellation is a request to stop work; it does not erase the registry entry
or guarantee rollback of partial indexing. Counters are reported at pipeline
boundaries and can remain zero during a long stage. There is no ETA.

### Retry, reload, and restart limits

- There is no dedicated Retry or Resume button. Resubmitting the form is the
  available intake action. **Refresh repositories** reloads registry status;
  it does not restart a job.
- An active request with the same canonical URL/ref is deduplicated. For an
  `awaiting_credentials` request, the corrected procedure is: stop the server,
  configure Voyage credentials in its terminal, restart it, reload the page,
  enter the **same URL and same original ref** (including blank if it was blank),
  and activate **Index repository** again. This explicitly requeues the existing
  snapshot with a **new job ID**, retaining its already resolved commit SHA.
  It reruns clone/index work, not a saved pipeline step. Repeated submission
  while this retry is active selects the active job rather than starting another.
  Credentials alone never automatically resume it. If credentials remain absent,
  an explicit retry returns to `awaiting_credentials` again.
- Failed/cancelled records are not deduplicated as active work. Resubmission
  creates a new request, rather than continuing from a saved pipeline step.
  Repeated requests resolving to an identical URL/ref/SHA also have a known
  snapshot uniqueness/finalization risk; successful repeat indexing is not
  guaranteed by the current implementation.
- Jobs run in memory within the server process. Restarting the server does
  not automatically resume persisted queued/cloning/resolving/indexing jobs
  or reconcile interrupted states. Cancel active work and wait for a terminal
  state before a planned restart. After a crash, a stuck record may require
  a lifecycle fix rather than merely another refresh.
- Reloading the browser loses its in-memory job tracking. Resubmit the same
  active URL/ref to obtain its existing job and resume **page polling**, not
  the indexing operation. A polling request error is displayed; the page
  does not automatically retry that failed poll.
- The embedding HTTP adapter has bounded retries for transient network,
  429, and 5xx errors (up to five retries). That is separate from job retry;
  it does not resume a job after credentials change or the server exits.

## 5. Explore a ready snapshot

Choose a repository entry in **Repositories**. The header shows its repository,
status, short commit SHA, symbol count, and relation count. Different completed
snapshots can be selected independently; there is no separate snapshot picker
or comparison view.

### Files, symbols, graph, and source

- **Files & symbols** lists indexed file paths and structural symbols. Click a
  file to open numbered source; click a symbol to select its graph node and
  populate the inspector. These are read-only views of the local pinned checkout.
- **Find file or symbol…** currently filters file paths first, then symbols
  within matched paths. A symbol name alone may not find its file. Search by
  part of the file path and browse its symbols, or use Ask Cornifer for text
  retrieval. This is a current search limitation.
- The **Graph** tab shows indexed Python nodes and real resolved edges. It draws
  a connected slice of at most 100 nodes, focusing around a selected symbol.
  The API itself is bounded to 2,500 nodes and 10,000 edges; large repositories
  are not represented in full by this view.
- Drag the graph background to pan, scroll to zoom, or use **Zoom in**,
  **Zoom out**, and **Fit graph to view**. Fit resets the view transform; it
  does not recalculate a bounding box for every node.
- **Relationships** filters All types, Calls, Imports, Inherits, or Implements.
  An available filter does not imply the index contains that edge type.
- Select a graph node to see its kind, qualified name, signature if available,
  source location, and incoming/outgoing relationships. Relationship buttons
  select the related symbol; **Open source** or the location link opens its
  source range. The inspector displays at most 60 related entries.
- The **Source** tab and citation links open numbered excerpts, not a full editor.
  Clicking a file starts with lines 1–80; changing to Source uses lines 1–100.
  The source endpoint limits an individual range to at most 501 lines.

Python is the only language with structural symbol/relationship extraction.
Common documentation and other source extensions are admitted to the file
catalog without structural nodes or edges. Examples include Markdown, Go,
JavaScript/TypeScript, Java, Rust, C/C++, Ruby, PHP, shell, JSON, YAML, and TOML.
The intended generic lexical fallback is incomplete in this checkout: the
chunk loop currently includes only successfully parsed Python files, so generic
files can appear in navigation/source but are not guaranteed searchable as
evidence. A repository containing only these files may have no retrievable
chunks. This is not equivalent language analysis. Unsupported
extensions, excluded files, external dependencies, and unresolved relationships
are not guaranteed to appear. **About language coverage** and the in-place
coverage note explain this distinction. An empty graph does not mean the
repository has no code; searchable evidence also depends on actual chunks.

### Ask Cornifer, context, and citations

Enter a code question in **Ask Cornifer**, then select **Search** or press
Enter. Shift+Enter inserts a newline. This works only for a ready snapshot.

Without hosted chat, results are source excerpts with `[e1]`, `[e2]`, etc.
With hosted chat, a generated answer appears above the evidence. With embedding
credentials absent, an existing index uses BM25 lexical retrieval. With Voyage
configured, uncached queries can send the question to Voyage for a query
embedding and use hybrid retrieval with graph adjacency boosting.

Each evidence card shows path, line range, symbol where available, excerpt,
retrieval provenance, and an excerpt SHA-256 prefix. Click its citation or an
answer's inline evidence link to open the source. A citation's excerpt hash is
distinct from the repository commit SHA. Evidence can be truncated by the
context budget. No matches means insufficient retrieved evidence, not proof
that a behavior is absent from the repository.

The underlying context pack also contains the repository SHA, capabilities,
relationships, retrieval systems, and omission information. The website renders
the evidence cards; MCP `get_context` exposes the structured pack. Defaults are
eight evidence hits and an 18,000-byte excerpt budget; MCP can request different
limits, with evidence count capped at 20. Context packs are cached for ten
minutes (up to 256 cache entries). Cache reuse can avoid retrieval work; it does
not turn hosted chat into a free or offline operation.

### Keyboard, small screens, and motion

Tab moves through controls; Enter/Space activates focused graph symbols.
Ctrl+K or Cmd+K focuses the catalog search; Escape leaves its focus. Enter in
the catalog opens the first visible file/symbol result. The graph zoom controls
are keyboard focusable. At narrower widths the workspace stacks, so scroll to
reach the assistant and memory sections. A system/browser **Reduce motion**
preference disables UI animations and animated graph zoom. Surfaces are flat
Gruvbox fills with self-hosted Roboto Mono; no gradients are used.

## 6. Keep an explicit second-brain session

The first successful context retrieval creates an application session.
**Session memory** then shows its commit scope/expiry and enables **Save note**
and **Clear**. Save a decision or reminder in the note field; prior search
questions and saved notes appear in the event list. There is no separate
Create session button.

- The stable application `session_id` is stored in Postgres. The browser stores
  its last ID per repository in localStorage, so it can reconnect after refresh
  in the same browser profile. A different origin/port/profile has a different
  localStorage store. The UI has no field to paste a session ID; MCP/API clients
  can explicitly supply one.
- Sessions are scoped to **repository snapshot ID and resolved commit SHA**.
  Reusing an ID with another snapshot, a stale session, or an expired session
  fails context retrieval. A newly indexed moving branch gets its own snapshot;
  old memory remains attached to the old one, rather than automatically becoming
  stale or being transferred.
- Default expiry is seven days from creation; routine searches/notes do not
  extend it. Only the most recent 64 events are retained per session. Notes are
  limited to 2,000 bytes by the service (the UI also has a 2,000-character limit).
- Notes and retrieved context events persist across service restarts. There is
  no automatic rolling summary generation or automatic injection of saved notes
  into search/chat. An MCP client can read the events and deliberately use them
  in its own workflow.
- **Clear** immediately deletes that session and its events; there is no undo
  or confirmation dialog. The next search creates another session. Clearing
  memory does not remove indexed source, embeddings, repository jobs, or the
  separate context cache.
- Expiry is enforced when reusing a session for context. A cleanup function
  exists, but this server does not schedule it: do not assume expired sessions
  are automatically deleted or that total storage is capped at 100 sessions.

## 7. Connect an MCP client

The website server exposes Streamable HTTP MCP at:

```text
http://127.0.0.1:7788/mcp
```

The top **MCP** button and the **MCP connection** endpoint button copy this URL.
They do **not** copy a full client configuration or API keys. If clipboard
access is unavailable, copy the displayed URL manually. There is no MCP client
connection test/pairing button; the page's connected status refers to the
local HTTP companion.

Configure your client's Streamable HTTP connection with that URL. For clients
using the common `mcpServers` URL configuration, a representative entry is:

```json
{
  "mcpServers": {
    "cornifer-companion": {
      "url": "http://127.0.0.1:7788/mcp"
    }
  }
}
```

Client configuration formats vary; some need an explicit HTTP transport type.
The running server owns provider credentials. A client running in another
container or computer cannot use its own `127.0.0.1` to reach this host server.
Keep the current local binding; remote access/authentication is not implemented
as a production deployment flow.

### Exact companion tools and arguments

The server identifies itself as `cornifer-companion`, version `0.2.0`.
Use returned repository IDs, not the engine's numeric `engine_repo_id`.
Fields marked optional can be omitted.

| Tool | JSON arguments | Result/use |
| --- | --- | --- |
| `list_repositories` | `{}` | Local snapshot records and safe status. |
| `get_index_status` | `{"repository_id":"<snapshot-id>"}` | Repository snapshot/status; it does not return the separate Job object. |
| `ingest_repository` | `{"url":"https://github.com/owner/repository","ref":"<branch-tag-or-SHA>"}` (`ref` optional) | `repository`, `job`, `reused`; starts/deduplicates intake under the same lifecycle limits as the UI. |
| `get_context` | `{"repository_id":"<snapshot-id>","question":"Where is routing handled?","session_id":"<session-id>","evidence_limit":8,"context_bytes":18000}` (last three optional) | `context` plus `session`; omit `session_id` to create a session, then retain returned `session.id`. |
| `remember_context` | `{"session_id":"<session-id>","note":"Check routing before changing dispatch."}` | Updated session and a persisted explicit note. |
| `get_session_context` | `{"session_id":"<session-id>","limit":16}` (`limit` optional) | `session` and recent `events`; default/max 64. |
| `clear_context` | `{"session_id":"<session-id>"}` | Deletes that session/events and returns `{"cleared":true}`. |

For a typical second-brain workflow: list repositories, choose a ready snapshot,
call `get_context` without a session ID, retain `session.id`, remember a note,
and supply the same `session_id` on later context calls for that snapshot.
Call `get_session_context` to inspect memory, and `clear_context` when done.

An MCP transport/session identifier is **not** this application session ID.
Transport reconnection does not select your memory. Companion MCP has no
`generate_answer`, graph-navigation, source-read, or cancel-job tool; cited
context comes from `get_context`, and those other operations are website/API
features. The MCP read-only annotation on `get_context` does not prevent it
from recording a local session/event.

### Companion stdio is a separate launch option

For a client supporting only stdio, run the same registry/context/session tools
with the same environment and database using:

```sh
go run ./cmd/cornifer-companion-mcp
```

Normally the MCP client launches this command with its working directory set
to the checkout and its environment configured. Stdout carries protocol
messages; logs go to stderr. It does not start the website. Avoid submitting
duplicate jobs from separate server processes: job execution/cancellation
is process-local.

`go run ./cmd/cornifer-mcp` is the **older engine MCP** with different tools,
such as `search_code` and `find_definition`, and engine repository configuration.
It is not a replacement for companion session-memory tools. No local inference
setup is required or performed by the workflows in this guide.

## 8. Storage, provider data, and operating costs

| Location or service | What it stores/receives |
| --- | --- |
| Local Postgres | Repository/job metadata, files, symbols, edges, source chunks, embedding vectors, context cache, sessions and event payloads. |
| `CORNIFER_COMPANION_DIR` | Shallow repository checkouts under `repos/`, BM25 caches under `cache/`, and embedding cache under `embeddings/`. Recorded snapshot paths must stay available. |
| Browser localStorage | The last application session ID per repository for that origin/profile, not API keys. |
| GitHub | Repository/ref fetch requests; public code is downloaded to this machine. |
| Voyage when configured | Indexing source chunks and uncached search questions for embeddings, plus model/output-dimension/request metadata. The current chunk-loop gap excludes generic non-Python files from embedding/retrieval chunks. |
| Hosted chat when configured | The bounded context pack: query, repository URL/SHA/capabilities, evidence excerpts/path/lines/hashes, retrieval metadata and relationships. Saved session notes are not automatically included. |

Repository size, chunk count, repeated snapshots, embeddings, source caches,
and session evidence all consume disk. There is no website repository-delete
control, storage quota UI, automatic clone eviction, or scheduled expiry purge.
The Compose volume persists after `make down`; back up both the database and
the companion directory if you need durable usable snapshots. Deleting local
files alone can break source/retrieval while leaving the registry `ready`.

No paid provider request has been made during this worker's verification.
Provider charges depend on your account/model and usage; this guide does not
quote prices or promise free requests. Starting without provider configuration
does not call a model provider, but submitting a repository still contacts
GitHub and downloads its checkout before reaching `awaiting_credentials`.

## 9. Stop, restart, and troubleshoot

For a website you launched in a terminal, press **Ctrl+C**. For the current
worker-started instance, use its owning session/process, rather than starting
another copy. If you need to identify a port owner on Linux:

```sh
ss -ltnp 'sport = :7788'
```

Stop the identified Cornifer process with `kill -INT <pid>` only after checking
that it is the intended server. Keep Postgres running during website restarts.
To stop a database managed by this checkout's Compose project, use `make down`;
for the existing named container, `docker stop cornifer-postgres` and later
`docker start cornifer-postgres` preserve its volume. Do not use `down -v` to
stop a database whose data you want to retain.

Go embeds the UI at build time. After changing UI files, stop and rerun
`go run ./cmd/cornifer-serve` to build the current bundle, then reload the page.
The guide/retry bundle uses asset version `workspace-9`. Merely refreshing
an older binary does not install new UI code.

| Problem | Check/action |
| --- | --- |
| `bind: address already in use` | The app is already running or another process owns 7788. Open the existing app or stop the verified old process. If intentionally changing port, set `CORNIFER_COMPANION_ADDR='127.0.0.1:7794'`; UI MCP copy uses the active port. |
| Database connection refused | Check `docker ps`, container readiness, and the same `CORNIFER_DATABASE_URL` in your server terminal; the host port is 5433, not container port 5432. |
| Missing tables | Run `go run ./cmd/migrate status` and `go run ./cmd/migrate up` from the checkout with the server's database URL. |
| Vector dimension mismatch | Inspect the existing vector column, use matching configuration, and choose a separate correctly migrated database if necessary; rerunning migrations cannot resize it. |
| Empty repositories | A fresh database has none. The worker's FastAPI verification fixture is not installed in the user's database. Index only after configuring credentials. |
| `awaiting_credentials` persists | Configure embeddings, restart/reload, then explicitly submit the same URL and original ref. Restart/configure alone does not create a retry job. |
| Failed URL/ref | Use a public repository root URL and a ref accepted by GitHub. Safe messages intentionally omit raw command/provider output. |
| Stuck active job after restart | No automatic job recovery/reconciliation exists. Refresh checks status, not worker liveness. |
| Graph absent | Confirm `ready`, Python structural coverage, available indexed symbols, and intact checkout/cache paths. Other languages have no graph nodes; the current generic chunk-loop gap also limits lexical fallback. |
| Symbol search misses a name | The catalog currently filters file paths before symbols. Use the filename/path and browse, or retrieve code text through Ask. |
| No evidence | Try terms present in the code. Missing embedding credentials uses lexical retrieval for existing indexes; an empty result is not a generated answer. |
| Hosted chat error | Check all four chat env values and full endpoint/model compatibility, restart, and reload. When configured chat fails the UI displays the error; it does not automatically fall back to the retrieval-only endpoint. |
| Source/cache unavailable | Restore the recorded companion checkout/BM25 cache paths and database together. Changing the base directory does not migrate existing records. |
| Memory disappeared | Check same origin/browser profile, snapshot ID/SHA, seven-day expiry, or prior Clear. MCP can inspect a known application session ID. |
| Clipboard unavailable | Copy the displayed MCP endpoint manually. |
| Old UI remains | Restart the server from this worker checkout, verify the intended port and current assets, then reload/cache-bust the page. |

## Completion status and known limits

The redesign and local documentation are implemented. The product is **not
fully complete or production validated**.

| Status | Evidence and practical limit |
| --- | --- |
| **Built** | Animated flat Gruvbox/Roboto Mono workspace; public URL intake/jobs/cancel/status; indexed graph/source explorer; cited retrieval/optional chat adapter; persistent explicit memory; companion HTTP/stdio MCP. |
| **Verified locally** | Earlier project Go tests and vet passed. The focused retry was checked against a separate temporary Postgres database using a controlled runner, with a Git stub checking pinned-SHA fetch; no hosted requests were involved. Changed explorer/source behavior has tests. UI syntax, build, motion interpolation/cancellation/reduced-motion behavior, and absence of gradients/old visual assets were checked. |
| **Verified on a real indexed fixture** | The explicitly identified FastAPI 0.115.0 fixture at pinned commit `40e33e492dbf…` contained 44 files, 716 symbols and 524 edges. Browser/API checks covered repository selection, graph node/filter/zoom/fit, source navigation, lexical context/citations, and session create/remember/clear. This used existing indexed fixture data, not a new hosted ingestion run. |
| **Current user app verified** | One Cornifer listener on localhost7788, existing user Postgres5433 preserved, health OK, current embedded assets loaded, empty/no-credentials states inspected in AO Browser. The user has since submitted Cornifer itself; it is awaiting embedding credentials, not ready. The temporary fixture preview was stopped during server cleanup. |
| **Partial interaction verification** | Graph pan logic was inspected and motion logic checked, but a complete browser pan/coordinate assertion and responsive viewport matrix remain unverified. A screenshot was captured during earlier animation work; other screenshot attempts were blocked by panel visibility/timeouts. Copy control was inspected, but an independent OS clipboard read-back was not completed. These are verification gaps, not evidence of provider success. |
| **Credential retry corrected** | Explicit same-URL/ref submission requeues a credential-blocked snapshot after configuration/restart; its SHA is retained. This is regression-tested locally, not a successful live provider ingestion. |
| **Provider-blocked / not exercised** | No new end-to-end public URL → Voyage embedding → ready corpus run, live hybrid provider query, or hosted chat answer validation has been performed. Adapter tests use controlled test responses. Keys are absent in the current running app; no paid requests were made. |
| **Known implementation gaps** | Interrupted active-job restart recovery, same-SHA repeat finalization, independent symbol-name catalog search, incomplete generic-file lexical chunking, automatic memory recall/summary, scheduled expiry cleanup, full-client MCP configuration copy, and large/full graph coverage remain limited as described above. |
| **Not delivered** | Production deployment, authentication/remote-user operation, private GitHub ingestion, structural language parity beyond Python, or exact GitNexus feature parity. Changes remain local unpublished worker commits. |

Implementation references: [server startup](../cmd/cornifer-serve/main.go),
[website controls](../cmd/cornifer-serve/web/app.js),
[job and session behavior](../internal/companion/service.go),
[snapshot storage](../internal/companion/postgres.go),
[provider configuration](../internal/companion/runner.go),
[chat adapter](../internal/companion/chat.go), and
[companion MCP tools](../internal/companion/mcp.go).
