// Package resolve turns raw imports and call sites found by internal/symbols
// into model.Edge rows: module-level import resolution (mapping file paths
// to dotted module names), name+scope+import-binding call resolution, and
// class-hierarchy-aware method resolution (so a call to a base method also
// links to overriding subclasses). Resolution here is heuristic, not type
// inference — every edge it produces carries a model.Confidence, and every
// reference it cannot bind becomes a model.UnresolvedRef rather than being
// dropped. It depends on internal/model and reads symbols via
// internal/store; internal/graph is the consumer that turns its edges into
// a traversable graph.
package resolve
