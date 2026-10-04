# Product plan: web and MCP repository intelligence

Cornifer's current Go engine is the foundation, not the completed product.
The product accepts a public GitHub repository URL, pins an exact commit,
indexes it without executing repository code, and exposes the same indexed
snapshot to a website and callable MCP server. A main coding agent can retrieve
evidence directly; hosted answer generation is optional rather than a required
hop for agents.

## Selected architecture: lightweight companion service

The user selected a lightweight companion service. The local Go process owns
cloning, indexing, PostgreSQL access, credentials, website and MCP transport;
the website is a loopback client and never sees provider keys. Browser-only is
out of scope because it requires a different parsing, storage, and credential
model.

| Option | What runs locally | Key handling | Consequence |
| --- | --- | --- | --- |
| Lightweight companion service **(selected)** | Clone, Go indexer, Postgres, and MCP backend | Hosted-provider keys stay in the companion | Loopback website/API and Streamable HTTP MCP share one service contract. |
| Browser-only | WASM git/parser/index and browser storage | A provider key cannot safely be kept in browser JavaScript | Requires a substantially different runtime and either no hosted generation or a credential-brokering backend. |

The contracts below are shared by the selected website and MCP transports.

## Reusable core today

The existing engine already supplies:

- safe Python source walking, Tree-sitter parsing, symbols, heuristic
  import/call resolution, unresolved-reference accounting, AST chunks, and
  incremental reindexing;
- repository-scoped PostgreSQL/pgvector storage, BM25, hybrid RRF, graph boost,
  exact commit snapshots, and embedding provenance;
- seven bounded stdio MCP reads: search, definition, references, dependencies,
  call graph, blast radius, and cycles; and
- source-located snippets, signatures, graph edge kind/confidence, and a
  reproducible evaluation harness.

The companion now supplies durable registry/job state, public-GitHub URL
validation, a loopback website, multi-repository selection, Streamable HTTP
MCP, deterministic cited context packs, explicit application sessions, a
hosted-chat adapter, and a credential boundary. It still needs a production
job worker/queue, non-loopback auth, non-Python structural extraction,
additional provider adapters, and end-to-end real hosted-provider validation.

## Shared contracts

All website and MCP reads identify an immutable snapshot rather than a working
tree. A request may use `repo_id` plus optional `commit_sha`; an omitted SHA
means the registry's current ready snapshot, returned explicitly in every
response.

### Repository and job

```text
Repository {
  repo_id, canonical_url, requested_ref, resolved_commit_sha,
  status: queued|cloning|resolving|indexing|ready|failed|cancelled|stale,
  capabilities, language_coverage, created_at, updated_at
}

IndexJob {
  job_id, repo_id, phase, progress: {files_seen, files_indexed, chunks, edges},
  resolved_commit_sha?, error_code?, safe_message?, cancellable, timestamps
}
```

`canonical_url` normalizes supported public GitHub HTTPS and SSH forms. URL
ingestion resolves the requested ref to a commit SHA before indexing, dedupes
active work for the same `(canonical_url, requested_ref)`, permits cancellation
only at safe phase boundaries, and never runs repository hooks, build scripts,
or repository-owned configuration. Each completed discovery creates an
immutable companion snapshot row, so a moved ref preserves old sessions,
context packs, checkout attribution, and provider provenance. Active work for
the same URL+ref is deduplicated; historical rows stay addressable by ID.

`capabilities` must name what was actually indexed, for example
`python_structural_graph`, `text_lexical`, and `embeddings`. The first release
must label Python as the only language with structural graph coverage. Other
source/text files may be searchable only when their capability says so; neither
the UI nor MCP may imply a complete graph for them.

### Evidence pack

`get_context` is the common deterministic building block for website chat and
agents. It takes a snapshot selector, question, retrieval budget, optional
symbol focus, and graph depth. It returns bounded retrieved excerpts and graph
relationships; it does not call a chat model.

```text
ContextPack {
  snapshot: {repo_id, canonical_url, commit_sha, capabilities},
  query, evidence[], relationships[], omitted: {evidence_count, reason},
  retrieval: {systems_used, degraded?, cache_key, context_bytes}
}

Evidence {
  citation_id, path, start_line, end_line, symbol?, snippet,
  excerpt_sha256, retrieval_sources, truncated
}

Relationship {
  from_citation_id?, to_citation_id?, from_symbol?, to_symbol?,
  kind, confidence, depth
}
```

