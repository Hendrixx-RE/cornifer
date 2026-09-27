// Package parse wraps tree-sitter to turn source bytes into a syntax tree.
// It owns grammar selection (Python only, per plan.md) and per-language
// tree-sitter queries; it does not walk the tree into domain Symbols
// (internal/symbols owns that) and does not read files from disk
// (internal/walker owns that). Callers pass in a file's contents and get
// back a tree plus any parse errors, collected rather than fatal so one
// broken file never aborts an indexing run.
package parse
