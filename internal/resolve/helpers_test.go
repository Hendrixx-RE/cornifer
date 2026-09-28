package resolve

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
	"github.com/Hendrixx-RE/cornifer/internal/symbols"
)

type fixture struct {
	out   *Output
	qname map[int64]string
	id    map[string]int64
}

// buildFiles parses and extracts an in-memory repo (path -> source) and runs
// Resolve on it, remapping each file's temporary symbol IDs to repo-unique
// ones the way the store would.
func buildFiles(t *testing.T, srcs map[string]string) *fixture {
	t.Helper()
	paths := make([]string, 0, len(srcs))
	for p := range srcs {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	fx := &fixture{qname: map[int64]string{}, id: map[string]int64{}}
	var inputs []FileInput
	for i, p := range paths {
		res, err := parse.New().Parse(context.Background(), p, []byte(srcs[p]))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		t.Cleanup(res.Tree.Close)
		mod := ModuleNameFromPath(p)
		syms := symbols.Extract(res, int64(i+1), mod)
		off := int64(i+1) * 10000
		for _, s := range syms {
			s.ID += off
			if s.ParentID != nil {
				pid := *s.ParentID + off
				s.ParentID = &pid
			}
			fx.qname[s.ID] = s.QualifiedName
			fx.id[s.QualifiedName] = s.ID
		}
		inputs = append(inputs, FileInput{
			File:    &model.File{ID: int64(i + 1), Path: p, Language: "python", ModuleName: mod},
			Symbols: syms,
			Result:  res,
		})
	}
	out, err := Resolve(inputs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	fx.out = out
	for _, e := range out.Edges {
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Errorf("edge %v has confidence %v outside (0,1]", e, e.Confidence)
		}
	}
	return fx
}

func (fx *fixture) edges(kind model.EdgeKind) map[string]model.Confidence {
	m := map[string]model.Confidence{}
	for _, e := range fx.out.Edges {
		if e.Kind == kind {
			m[fx.qname[e.SrcSymbolID]+" -> "+fx.qname[e.DstSymbolID]] = e.Confidence
		}
	}
	return m
}

func (fx *fixture) wantEdge(t *testing.T, kind model.EdgeKind, src, dst string, conf model.Confidence) {
	t.Helper()
	got, ok := fx.edges(kind)[src+" -> "+dst]
	if !ok {
		t.Errorf("missing %s edge %s -> %s; have:\n%s", kind, src, dst, fx.dump(kind))
		return
	}
	if got != conf {
		t.Errorf("%s edge %s -> %s confidence = %v, want %v", kind, src, dst, got, conf)
	}
}

func (fx *fixture) noEdge(t *testing.T, kind model.EdgeKind, src, dst string) {
	t.Helper()
	if c, ok := fx.edges(kind)[src+" -> "+dst]; ok {
		t.Errorf("unexpected %s edge %s -> %s (confidence %v)", kind, src, dst, c)
	}
}

func (fx *fixture) dump(kind model.EdgeKind) string {
	var lines []string
	for k, c := range fx.edges(kind) {
		lines = append(lines, fmt.Sprintf("  %s (%v)", k, c))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// wantUnresolved asserts a row exists for (src, name, kind) and that its
// external flag matches.
func (fx *fixture) wantUnresolved(t *testing.T, kind model.EdgeKind, src, name string, external bool) {
	t.Helper()
	for _, u := range fx.out.UnresolvedRefs {
		if u.Kind == kind && fx.qname[u.SrcSymbolID] == src && u.Name == name {
			if fx.out.IsExternal(u) != external {
				t.Errorf("unresolved %s %s in %s: external = %v, want %v", kind, name, src, !external, external)
			}
			return
		}
	}
	t.Errorf("missing unresolved %s ref %q in %s", kind, name, src)
}
