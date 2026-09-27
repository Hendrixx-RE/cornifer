// Package store defines the Store interface: all Postgres access for
// Cornifer's structural and semantic index (repos, files, symbols, edges,
// unresolved_refs, chunks, and pgvector similarity search over chunks). It
// is the only package later waves should use to talk to Postgres directly
// — internal/resolve, internal/chunk, internal/embed, internal/bm25, and
// internal/retrieve all go through Store rather than opening their own
// pgx connections. Migrations under migrations/ define the schema this
// package's implementation reads and writes; see migrations/doc.go for the
// schema-ownership boundary between the two packages.
package store
