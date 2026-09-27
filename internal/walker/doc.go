// Package walker discovers the files a Repo indexing run should process: it
// walks the repo root respecting .gitignore, skips vendored/generated
// paths, detects each file's language, and computes its content hash
// (model.File.ContentHash) for incremental reindex. It produces model.File
// values (without IDs) and raw file bytes; it does not parse those bytes
// (internal/parse) and does not talk to Postgres (internal/store) — the
// caller (cmd/cornifer's index/reindex commands) wires walker output into
// the rest of the pipeline.
package walker
