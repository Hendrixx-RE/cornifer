package graph

import (
	"sort"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func endsOf(paths []Path) []int64 {
	ends := make([]int64, len(paths))
	for i, p := range paths {
		ends[i] = p.End()
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	return ends
}

func TestCalleesDepthLimit(t *testing.T) {
	// Deep chain: 1 -> 2 -> 3 -> 4 -> 5, all Calls.
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact),
		edge(2, 2, 3, model.EdgeKindCalls, model.ConfidenceExact),
		edge(3, 3, 4, model.EdgeKindCalls, model.ConfidenceExact),
		edge(4, 4, 5, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	got := Callees(g, 1, TraversalOptions{MaxDepth: 2})
	if want := []int64{2, 3}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callees depth=2 ends = %v, want %v", endsOf(got), want)
	}

	got = Callees(g, 1, TraversalOptions{MaxDepth: 100})
	if want := []int64{2, 3, 4, 5}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callees depth=100 ends = %v, want %v", endsOf(got), want)
	}

	got = Callees(g, 1, TraversalOptions{MaxDepth: 0})
	if len(got) != 0 {
		t.Fatalf("Callees depth=0 = %v, want empty", got)
	}
}

func TestCalleesDisconnectedAndCycleSafe(t *testing.T) {
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact),
		edge(2, 2, 1, model.EdgeKindCalls, model.ConfidenceExact), // cycle back to 1
		edge(3, 100, 200, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	got := Callees(g, 1, TraversalOptions{MaxDepth: 50})
	if want := []int64{2}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callees over 2-cycle = %v, want %v (must not revisit 1 or infinite loop)", endsOf(got), want)
	}

	got = Callees(g, 1, TraversalOptions{MaxDepth: 50})
	for _, p := range got {
		if p.End() == 100 || p.End() == 200 {
			t.Fatalf("Callees(1) reached disconnected component node %d", p.End())
		}
	}
}

func TestCalleesKindAndConfidenceFilter(t *testing.T) {
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceLow),
		edge(2, 1, 3, model.EdgeKindImports, model.ConfidenceExact),
		edge(3, 1, 4, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	got := Callees(g, 1, TraversalOptions{MaxDepth: 1, Kinds: []model.EdgeKind{model.EdgeKindCalls}})
	if want := []int64{2, 4}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callees kind-filtered = %v, want %v", endsOf(got), want)
	}

	got = Callees(g, 1, TraversalOptions{MaxDepth: 1, Kinds: []model.EdgeKind{model.EdgeKindCalls}, MinConfidence: model.ConfidenceHigh})
	if want := []int64{4}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callees confidence-filtered = %v, want %v", endsOf(got), want)
	}
}

func TestCalleesMultiEdge(t *testing.T) {
	// Two distinct edges between the same pair of nodes with different
	// kinds; both should be traversable but the node is only reported once.
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact),
		edge(2, 1, 2, model.EdgeKindImports, model.ConfidenceExact),
	}
	g := Build(edges)

	got := Callees(g, 1, TraversalOptions{MaxDepth: 1})
	if len(got) != 1 {
		t.Fatalf("Callees multi-edge = %d paths, want 1 (node reported once)", len(got))
	}
}

// TestCallersThroughInterface is the headline "callers through interfaces"
// scenario from plan.md: Base.method (11) is overridden by Sub.method (12)
// via EdgeKindImplements; CallerA (21) calls Base.method directly, CallerB
// (20) only ever calls the override. Callers(Base.method) must surface
// both, even when the caller restricts Kinds to Calls only (excluding
// low-confidence heuristics is a common real filter, and must not disable
// the interface hop).
func TestCallersThroughInterface(t *testing.T) {
	const (
		baseMethod         = 11
		override           = 12
		callerA            = 21 // calls override directly
		callerB            = 20 // calls override directly (renamed for clarity below)
		directCallerOfBase = 30
	)
	edges := []*model.Edge{
		edge(1, override, baseMethod, model.EdgeKindImplements, model.ConfidenceExact),
		edge(2, callerA, override, model.EdgeKindCalls, model.ConfidenceExact),
		edge(3, directCallerOfBase, baseMethod, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	opts := TraversalOptions{Kinds: []model.EdgeKind{model.EdgeKindCalls}, MaxDepth: 2}
	got := Callers(g, baseMethod, opts)

	want := []int64{override, callerA, directCallerOfBase}
	if !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callers(base method) = %v, want %v (override + its caller + direct caller)", endsOf(got), want)
	}

	// At depth 1 only the override (1 implements-hop) and the direct
	// caller (1 calls-hop) are reachable; the transitive caller through the
	// override needs depth 2.
	got = Callers(g, baseMethod, TraversalOptions{Kinds: []model.EdgeKind{model.EdgeKindCalls}, MaxDepth: 1})
	want = []int64{override, directCallerOfBase}
	if !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("Callers(base method) depth=1 = %v, want %v", endsOf(got), want)
	}

	// Sanity: the path to callerA really does go through the override via
	// an Implements edge, then a Calls edge — not some other route.
	for _, p := range got2ForCallerA(g, baseMethod) {
		if len(p.Edges) != 2 {
			t.Fatalf("path to callerA has %d edges, want 2", len(p.Edges))
		}
		if p.Edges[0].Kind != model.EdgeKindImplements || p.Edges[1].Kind != model.EdgeKindCalls {
			t.Fatalf("path kinds = [%s, %s], want [implements, calls]", p.Edges[0].Kind, p.Edges[1].Kind)
		}
	}
}

func got2ForCallerA(g *Graph, baseMethod int64) []Path {
	all := Callers(g, baseMethod, TraversalOptions{Kinds: []model.EdgeKind{model.EdgeKindCalls}, MaxDepth: 2})
	var out []Path
	for _, p := range all {
		if p.End() == 21 {
			out = append(out, p)
		}
	}
	return out
}

func int64SlicesEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
