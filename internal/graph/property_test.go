package graph

import (
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// naiveClosure computes the reverse transitive closure of start over edges
// restricted to allowed kinds, up to maxDepth hops, by brute-force
// level-by-level set expansion instead of the BFS-with-visited-set
// approach BlastRadius uses. It is deliberately simple (and quadratic-ish)
// so it acts as an independent reference oracle.
func naiveClosure(edges []*model.Edge, start int64, allowedKinds map[model.EdgeKind]bool, maxDepth int) map[int64]bool {
	reached := map[int64]bool{}
	if maxDepth <= 0 {
		return reached
	}
	frontier := map[int64]bool{start: true}
	visited := map[int64]bool{start: true}
	for depth := 0; depth < maxDepth; depth++ {
		next := map[int64]bool{}
		for _, e := range edges {
			if !allowedKinds[e.Kind] {
				continue
			}
			if !frontier[e.DstSymbolID] {
				continue
			}
			if visited[e.SrcSymbolID] {
				continue
			}
			next[e.SrcSymbolID] = true
		}
		if len(next) == 0 {
			break
		}
		for n := range next {
			visited[n] = true
			reached[n] = true
		}
		frontier = next
	}
	return reached
}

func genRandomEdges(rng *rand.Rand, numNodes, numEdges int, kinds []model.EdgeKind) []*model.Edge {
	edges := make([]*model.Edge, 0, numEdges)
	for i := 0; i < numEdges; i++ {
		src := int64(rng.Intn(numNodes))
		dst := int64(rng.Intn(numNodes))
		kind := kinds[rng.Intn(len(kinds))]
		edges = append(edges, edge(int64(i), src, dst, kind, model.ConfidenceExact))
	}
	return edges
}

// TestBlastRadiusMatchesNaiveReference property-tests BlastRadius against
// naiveClosure across many random small graphs (including ones dense with
// cycles and self-loops, since numNodes is small relative to numEdges),
// checking that the *set* of reached nodes agrees exactly.
func TestBlastRadiusMatchesNaiveReference(t *testing.T) {
	kinds := []model.EdgeKind{model.EdgeKindImports, model.EdgeKindCalls}
	allowed := map[model.EdgeKind]bool{model.EdgeKindImports: true, model.EdgeKindCalls: true}

	f := func(seed int64, nodesSeed, edgesSeed uint8, startSeed uint8, depthSeed uint8) bool {
		rng := rand.New(rand.NewSource(seed))
		numNodes := int(nodesSeed%12) + 2 // 2..13 nodes
		numEdges := int(edgesSeed%40) + 1 // 1..40 edges (dense: guarantees cycles)
		start := int64(int(startSeed) % numNodes)
		maxDepth := int(depthSeed%6) + 1 // 1..6

		edges := genRandomEdges(rng, numNodes, numEdges, kinds)
		g := Build(edges)

		got := BlastRadius(g, start, TraversalOptions{MaxDepth: maxDepth})
		gotSet := map[int64]bool{}
		for _, p := range got {
			gotSet[p.End()] = true
		}

		want := naiveClosure(edges, start, allowed, maxDepth)

		if len(gotSet) != len(want) {
			return false
		}
		for n := range want {
			if !gotSet[n] {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatalf("BlastRadius diverged from naive reference: %v", err)
	}
}

// TestBlastRadiusPathsAreConsistent checks a structural invariant that
// holds regardless of graph shape: every Path returned starts at the
// queried symbol, its length matches its Depth(), consecutive nodes are
// actually connected by the stated edge in the stated direction, and no
// path revisits a node (BlastRadius must be cycle-safe).
func TestBlastRadiusPathsAreConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	kinds := []model.EdgeKind{model.EdgeKindImports, model.EdgeKindCalls}

	for trial := 0; trial < 200; trial++ {
		numNodes := rng.Intn(10) + 2
		numEdges := rng.Intn(30) + 1
		edges := genRandomEdges(rng, numNodes, numEdges, kinds)
		g := Build(edges)
		start := int64(rng.Intn(numNodes))

		for _, p := range BlastRadius(g, start, TraversalOptions{MaxDepth: 5}) {
			if p.Nodes[0] != start {
				t.Fatalf("path %v does not start at %d", p.Nodes, start)
			}
			if len(p.Edges) != p.Depth() || len(p.Nodes) != len(p.Edges)+1 {
				t.Fatalf("path %v has inconsistent node/edge lengths", p.Nodes)
			}
			seen := map[int64]bool{}
			for _, n := range p.Nodes {
				if seen[n] {
					t.Fatalf("path %v revisits node %d", p.Nodes, n)
				}
				seen[n] = true
			}
			for i, e := range p.Edges {
				// BlastRadius walks reverse edges: node[i] is the edge's
				// Dst, node[i+1] is the edge's Src.
				if e.DstSymbolID != p.Nodes[i] || e.SrcSymbolID != p.Nodes[i+1] {
					t.Fatalf("path %v edge %d (%d->%d) does not connect the stated nodes", p.Nodes, i, e.SrcSymbolID, e.DstSymbolID)
				}
			}
		}
	}
}
