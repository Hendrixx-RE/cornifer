# Structural-label verification

`eval/verification/fastapi-pyright-lsp-40e33e492.json` records an independent
LSP pass for the seven structural labels in `eval/queries.yaml`.

- Tool: Pyright language server 1.1.412.
- Source: a local sparse checkout of `fastapi/fastapi` at
  `40e33e492dbf4af6172997f4e3238a32e56cbe26` containing `fastapi/`.
- Method: request `textDocument/definition` and
  `textDocument/references` with `includeDeclaration=true` at each labelled
  declaration. Every definition response and every reference set contains the
  labelled source location.

This is enough to call those seven declaration labels IDE-verified. It is not
a claim that Pyright and Cornifer resolve every dynamic Python call the same
way: Cornifer still reports heuristic edge confidence and unresolved
references, while Pyright's reference counts are type-analysis results. The
semantic and identifier labels have no comparable IDE check and remain
source-verified.
