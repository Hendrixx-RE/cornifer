// Package chunk splits a parsed, symbol-extracted file into AST-aware
// retrieval units (model.Chunk). It runs after internal/symbols has already
// produced the file's Symbols (chunk boundaries follow symbol boundaries —
// one chunk per function/method/class, module-level code gets its own
// chunk) and before internal/embed turns chunk text into vectors. It does
// not parse source itself (internal/parse) and does not call any embedding
// model (internal/embed).
package chunk
