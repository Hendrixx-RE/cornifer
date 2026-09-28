package resolve

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
	"github.com/Hendrixx-RE/cornifer/internal/symbols"
	"github.com/Hendrixx-RE/cornifer/internal/walker"
)

// TestFastAPIResolutionStats resolves the pinned FastAPI checkout (see
// TARGET_REPO; fetch with scripts/fetch-target-repo.sh) and asserts coarse
// recall floors, so a regression in resolution shows up as a number rather
// than as silently emptier graphs. Floors are deliberately below the
// observed values (logged with -v) to tolerate small heuristic changes.
func TestFastAPIResolutionStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping FastAPI stats test in -short mode")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "repos", "fastapi")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("skipping: %s not present (run scripts/fetch-target-repo.sh first): %v", root, err)
	}

	files, err := walker.Walk(context.Background(), root)
	if err != nil {
		t.Fatalf("walker.Walk: %v", err)
	}
	p := parse.New()
	var inputs []FileInput
	for i, f := range files {
		res, err := p.Parse(context.Background(), f.Path, f.Content)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Path, err)
		}
		defer res.Tree.Close()
		syms := symbols.Extract(res, int64(i+1), f.ModuleName)
		off := int64(i+1) * 100000
		for _, s := range syms {
			s.ID += off
			if s.ParentID != nil {
				pid := *s.ParentID + off
				s.ParentID = &pid
			}
		}
		file := f.File
		file.ID = int64(i + 1)
		inputs = append(inputs, FileInput{File: &file, Symbols: syms, Result: res})
	}

	out, err := Resolve(inputs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s := out.Stats
	t.Logf("files=%d edges=%d unresolved rows=%d\n%s", len(files), len(out.Edges), len(out.UnresolvedRefs), s)

	for _, e := range out.Edges {
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Fatalf("edge with confidence %v", e.Confidence)
		}
	}

	floors := map[model.EdgeKind]float64{
		model.EdgeKindImports: 0.85,
		model.EdgeKindCalls:   0.60,
	}
	for kind, floor := range floors {
		if got := s.ResolvedRatio(kind); got < floor {
			t.Errorf("%s in-repo resolved ratio = %.3f, want >= %.2f", kind, got, floor)
		}
	}
	strong := s.EdgesByConfidence[model.ConfidenceExact] + s.EdgesByConfidence[model.ConfidenceHigh]
	if strong < 3000 {
		t.Errorf("only %d Exact/High edges (of %d), want >= 3000", strong, len(out.Edges))
	}
	if s.EdgesByKind[model.EdgeKindInherits] < 20 {
		t.Errorf("only %d inherits edges", s.EdgesByKind[model.EdgeKindInherits])
	}
	if s.EdgesByKind[model.EdgeKindImplements] < 5 {
		t.Errorf("only %d implements edges", s.EdgesByKind[model.EdgeKindImplements])
	}
	if len(out.UnresolvedRefs) == 0 {
		t.Errorf("no unresolved refs recorded for a real repo")
	}
}
