package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func edge(id, src, dst int64, kind model.EdgeKind, conf model.Confidence) *model.Edge {
	return &model.Edge{ID: id, SrcSymbolID: src, DstSymbolID: dst, Kind: kind, Confidence: conf}
}

func TestBuild(t *testing.T) {
	edges := []*model.Edge{
		nil, // must be skipped, not panic
		edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact),
		edge(2, 2, 3, model.EdgeKindCalls, model.ConfidenceHigh),
		edge(3, 1, 1, model.EdgeKindCalls, model.ConfidenceExact), // self-loop
	}
	g := Build(edges)

	if g.NodeCount() != 3 {
		t.Fatalf("NodeCount() = %d, want 3", g.NodeCount())
	}
	if !g.HasNode(1) || !g.HasNode(2) || !g.HasNode(3) {
		t.Fatalf("expected nodes 1,2,3 present")
	}
	if g.HasNode(99) {
		t.Fatalf("HasNode(99) = true, want false")
	}

	out1 := g.Out(1)
	if len(out1) != 2 {
		t.Fatalf("Out(1) len = %d, want 2 (self-loop + call to 2)", len(out1))
	}
	in3 := g.In(3)
	if len(in3) != 1 || in3[0].SrcSymbolID != 2 {
		t.Fatalf("In(3) = %+v, want single edge from 2", in3)
	}
	in2 := g.In(2)
	if len(in2) != 1 || in2[0].SrcSymbolID != 1 {
		t.Fatalf("In(2) = %+v, want single edge from 1", in2)
	}
}

func TestLoad(t *testing.T) {
	want := []*model.Edge{edge(1, 1, 2, model.EdgeKindCalls, model.ConfidenceExact)}
	loader := EdgeLoaderFunc(func(ctx context.Context) ([]*model.Edge, error) {
		return want, nil
	})
	g, err := Load(context.Background(), loader)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if g.NodeCount() != 2 {
		t.Fatalf("NodeCount() = %d, want 2", g.NodeCount())
	}

	loaderErr := EdgeLoaderFunc(func(ctx context.Context) ([]*model.Edge, error) {
		return nil, errors.New("boom")
	})
	if _, err := Load(context.Background(), loaderErr); err == nil {
		t.Fatalf("Load() error = nil, want error propagated from loader")
	}
}
