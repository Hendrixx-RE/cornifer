package symbols

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

// goldenCases lists every fixture under testdata/<name>.py, golden output in
// testdata/<name>.golden. Run `go test ./internal/symbols/... -update` to
// regenerate golden files after an intentional behavior change.
var goldenCases = []string{
	"nested_classes",
	"decorators",
	"async_defs",
	"lambdas",
	"multiline_signature",
	"class_attributes",
	"properties",
	"overloads",
	"nested_defs",
}

func TestGolden(t *testing.T) {
	for _, name := range goldenCases {
		name := name
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join("testdata", name+".py")
			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			res, err := parse.New().Parse(context.Background(), srcPath, src)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			defer res.Tree.Close()
			if len(res.Errors) != 0 {
				t.Fatalf("Parse() errors = %v, want none for a valid fixture", res.Errors)
			}

			syms := Extract(res, 1, "m")
			got := formatSymbols(syms)

			goldenPath := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("golden mismatch for %s.\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
			}
		})
	}
}

// formatSymbols renders syms as stable, human-readable text: one block per
// symbol including its temporary ID/ParentID (see doc.go), kind, qualified
// name, line span, signature, and docstring. Extraction order is
// deterministic (module first, depth-first by lexical nesting) so this text
// is stable across runs, which is what makes it usable as a golden file.
func formatSymbols(syms []*model.Symbol) string {
	var b strings.Builder
	for _, s := range syms {
		parent := "-"
		if s.ParentID != nil {
			parent = fmt.Sprintf("%d", *s.ParentID)
		}
		fmt.Fprintf(&b, "[%d] %s %q parent=%s lines=%d-%d\n", s.ID, s.Kind, s.QualifiedName, parent, s.StartLine, s.EndLine)
		if s.Signature != "" {
			fmt.Fprintf(&b, "    sig: %s\n", indentContinuation(s.Signature))
		}
		if s.Docstring != "" {
			fmt.Fprintf(&b, "    doc: %s\n", indentContinuation(s.Docstring))
		}
	}
	return b.String()
}

// indentContinuation joins a possibly multi-line value onto a single
// "field: " line by replacing embedded newlines with a visible marker, so
// each symbol's golden block stays exactly one line per field regardless of
// multi-line signatures/docstrings.
func indentContinuation(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

// TestNestedQualifiedNames asserts the documented "<locals>" scheme (doc.go)
// independently of the golden file: names, kinds, and ParentID nesting.
func TestNestedQualifiedNames(t *testing.T) {
	src := []byte("def a():\n    def b():\n        def c(): pass\n    class D:\n        def e(self): pass\n    x = 1\n")
	res, err := parse.New().Parse(context.Background(), "m.py", src)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Tree.Close()

	byQN := map[string]*model.Symbol{}
	for _, s := range Extract(res, 1, "m") {
		byQN[s.QualifiedName] = s
	}
	cases := []struct {
		qn     string
		kind   model.SymbolKind
		parent string
	}{
		{"m.a", model.SymbolKindFunction, "m"},
		{"m.a.<locals>.b", model.SymbolKindFunction, "m.a"},
		{"m.a.<locals>.b.<locals>.c", model.SymbolKindFunction, "m.a.<locals>.b"},
		{"m.a.<locals>.D", model.SymbolKindClass, "m.a"},
		{"m.a.<locals>.D.e", model.SymbolKindMethod, "m.a.<locals>.D"},
	}
	for _, c := range cases {
		s := byQN[c.qn]
		if s == nil {
			t.Errorf("missing symbol %q", c.qn)
			continue
		}
		if s.Kind != c.kind {
			t.Errorf("%s kind = %s, want %s", c.qn, s.Kind, c.kind)
		}
		if s.ParentID == nil || *s.ParentID != byQN[c.parent].ID {
			t.Errorf("%s ParentID = %v, want ID of %s", c.qn, s.ParentID, c.parent)
		}
	}
	if _, ok := byQN["m.a.<locals>.x"]; ok {
		t.Error("local variable x must not be a symbol")
	}
}
