package graph

import "github.com/Hendrixx-RE/cornifer/internal/model"

// defaultCycleKinds is the edge-kind set FindCycles uses when opts.Kinds
// is empty: import cycles are the "is there a circular dependency between
// these modules?" question plan.md leads with.
var defaultCycleKinds = []model.EdgeKind{model.EdgeKindImports}

// SCC is one strongly connected component of size > 1, or a single node
// with a self-loop — i.e. a component that actually contains a cycle.
// Trivial single-node components with no self-loop are not cycles and are
// never reported.
type SCC struct {
	// Nodes is every symbol ID in the component, in Tarjan discovery
	// order.
	Nodes []int64

	// ShortestCycle is the shortest cycle contained in the component, as a
	// closed walk of symbol IDs: ShortestCycle[0] == ShortestCycle[len-1],
	// and each consecutive pair is connected by an edge that was part of
	// this traversal's edge set. It always has length >= 2 (a self-loop
	// reports [n, n]).
	//
	// SCC membership alone does not tell you this: a component can be
	// strongly connected only through a long detour, so ShortestCycle is
	// computed separately via a BFS shortest-path search from each node in
	// the component back to itself, not read off the SCC structure.
	ShortestCycle []int64
}

// FindCycles finds every import cycle (or, with opts.Kinds set, every
// cycle over the given edge kinds) via Tarjan's strongly-connected-
// components algorithm, filtered by opts.MinConfidence. opts.MaxDepth is
// ignored: SCCs and their shortest cycles are computed over the whole
// (filtered) graph, not bounded by hop count from a start symbol.
func FindCycles(g *Graph, opts TraversalOptions) []SCC {
	kinds := opts.Kinds
	if len(kinds) == 0 {
		kinds = defaultCycleKinds
	}
	filter := TraversalOptions{Kinds: kinds, MinConfidence: opts.MinConfidence}

	adj := buildFilteredAdjacency(g, filter)
	comps := tarjanSCCs(adj)

	var out []SCC
	for _, nodes := range comps {
		if len(nodes) == 1 {
			n := nodes[0]
			if !hasSelfLoop(adj, n) {
				continue
			}
			out = append(out, SCC{Nodes: nodes, ShortestCycle: []int64{n, n}})
			continue
		}
		out = append(out, SCC{Nodes: nodes, ShortestCycle: shortestCycleIn(adj, nodes)})
	}
	return out
}

// buildFilteredAdjacency collapses the graph's edges (restricted by
// opts.Kinds/MinConfidence) into deduplicated forward neighbor lists,
// suitable for SCC/cycle algorithms that only care about reachability, not
// per-edge metadata.
func buildFilteredAdjacency(g *Graph, opts TraversalOptions) map[int64][]int64 {
	adj := make(map[int64][]int64)
	seen := make(map[[2]int64]bool)
	for src, edges := range g.forward {
		for _, e := range edges {
			if e.Confidence < opts.MinConfidence || !opts.allowsKind(e.Kind) {
				continue
			}
			key := [2]int64{src, e.DstSymbolID}
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, ok := adj[src]; !ok {
				adj[src] = nil
			}
			adj[src] = append(adj[src], e.DstSymbolID)
			if _, ok := adj[e.DstSymbolID]; !ok {
				adj[e.DstSymbolID] = nil
			}
		}
	}
	return adj
}

func hasSelfLoop(adj map[int64][]int64, n int64) bool {
	for _, v := range adj[n] {
		if v == n {
			return true
		}
	}
	return false
}

// tarjanSCCs computes strongly connected components with an iterative
// (explicit-stack) implementation of Tarjan's algorithm, so it does not
// blow the goroutine stack on the long import chains a 100K-LOC repo can
// produce. Components are returned in discovery order.
func tarjanSCCs(adj map[int64][]int64) [][]int64 {
	nodes := make([]int64, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}

	index := make(map[int64]int, len(nodes))
	lowlink := make(map[int64]int, len(nodes))
	onStack := make(map[int64]bool, len(nodes))
	var stack []int64
	var sccs [][]int64
	counter := 0

	type frame struct {
		node    int64
		i       int // next child index in adj[node] to visit
		hasNext bool
	}

	for _, root := range nodes {
		if _, ok := index[root]; ok {
			continue
		}

		var work []*frame
		work = append(work, &frame{node: root})
		index[root] = counter
		lowlink[root] = counter
		counter++
		stack = append(stack, root)
		onStack[root] = true

		for len(work) > 0 {
			top := work[len(work)-1]
			children := adj[top.node]
			if top.i < len(children) {
				child := children[top.i]
				top.i++
				if _, ok := index[child]; !ok {
					index[child] = counter
					lowlink[child] = counter
					counter++
					stack = append(stack, child)
					onStack[child] = true
					work = append(work, &frame{node: child})
				} else if onStack[child] {
					if index[child] < lowlink[top.node] {
						lowlink[top.node] = index[child]
					}
				}
				continue
			}

			// Done with top.node's children: pop it, propagate lowlink to
			// the parent frame, and if it's a root, emit its SCC.
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1]
				if lowlink[top.node] < lowlink[parent.node] {
					lowlink[parent.node] = lowlink[top.node]
				}
			}
			if lowlink[top.node] == index[top.node] {
				var comp []int64
				for {
					n := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[n] = false
					comp = append(comp, n)
					if n == top.node {
						break
					}
				}
				sccs = append(sccs, comp)
			}
		}
	}
	return sccs
}

// shortestCycleIn returns the shortest cycle that stays entirely within
// the given SCC's node set: for every node n and every edge n->v inside
// the component, the shortest path v->...->n inside the component gives a
// candidate cycle n->v->...->n; the shortest such candidate over every
// (n, v) pair is the answer. This is a real search (BFS per candidate),
// not a read of the SCC membership list, because a component's minimum
// cycle length is not derivable from its size alone.
func shortestCycleIn(adj map[int64][]int64, nodes []int64) []int64 {
	inComp := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		inComp[n] = true
	}

	var best []int64
	for _, n := range nodes {
		for _, v := range adj[n] {
			if !inComp[v] {
				continue
			}
			if v == n {
				return []int64{n, n} // self-loop: minimum possible cycle length
			}
			path := shortestPathWithin(adj, inComp, v, n)
			if path == nil {
				continue
			}
			full := append([]int64{n}, path...)
			if best == nil || len(full) < len(best) {
				best = full
			}
		}
	}
	return best
}

// shortestPathWithin does a plain BFS from src to dst using only nodes in
// allowed, returning the path (src..dst inclusive) or nil if unreachable.
func shortestPathWithin(adj map[int64][]int64, allowed map[int64]bool, src, dst int64) []int64 {
	if src == dst {
		return []int64{src}
	}
	visited := map[int64]bool{src: true}
	prev := map[int64]int64{}
	queue := []int64{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if !allowed[next] || visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			if next == dst {
				path := []int64{dst}
				for at := cur; ; at = prev[at] {
					path = append([]int64{at}, path...)
					if at == src {
						break
					}
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}
