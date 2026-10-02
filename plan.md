# Cornifer — Repository Intelligence Engine

Parse a real codebase into a **structural index** (AST → symbols → import/call graph) and a **semantic index** (AST-aware chunks → embeddings + BM25), answer queries by combining exact structural lookups with hybrid retrieval, and expose it all as an **MCP server** so any AI tool can call it.

## Decisions to confirm

| Decision | Default in this plan | Notes |
|---|---|---|
| Implementation language | **Go** (the project lives under `go/`) | Original spec assumed Python. Go has tree-sitter bindings (`smacker/go-tree-sitter`), `pgx`, `pgvector-go`, and an official MCP SDK (`modelcontextprotocol/go-sdk`). Python would be easier for embeddings/eval glue; see "Embedding service" below for how to bridge. |
| Language being indexed | **Python** (one language, deep) | TypeScript is the alternative. Do not support both. |
| Target repo | **FastAPI** (~10K+ LOC of source, mature, well-typed) | Pin a commit SHA so eval labels stay valid. |
| Storage | **Postgres + pgvector** | One DB for symbols, edges, chunks, vectors, and (optionally) FTS. |
| Embedding model | Voyage `voyage-code-3` (API) or `jina-embeddings-v2-base-code` (local) | Behind an interface so both can be benchmarked. |

## What it answers that grep can't (lead the demo with these)

1. **Callers through interfaces** — "find every caller of this function, including via the interface it implements" → symbol resolution + call-graph traversal.
2. **Blast radius** — "what breaks if I change this file?" → reverse dependency graph, transitive closure.
3. **Cycles** — "is there a circular dependency between these modules?" → SCC/cycle detection on the import graph.
4. **Semantic search** — "code related to rate limiting" even if never named that → dense embeddings beat BM25/grep on vocabulary mismatch.

BM25-only RAG can't do 1–3; grep can't do any reliably.

## Architecture

```
Git repo
   ↓
┌──────────────── INDEXING PIPELINE ─────────────────┐
│ File walker → tree-sitter parser (Python grammar)   │
│      ↓                                ↓             │
│ Symbol extractor                AST-aware chunker   │
│ (funcs/classes/vars,            (function/class     │
│  signatures, docstrings)         chunks + enclosing │
│      ↓                           context)           │
│ Import + call-graph resolution        ↓             │
│      ↓                          Embedding model     │
│ Symbol table + edges (PG)             ↓             │
│                                 pgvector (ANN)      │
│                                 BM25 index          │
└─────────────────────────────────────────────────────┘
                        ↓
┌──────────────────── QUERY LAYER ───────────────────┐
│ Hybrid ranker: BM25 + vector + graph proximity      │
│ fused via RRF (+ optional cross-encoder rerank)     │
└─────────────────────────────────────────────────────┘
                        ↓
 MCP server: search_code, find_definition, find_references,
             get_dependencies, get_call_graph
             (+ optional REST endpoint)
```

## Proposed layout

```
cornifer/
├── cmd/
│   ├── cornifer/          # CLI: index, reindex, query, eval
│   └── cornifer-mcp/      # MCP server (stdio, optional HTTP)
├── internal/
│   ├── walker/            # file discovery, .gitignore, language detection, content hash
│   ├── parse/             # tree-sitter wrapper, per-language queries
│   ├── symbols/           # extraction: defs, signatures, docstrings, scopes
│   ├── resolve/           # import resolution + heuristic call resolution
│   ├── graph/             # in-memory graph, callers/callees, closure, SCC
│   ├── chunk/             # AST-aware chunker
│   ├── embed/             # Embedder interface + Voyage/Jina/local impls
│   ├── store/             # Postgres access, migrations, pgvector queries
│   ├── bm25/              # sparse index over chunks
│   ├── retrieve/          # hybrid search, RRF, graph boosting, rerank
│   ├── mcp/               # tool handlers
│   └── eval/              # eval harness, metrics, baselines
├── migrations/
├── eval/                  # queries.yaml (hand-labeled), results/
├── docs/                  # architecture diagram, design notes, eval writeup
├── docker-compose.yml     # postgres + pgvector
├── plan.md
└── README.md
```

## Data model (Postgres)

