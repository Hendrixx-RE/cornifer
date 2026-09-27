// Package graph loads model.Edge rows into an in-memory, bidirectional
// adjacency structure and answers the structural queries built on top of
// it: callers/callees with a depth limit (including "callers through
// interfaces" via EdgeKindImplements), blast radius (reverse transitive
// closure over import/call edges, returned as paths), and import-cycle
// detection (Tarjan SCC with a shortest-cycle witness per SCC), per
// plan.md "graph queries".
//
// This package depends on internal/model only. It does not import
// internal/store directly — callers obtain edges however they like (e.g.
// from Postgres) and either call Build with an already-loaded []*model.Edge
// slice, or implement the small EdgeLoader interface here and call Load.
// This keeps internal/graph buildable and testable with no database
// running, and avoids a compile-time coupling to the store package while
// it is developed in parallel.
//
// # Memory characteristics
//
// Build is O(E) in time and space: each edge is appended to exactly two
// slices (forward adjacency keyed by source symbol, reverse adjacency keyed
// by destination symbol) as a pointer to the same underlying model.Edge, so
// edges are never copied. For a ~100K-LOC Python repository, a rough
// order-of-magnitude estimate: tens of thousands of symbols and on the
// order of a few hundred thousand edges (calls dominate, then imports,
// inherits, implements). Each model.Edge is a small fixed-size struct
// (two int64 IDs, a short string-backed EdgeKind, a float64 confidence,
// ~48 bytes), allocated once; each adjacency slot costs one 8-byte pointer
// plus Go's slice/map overhead. In practice this keeps a full-repo graph
// comfortably within tens of megabytes of resident memory — small enough to
// rebuild from scratch on every reindex rather than maintaining it
// incrementally.
package graph
