# Pinned FastAPI source-package evaluation

Raw ranks and metadata are in
[`eval/results/fastapi-40e33e492db-jina-code-source-128.json`](../eval/results/fastapi-40e33e492db-jina-code-source-128.json).
The target is `fastapi/fastapi` commit
`40e33e492dbf4af6172997f4e3238a32e56cbe26`. The index is deliberately a
sparse checkout of the repository's `fastapi/` package: 44 Python files, 716
symbols, 524 resolved edges, and 402 chunks. It excludes tests, docs, and
examples. Every one of the 22 labelled spans is inside that package, but this
is still not a full-repository result.

The sidecar used the real `jinaai/jina-embeddings-v2-base-code` revision
`516f4baf13dec4ddddda8631e019b5737c8bc250` (Apache-2.0), with its `encode`
method's mean pooling, 768-dimensional output, cosine ranking, and symmetric
query/document encoding without an instruction prefix. The resource-bounded
CPU run used `max_length=128`; the raw report records this exact identity for
both index and query. It ran against an isolated PostgreSQL 16.6 / pgvector
0.8.1 database. Index time was 47.175s, including 46.426s of embedding.

All labels are source-verified direct spans at the pinned commit: 7
structural, 7 semantic, and 8 identifier. There are zero IDE-verified labels.

| System | P@5 | R@5 | MRR |
| --- | ---: | ---: | ---: |
| Hybrid with graph boost | 0.173 | 0.864 | 0.642 |
| Hybrid without graph boost | 0.164 | 0.818 | 0.678 |
| BM25 | 0.118 | 0.591 | 0.470 |
| Vector | 0.173 | 0.864 | 0.600 |
| Ripgrep baseline | 0.018 | 0.091 | 0.061 |

| System / query type | Identifier P/R/MRR | Semantic P/R/MRR | Structural P/R/MRR |
| --- | --- | --- | --- |
| Hybrid with boost | 0.200 / 1.000 / 0.775 | 0.171 / 0.857 / 0.607 | 0.143 / 0.714 / 0.524 |
| Hybrid without boost | 0.175 / 0.875 / 0.750 | 0.171 / 0.857 / 0.762 | 0.143 / 0.714 / 0.512 |
| BM25 | 0.175 / 0.875 / 0.792 | 0.114 / 0.571 / 0.381 | 0.057 / 0.286 / 0.190 |
| Vector | 0.200 / 1.000 / 0.667 | 0.171 / 0.857 / 0.576 | 0.143 / 0.714 / 0.548 |
| Ripgrep | 0.025 / 0.125 / 0.042 | 0.029 / 0.143 / 0.143 | 0.000 / 0.000 / 0.000 |

## Source-grounded failure analysis

- Hybrid and vector both missed `FastAPI.add_api_route` at
  `fastapi/applications.py:1056`, `get_request_handler` at
  `fastapi/routing.py:217`, and `get_openapi` at
  `fastapi/openapi/utils.py:456`. Their top hits are source-near related
  implementations (for example `APIRouter.include_router` at routing.py:1120
  and FastAPI setup/openapi helper chunks), rather than the labelled span.
  This is retrieval ranking evidence, not an IDE reference result.
- These misses sit in long definitions. The 128-token model cap is a likely
  contributor: it can omit the later forwarding/body language that the
  natural-language label describes. `FastAPI.__init__` was also explicitly
  reported as oversized by the indexer. The result does not isolate that cause
  from embedding-model behavior, so it should guide a 512-token rerun rather
  than be treated as a definitive diagnosis.
- Boosting improves recall by recovering one extra relevant top-five hit on
  this corpus, but its lower MRR shows that the post-RRF graph stage can move a
  first relevant hit down. Keep the ablation; do not claim an unconditional
  graph-boost gain.
- Ripgrep's whole-word token baseline is intentionally weak for natural
  language and dotted identifiers here. Its low score is a baseline result,
  not evidence that source search is generally ineffective.

The next comparable evaluation should retain the same labels and model
revision, raise the documented model cap to 512 on adequate CPU/GPU capacity,
and separately index the complete checkout. It should also add actual
Pyright/Pylance reference captures before any label is called IDE-verified.