- `repos(id, root, commit_sha, indexed_at)`
- `files(id, repo_id, path, language, content_hash, module_name)` — `content_hash` drives incremental reindex.
- `symbols(id, file_id, kind, name, qualified_name, parent_id, start_line, end_line, signature, docstring)` — kinds: module, class, function, method, variable.
- `edges(id, src_symbol_id, dst_symbol_id, kind, confidence)` — kinds: `imports`, `calls`, `inherits`, `implements`. `confidence` records exact vs. heuristic resolution.
- `unresolved_refs(id, src_symbol_id, name, kind)` — call sites/imports that could not be resolved (keep them; they explain recall gaps).
- `chunks(id, symbol_id, file_id, text, context_header, token_count, embedding vector(N), tsv tsvector)`
- Indexes: HNSW on `embedding`, btree on `symbols(qualified_name)` / `edges(src)` / `edges(dst)`.

## Phases

### Phase 0 — Setup (½ day)
- [ ] `go mod init`, repo skeleton, `docker-compose.yml` with `pgvector/pgvector`.
- [ ] Migrations tool (`goose` or `golang-migrate`), Makefile targets: `up`, `migrate`, `index`, `test`.
- [ ] Clone target repo at a pinned commit into `testdata/` or a gitignored `repos/` dir.
- [ ] Decide embedding bridge (see below).

### Week 1 — Parsing + structural index

**Days 1–2: parsing and symbol extraction**
- [ ] File walker: respect `.gitignore`, skip vendored/generated, hash contents.
- [ ] tree-sitter Python grammar wired up; parse every file, log parse errors without aborting.
- [ ] Extract functions, classes, methods, module-level variables with line ranges, signatures, decorators, docstrings, and parent scope → `qualified_name`.
- [ ] Golden-file unit tests on small fixture files (nested classes, decorators, async defs, lambdas assigned to names).

**Day 3: schema and storage**
- [ ] Write migrations for the model above; bulk insert symbols with `pgx` `CopyFrom`.
- [ ] `find_definition(name)` working end-to-end from CLI (exact + qualified-name lookup).

**Days 4–5: imports and call graph (heuristic)**
- [ ] Module-level import extraction: `import x`, `from x import y as z`, relative imports, `__init__.py` re-exports.
- [ ] Map file paths ↔ dotted module names; resolve imports to files/symbols inside the repo; mark external imports as such.
- [ ] Call-site extraction; resolve by name + lexical scope + import bindings + `self.`/`cls.` method lookup + class hierarchy (walk MRO-ish base list).
- [ ] Interface/override handling: a call to `Base.method` also links to overriding subclasses (this is what enables "callers through the interface").
- [ ] **Document clearly that resolution is heuristic, not type inference.** Record confidence per edge and count unresolved refs.

**Days 6–7: graph queries**
- [ ] Load edges into an in-memory adjacency structure (forward and reverse).
- [ ] Callers/callees with depth limit; blast radius = reverse transitive closure over file/module imports and symbol calls.
- [ ] Cycle detection on the import graph (Tarjan SCC), reporting the shortest cycle per SCC.
- [ ] CLI commands for each; sanity-check against real FastAPI examples.

**Exit criteria:** index FastAPI in under a few minutes; the four structural queries return sensible answers you have spot-checked against IDE "find references."

### Week 2 — Retrieval layer

**Days 8–9: AST-aware chunking**
- [ ] One chunk per function/method/class; large classes split into a header chunk (signature, docstring, attribute list) plus one chunk per method.
- [ ] Prepend a context header (file path, enclosing class, imports used, signature) to every chunk's embedding text.
- [ ] Oversized functions: split at statement boundaries, never mid-statement. Tiny siblings may be merged up to a token budget.
- [ ] Module-level code not inside any def gets its own chunk.
- [ ] Tests: no chunk crosses a symbol boundary; every source line is covered by at least one chunk.

**Days 10–11: embeddings and ANN**
- [ ] `Embedder` interface (`Embed(ctx, []string) ([][]float32, error)`), batching, retry/backoff, on-disk cache keyed by chunk hash so re-runs are free.
- [ ] Store vectors in pgvector; build HNSW index; verify recall vs. exact scan on a sample.

**Day 12: BM25**
- [ ] Code-aware tokenizer: split `snake_case` and `camelCase`, keep the original identifier as a token too, drop stopwords sparingly.
- [ ] Either an in-process BM25 (persisted to disk) or Postgres FTS with `ts_rank_cd`; pick one, keep it behind an interface.

**Days 13–14: hybrid fusion**
- [ ] Reciprocal Rank Fusion: `score = Σ 1 / (k + rank_i)`, `k = 60`; parameterize.
- [ ] Graph boosting: after fusion, boost hits that are graph-adjacent (callers/callees/same module) to other top-N hits; weight configurable.
- [ ] Optional cross-encoder rerank of the top ~50 (behind a flag; only keep it if the eval shows it helps).
- [ ] Iterate against real queries; record what was tried in `docs/tuning.md`.

