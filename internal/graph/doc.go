// Package graph loads model.Edge rows into an in-memory, bidirectional
// adjacency structure and answers the structural queries built on top of
// it: callers/callees with a depth limit, blast radius (reverse transitive
// closure over import/call edges), and import-cycle detection (Tarjan
// SCC), each per plan.md "graph queries". It depends on internal/model and
// internal/store (to load edges) but not on internal/resolve directly — it
// consumes already-resolved edges, it does not resolve references itself.
package graph
