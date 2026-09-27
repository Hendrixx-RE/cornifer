package graph

import (
	"context"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// EdgeLoader obtains the edges to build a Graph from. Implementations
// typically wrap a store (e.g. Postgres) query scoped to a single repo;
// this package deliberately does not import internal/store so that it has
// no compile-time dependency on it.
type EdgeLoader interface {
	LoadEdges(ctx context.Context) ([]*model.Edge, error)
}

// EdgeLoaderFunc adapts a plain function to an EdgeLoader.
type EdgeLoaderFunc func(ctx context.Context) ([]*model.Edge, error)

// LoadEdges implements EdgeLoader.
func (f EdgeLoaderFunc) LoadEdges(ctx context.Context) ([]*model.Edge, error) {
	return f(ctx)
}

// Graph is an in-memory, bidirectional adjacency structure over
// model.Edge. It is immutable after construction and safe for concurrent
// reads from multiple goroutines (it performs no writes after Build/Load
// return).
type Graph struct {
	// forward maps a source symbol ID to the edges leading out of it.
	forward map[int64][]*model.Edge
	// reverse maps a destination symbol ID to the edges leading into it.
	reverse map[int64][]*model.Edge
	// nodes is the set of every symbol ID that appears as a src or dst of
	// at least one edge.
	nodes map[int64]struct{}
}

// Build constructs a Graph from edges in O(E) time and space. Nil entries
// in edges are skipped. Multi-edges (repeated src/dst/kind) are preserved
// as-is; callers that want deduplication should do so before calling Build.
func Build(edges []*model.Edge) *Graph {
	g := &Graph{
		forward: make(map[int64][]*model.Edge, len(edges)),
		reverse: make(map[int64][]*model.Edge, len(edges)),
		nodes:   make(map[int64]struct{}, 2*len(edges)),
	}
	for _, e := range edges {
		if e == nil {
			continue
		}
		g.forward[e.SrcSymbolID] = append(g.forward[e.SrcSymbolID], e)
		g.reverse[e.DstSymbolID] = append(g.reverse[e.DstSymbolID], e)
		g.nodes[e.SrcSymbolID] = struct{}{}
		g.nodes[e.DstSymbolID] = struct{}{}
	}
	return g
}

// Load fetches edges from loader and builds a Graph from them.
func Load(ctx context.Context, loader EdgeLoader) (*Graph, error) {
	edges, err := loader.LoadEdges(ctx)
	if err != nil {
		return nil, err
	}
	return Build(edges), nil
}

// NodeCount returns the number of distinct symbol IDs referenced by any
// edge in the graph.
func (g *Graph) NodeCount() int {
	return len(g.nodes)
}

// HasNode reports whether id appears as the source or destination of at
// least one edge.
func (g *Graph) HasNode(id int64) bool {
	_, ok := g.nodes[id]
	return ok
}

// Out returns the edges whose source is id, in the order they were
// supplied to Build. The returned slice must not be mutated.
func (g *Graph) Out(id int64) []*model.Edge {
	return g.forward[id]
}

// In returns the edges whose destination is id, in the order they were
// supplied to Build. The returned slice must not be mutated.
func (g *Graph) In(id int64) []*model.Edge {
	return g.reverse[id]
}