**Exit criteria:** `cornifer query "rate limiting"` returns relevant chunks that BM25 alone misses.

### Week 3 — API layer, evaluation, polish

**Days 15–16: MCP server**
- [ ] Tools: `search_code`, `find_definition`, `find_references`, `get_dependencies`, `get_call_graph`. Add `get_blast_radius` and `find_cycles` since they are the demo headliners.
- [ ] Tight JSON schemas, bounded outputs (limit/depth params, truncated snippets), and useful errors ("symbol ambiguous: candidates …").
- [ ] stdio transport first; test from Claude Code (`claude mcp add`).

**Day 17: optional REST endpoint**
- [ ] Thin HTTP layer over the same handlers so external agents can call without MCP.

**Days 18–19: evaluation (non-negotiable)**
- [x] Hand-build 20–30 queries in `eval/queries.yaml`, mixing types:
  - structural (find references / callers / blast radius) — ground truth verified with IDE "find references" (Pyright/Pylance) on the pinned commit;
  - semantic / vocabulary-mismatch (e.g. "rate limiting"-style intent queries);
  - exact-identifier lookups (where grep should do well — be honest).
- [x] The committed set has 22 source-grounded FastAPI queries at
  `40e33e492dbf4af6172997f4e3238a32e56cbe26` (7 structural, 7 semantic,
  8 identifier). It contains **no IDE-verified labels**: source inspection is
  explicitly recorded instead of fabricating Pyright/Pylance checks.
- [x] `cornifer eval` runs hybrid, BM25-only, vector-only, and ripgrep. It
  would include hybrid-without-graph-boost only when a real boost is wired;
  graph boosting is currently absent, so reports state that limitation rather
  than emit a meaningless duplicate row.
- [x] `cornifer eval` calculates precision@5, recall@5, and MRR overall and
  per query type, and writes reproducible raw JSON containing target/index
  SHA, label provenance, provider/model metadata, and exact top-five ranks.
- [ ] Run and commit a **real-embedding FastAPI** raw result. The current
  worktree has the pinned source but no Docker daemon/Postgres, so no target
  numbers were invented from fake embeddings.
- [ ] Failure analysis: classify actual target misses after the real-embedding
  result exists (resolution heuristics vs. chunking vs. embeddings).

**Days 20–21: polish**
- [ ] Incremental reindex: diff `content_hash`, re-parse changed files, delete/reinsert their symbols/chunks, re-resolve edges touching them.
- [ ] README: architecture diagram, quickstart, eval table, known limitations.
- [ ] Short demo recording built around the four "grep can't" questions.

## Embedding bridge (Go-specific)

Options, in order of preference:
1. **Hosted API (Voyage)** called directly from Go — simplest, no Python dependency.
2. **Local model via a small sidecar** (Python `sentence-transformers` or a `text-embeddings-inference` container) exposing `/embed`; Go talks HTTP.
3. ONNX runtime in-process — avoid unless there's time.

Keep the eval harness runnable against both an API model and a local one so the write-up can compare them.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Call resolution in a dynamic language is inherently lossy | Heuristic + per-edge confidence + unresolved-ref tracking; state limitations in README; eval structural queries against IDE ground truth |
| Eval labels go stale as the target repo changes | Pin commit SHA; store SHA in results |
| Embedding cost/latency on 50K LOC | Cache by chunk hash; batch; index once and reuse |
| Scope creep into multi-language support | Python only until eval is done |
| Overfitting fusion weights to the eval set | Tune on a dev split (~10 queries), report on held-out (~20) |

## Definition of done

- Indexes a real 10K–100K LOC repo end to end with one command.
- MCP server usable from Claude Code with all five core tools.
- Eval table: precision@5 / recall@5 for hybrid vs. BM25 vs. vector vs. grep on ≥20 hand-labeled queries, reproducible via `cornifer eval`.
- README with diagram, limitations, and demo.
- Resume line backed by real numbers:

  > Built a repository intelligence engine — AST parsing, symbol resolution, and call-graph analysis over a ~N K-LOC codebase, combined with hybrid BM25/embedding/graph retrieval (RRF fusion) — measured X% precision@5 vs. Y% for an embedding-only baseline on a 30-query hand-labeled eval set; exposed via MCP.
