package graph

import (
	"sort"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func sortedCopy(xs []int64) []int64 {
	out := append([]int64(nil), xs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func findSCC(sccs []SCC, member int64) (SCC, bool) {
	for _, s := range sccs {
		for _, n := range s.Nodes {
			if n == member {
				return s, true
			}
		}
	}
	return SCC{}, false
}

// TestFindCycles exercises a graph with a known SCC structure:
//
//   - Component A {1,2,3,4}: a 4-cycle (1->2->3->4->1) plus a shortcut
//     edge 2->1, so the SCC has 4 members but its *shortest* cycle is the
//     2-edge one (1->2->1), not the 4-edge Hamiltonian cycle you'd get by
//     just walking the SCC membership in order. This is the case that
//     distinguishes "SCC detection" from "shortest cycle within the SCC".
//   - Component B {7,8}: a plain mutual 2-cycle, in a separate component
//     from A.
//   - Node 5: a self-loop, the minimal possible cycle.
//   - Node 9: has an outgoing edge into component A but is not itself part
//     of any cycle, so it must not be reported.
func TestFindCycles(t *testing.T) {
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindImports, model.ConfidenceExact),
		edge(2, 2, 3, model.EdgeKindImports, model.ConfidenceExact),
		edge(3, 3, 4, model.EdgeKindImports, model.ConfidenceExact),
		edge(4, 4, 1, model.EdgeKindImports, model.ConfidenceExact),
		edge(5, 2, 1, model.EdgeKindImports, model.ConfidenceExact), // shortcut
		edge(6, 7, 8, model.EdgeKindImports, model.ConfidenceExact),
		edge(7, 8, 7, model.EdgeKindImports, model.ConfidenceExact),
		edge(8, 5, 5, model.EdgeKindImports, model.ConfidenceExact), // self-loop
		edge(9, 9, 1, model.EdgeKindImports, model.ConfidenceExact), // feeds into A, not cyclic itself
	}
	g := Build(edges)

	sccs := FindCycles(g, TraversalOptions{})

	compA, ok := findSCC(sccs, 1)
	if !ok {
		t.Fatalf("component A (containing node 1) not reported")
	}
	if want := []int64{1, 2, 3, 4}; !int64SlicesEqual(sortedCopy(compA.Nodes), want) {
		t.Fatalf("component A nodes = %v, want %v", sortedCopy(compA.Nodes), want)
	}
	if len(compA.ShortestCycle) != 3 {
		t.Fatalf("component A shortest cycle = %v, want length 3 (the 1<->2 shortcut, not the 4-node loop)", compA.ShortestCycle)
	}
	if compA.ShortestCycle[0] != compA.ShortestCycle[len(compA.ShortestCycle)-1] {
		t.Fatalf("component A shortest cycle %v is not closed", compA.ShortestCycle)
	}

	compB, ok := findSCC(sccs, 7)
	if !ok {
		t.Fatalf("component B (containing node 7) not reported")
	}
	if want := []int64{7, 8}; !int64SlicesEqual(sortedCopy(compB.Nodes), want) {
		t.Fatalf("component B nodes = %v, want %v", sortedCopy(compB.Nodes), want)
	}
	if len(compB.ShortestCycle) != 3 {
		t.Fatalf("component B shortest cycle = %v, want length 3", compB.ShortestCycle)
	}

	selfLoop, ok := findSCC(sccs, 5)
	if !ok {
		t.Fatalf("self-loop component (node 5) not reported")
	}
	if want := []int64{5, 5}; !int64SlicesEqual(selfLoop.ShortestCycle, want) {
		t.Fatalf("self-loop shortest cycle = %v, want %v", selfLoop.ShortestCycle, want)
	}

	if _, ok := findSCC(sccs, 9); ok {
		t.Fatalf("node 9 has no cycle through it and must not be reported")
	}

	if len(sccs) != 3 {
		t.Fatalf("FindCycles returned %d SCCs, want 3 (A, B, self-loop)", len(sccs))
	}
}

func TestFindCyclesEmptyGraph(t *testing.T) {
	g := Build(nil)
	if got := FindCycles(g, TraversalOptions{}); len(got) != 0 {
		t.Fatalf("FindCycles(empty) = %v, want none", got)
	}
}

func TestFindCyclesRespectsKindFilter(t *testing.T) {
	// A Calls-only 2-cycle must not be reported when FindCycles defaults to
	// Imports, but must be reported when Kinds explicitly includes Calls.
	edges := []*model.Edge{
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact),
		edge(2, 2, 1, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	if got := FindCycles(g, TraversalOptions{}); len(got) != 0 {
		t.Fatalf("FindCycles default (imports-only) = %v, want none for a calls-only cycle", got)
	}

	got := FindCycles(g, TraversalOptions{Kinds: []model.EdgeKind{model.EdgeKindCalls}})
	if len(got) != 1 {
		t.Fatalf("FindCycles(Kinds=Calls) = %v, want 1 SCC", got)
	}
}
