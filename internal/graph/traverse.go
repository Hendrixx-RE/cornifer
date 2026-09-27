package graph

import "github.com/Hendrixx-RE/cornifer/internal/model"

// TraversalOptions bounds and filters a Callees, Callers, or BlastRadius
// traversal.
type TraversalOptions struct {
	// Kinds restricts which edge kinds may be followed. An empty (or nil)
	// slice allows every edge kind. Callers and BlastRadius additionally
	// document their own defaults/exceptions to this rule; see their
	// doc comments.
	Kinds []model.EdgeKind

	// MinConfidence excludes edges with Confidence below this threshold,
	// so callers can drop low-confidence heuristic resolutions. Zero (the
	// default) admits every edge, since model.Confidence never goes below
	// zero.
	MinConfidence model.Confidence

	// MaxDepth bounds the number of edge hops from the start symbol.
	// MaxDepth <= 0 yields no traversal (an empty result).
	MaxDepth int
}

func (o TraversalOptions) allowsKind(k model.EdgeKind) bool {
	if len(o.Kinds) == 0 {
		return true
	}
	for _, want := range o.Kinds {
		if want == k {
			return true
		}
	}
	return false
}

// Path is a witness for why a symbol was reached by a traversal: the
// sequence of symbol IDs visited (Nodes[0] is always the traversal's start
// symbol) and the edge taken between each consecutive pair
// (len(Edges) == len(Nodes)-1). Edges are stored exactly as they appear in
// the graph — for a reverse traversal (Callers, BlastRadius) an edge's
// Src/Dst still reflect the original call/import direction, not the
// direction it was walked in.
type Path struct {
	Nodes []int64
	Edges []*model.Edge
}

// End returns the terminal symbol ID of the path, i.e. the reached symbol.
func (p Path) End() int64 {
	return p.Nodes[len(p.Nodes)-1]
}

// Depth returns the number of edge hops in the path.
func (p Path) Depth() int {
	return len(p.Edges)
}

// adjacency selects a node's outgoing edges for a traversal direction, and
// the neighbor symbol ID reached by following a given edge in that
// direction.
type adjacency struct {
	edgesFrom func(g *Graph, id int64) []*model.Edge
	neighbor  func(e *model.Edge) int64
}

var forwardAdjacency = adjacency{
	edgesFrom: (*Graph).Out,
	neighbor:  func(e *model.Edge) int64 { return e.DstSymbolID },
}

var reverseAdjacency = adjacency{
	edgesFrom: (*Graph).In,
	neighbor:  func(e *model.Edge) int64 { return e.SrcSymbolID },
}

// bfs performs a breadth-first, cycle-safe traversal from start following
// adj, keeping edges for which allow(kind) && confidence >= minConfidence,
// up to maxDepth hops. It returns one Path per distinct reached node — the
// shortest (by hop count) — never revisiting a node once reached, which
// makes it safe on graphs with cycles, self-loops, and multi-edges.
func bfs(g *Graph, adj adjacency, start int64, maxDepth int, minConfidence model.Confidence, allow func(model.EdgeKind) bool) []Path {
	if maxDepth <= 0 {
		return nil
	}

	visited := map[int64]bool{start: true}
	queue := []Path{{Nodes: []int64{start}}}
	var results []Path

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.Depth() >= maxDepth {
			continue
		}
		curNode := cur.End()
		for _, e := range adj.edgesFrom(g, curNode) {
			if e.Confidence < minConfidence {
				continue
			}
			if !allow(e.Kind) {
				continue
			}
			next := adj.neighbor(e)
			if visited[next] {
				continue
			}
			visited[next] = true

			nodes := make([]int64, len(cur.Nodes)+1)
			copy(nodes, cur.Nodes)
			nodes[len(cur.Nodes)] = next

			edges := make([]*model.Edge, len(cur.Edges)+1)
			copy(edges, cur.Edges)
			edges[len(cur.Edges)] = e

			p := Path{Nodes: nodes, Edges: edges}
			results = append(results, p)
			queue = append(queue, p)
		}
	}
	return results
}

// Callees returns every symbol reachable from symbolID by following
// forward edges (symbolID calls/imports/... the reached symbol), up to
// opts.MaxDepth hops, filtered by opts.Kinds and opts.MinConfidence. One
// path (the shortest) is returned per reachable symbol.
func Callees(g *Graph, symbolID int64, opts TraversalOptions) []Path {
	return bfs(g, forwardAdjacency, symbolID, opts.MaxDepth, opts.MinConfidence, opts.allowsKind)
}

// Callers returns every symbol that (transitively, up to opts.MaxDepth
// hops) calls symbolID, filtered by opts.Kinds and opts.MinConfidence. One
// path (the shortest) is returned per reachable symbol.
//
// EdgeKindImplements is always followed in addition to opts.Kinds,
// regardless of the filter: an overriding method's EdgeKindImplements edge
// points at the method it overrides, so walking it in reverse steps from a
// base method to its overrides. Without this, "find callers of
// Base.method" would miss every call site that only ever calls a subclass
// override — the "callers through interfaces" query plan.md leads with.
// Reached symbols connected only via Implements hops are included in the
// result like any other reached node (their Path shows exactly that), so
// callers can distinguish "is an override of" from "calls" by inspecting
// Path.Edges.
func Callers(g *Graph, symbolID int64, opts TraversalOptions) []Path {
	allow := func(k model.EdgeKind) bool {
		return opts.allowsKind(k) || k == model.EdgeKindImplements
	}
	return bfs(g, reverseAdjacency, symbolID, opts.MaxDepth, opts.MinConfidence, allow)
}