Paths are normalized repository-relative slash paths; line ranges are
one-indexed and inclusive. `commit_sha + path + start_line + end_line +
excerpt_sha256` is the citation identity. A response must return an explicit
`insufficient_evidence` status when retrieval produced no support instead of
inventing an answer. Current MCP responses are source-located, but this common
pack closes two gaps: commit SHA on every result and line ranges for
module-level chunks.

### Website answer

Website chat consumes a ContextPack. `POST /api/answer` is available with an
explicit OpenAI-compatible provider configuration; the checked-in UI currently
shows the deterministic evidence view and credentials-required state rather
than making a provider call. When a chat call is made, the server sends only
that bounded pack and returns:

```text
Answer {
  status: complete|insufficient_evidence|provider_unavailable|error,
  text?, citations[], snapshot, provider?: {name, model},
  usage?: {input_tokens, output_tokens}, cached
}
```

Every citation must be a `citation_id` present in the pack; the server rejects
or strips unsupported citations. The UI shows the answer beside expandable
source excerpts, graph relationships, snapshot SHA, truncation, and degraded
retrieval/provider errors. It must say that credentials are missing rather
than manufacture an answer from the fake embedder.

Embeddings and chat are separate server-side configurations: provider, model,
base URL, timeout, and secret reference. Secrets never reach browser code,
MCP result payloads, logs, or repository metadata. Cache keys include snapshot
SHA, normalized question, retrieval settings, prompt/version, and provider
model; caches must never cross snapshots.

## Website and MCP surface

The current website implements public-GitHub URL intake with optional ref,
ready-repository selection, deterministic evidence/source panes, a visible
credentials-required state, and repository-scoped memory notes. Job status is
polled; cancellation and rendered graph relationships are API-supported but
not yet exposed as polished UI controls. It names Python structural coverage
before questions are asked.

The legacy `cornifer-mcp` keeps its existing stdio structural tools.
`cornifer-companion-mcp` and the companion's Streamable HTTP `/mcp` endpoint
register the same registry/context/session tools; application session IDs are
explicit data, not transport session IDs. Both website and MCP transports call
the same ContextPack service. Current companion tools are:

- `ingest_repository(url, ref?)`, `list_repositories()`, and `get_index_status(repo_id)`;
- `get_context(question, repository_id, session_id?, budget?)`; and
- `remember_context`, `get_session_context`, and `clear_context`.

Agents may use `get_context` and existing retrieval tools directly, with no
mandatory paid summarization. Website chat may opt into hosted generation only
when its separate chat credential/configuration is available.

## Acceptance criteria

1. A public GitHub URL/ref becomes a canonical URL and exact SHA; repeated
   intake dedupes the ready snapshot and no repository code/config executes.
2. Job status reports safe phases, counts, cancellation, and actionable errors;
   a failed job never becomes selectable as ready.
3. Every web/MCP evidence response identifies repository and commit SHA, emits
   normalized cited excerpts, graph relationship confidence, and explicit
   truncation/degradation.
4. Python repositories clearly support symbols/call-import graph queries.
   Unsupported languages are visibly text/lexical-only until a parser and
   resolver capability is added.
5. Stdio and Streamable HTTP MCP return the same snapshot/evidence semantics;
   multi-repository requests cannot leak results across snapshots.
6. Website answers cite only returned evidence, surface insufficient evidence
   and missing-provider states, apply a context budget, and cache only within
   an identical snapshot/settings/model tuple.
7. Embedding and chat provider/model/base URL are independently configurable;
   secrets remain server-side and no local LLM or embedding inference is
   required.

## Influences, not dependencies

- [GitNexus](https://github.com/abhigyanpatwari/GitNexus) demonstrates the
  useful split between graph-backed MCP and a web explorer, plus explicit
  repository discovery/context tools. It is under the
  [PolyForm Noncommercial 1.0.0](https://raw.githubusercontent.com/abhigyanpatwari/GitNexus/main/LICENSE),
  so Cornifer must not copy its implementation into a commercial product.
- [DeepWiki-Open](https://github.com/AsyncFuncAI/deepwiki-open) illustrates
  URL-to-structure-to-generated documentation and a source/wiki presentation.
  It is [MIT licensed](https://raw.githubusercontent.com/AsyncFuncAI/deepwiki-open/main/LICENSE);
  Cornifer borrows only the product lesson, not code.
- [Repomix](https://github.com/yamadashy/repomix) demonstrates token accounting,
  structured context packaging, ignore handling, and secret-aware sharing. It
  is [MIT licensed](https://raw.githubusercontent.com/yamadashy/repomix/main/LICENSE).
  Cornifer should borrow the context-budget and safe-input ideas, while using
  retrieved evidence packs rather than shipping an entire repository by
  default.
