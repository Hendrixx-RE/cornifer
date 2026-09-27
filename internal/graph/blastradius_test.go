package graph

import (
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// TestBlastRadius builds: module C imports module B, module B imports
// module A; separately, function f (in C) calls g (in B). Changing A
// should blast-radius to B and C (via imports); changing g should
// blast-radius to f (via calls). A disconnected module D must never
// appear.
func TestBlastRadius(t *testing.T) {
	const (
		modA = 1
		modB = 2
		modC = 3
		modD = 4
		fnG  = 10
		fnF  = 11
	)
	edges := []*model.Edge{
		edge(1, modB, modA, model.EdgeKindImports, model.ConfidenceExact),
		edge(2, modC, modB, model.EdgeKindImports, model.ConfidenceExact),
		edge(3, fnF, fnG, model.EdgeKindCalls, model.ConfidenceExact),
	}
	g := Build(edges)

	got := BlastRadius(g, modA, TraversalOptions{MaxDepth: 10})
	if want := []int64{modB, modC}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("BlastRadius(modA) = %v, want %v", endsOf(got), want)
	}
	for _, p := range got {
		if p.End() == modD {
			t.Fatalf("BlastRadius(modA) reached disconnected modD")
		}
	}

	got = BlastRadius(g, fnG, TraversalOptions{MaxDepth: 10})
	if want := []int64{fnF}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("BlastRadius(fnG) = %v, want %v", endsOf(got), want)
	}

	// Path explains *why*: modC is in modA's blast radius via modC->modB->modA.
	got = BlastRadius(g, modA, TraversalOptions{MaxDepth: 10})
	var pathToC Path
	for _, p := range got {
		if p.End() == modC {
			pathToC = p
		}
	}
	wantNodes := []int64{modA, modB, modC}
	if !int64SlicesEqual(pathToC.Nodes, wantNodes) {
		t.Fatalf("path to modC = %v, want %v", pathToC.Nodes, wantNodes)
	}
}

func TestBlastRadiusDepthLimit(t *testing.T) {
	edges := []*model.Edge{
		edge(1, 2, 1, model.EdgeKindImports, model.ConfidenceExact),
		edge(2, 3, 2, model.EdgeKindImports, model.ConfidenceExact),
		edge(3, 4, 3, model.EdgeKindImports, model.ConfidenceExact),
	}
	g := Build(edges)

	got := BlastRadius(g, 1, TraversalOptions{MaxDepth: 1})
	if want := []int64{2}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("BlastRadius depth=1 = %v, want %v", endsOf(got), want)
	}
}

func TestBlastRadiusCustomKinds(t *testing.T) {
	// Restricting to Inherits only should ignore the Imports edge.
	edges := []*model.Edge{
		edge(1, 2, 1, model.EdgeKindImports, model.ConfidenceExact),
		edge(2, 3, 1, model.EdgeKindInherits, model.ConfidenceExact),
	}
	g := Build(edges)

	got := BlastRadius(g, 1, TraversalOptions{MaxDepth: 5, Kinds: []model.EdgeKind{model.EdgeKindInherits}})
	if want := []int64{3}; !int64SlicesEqual(endsOf(got), want) {
		t.Fatalf("BlastRadius kind-restricted = %v, want %v", endsOf(got), want)
	}
}
