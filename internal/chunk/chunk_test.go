package chunk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
	"github.com/Hendrixx-RE/cornifer/internal/symbols"
	"github.com/Hendrixx-RE/cornifer/internal/walker"
)

func run(t *testing.T, c Chunker, path, src string, extra ...*model.Symbol) ([]*model.Chunk, []*model.Symbol) {
	t.Helper()
	res, err := parse.New().Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	syms := symbols.Extract(res, 1, "m")
	res.Tree.Close()
	syms = append(syms, extra...)
	chunks, err := c.Chunk(context.Background(), &model.File{ID: 1, Path: path, ModuleName: "m"}, []byte(src), syms)
	if err != nil {
		t.Fatal(err)
	}
	return chunks, syms
}

// checkInvariants asserts the two package invariants plus contract details.
func checkInvariants(t *testing.T, name, src string, chunks []*model.Chunk, syms []*model.Symbol) {
	t.Helper()
	n := strings.Count(src, "\n")
	if src != "" && !strings.HasSuffix(src, "\n") {
		n++
	}
	covered := make([]int, n+2)
	lines := strings.Split(src, "\n")
	for _, c := range chunks {
		if c.StartLine < 1 || c.EndLine > n || c.StartLine > c.EndLine {
			t.Fatalf("%s: bad range %d-%d (n=%d)", name, c.StartLine, c.EndLine, n)
		}
		for l := c.StartLine; l <= c.EndLine; l++ {
			covered[l]++
		}
		if want := strings.Join(lines[c.StartLine-1:c.EndLine], "\n"); c.Text != want {
			t.Fatalf("%s: chunk %d-%d text is not the raw source slice", name, c.StartLine, c.EndLine)
		}
		if strings.Contains(c.Text, c.ContextHeader) && c.ContextHeader != "" && strings.HasPrefix(c.Text, c.ContextHeader) {
			t.Fatalf("%s: header baked into Text", name)
		}
		if c.TokenCount != CountTokens(EmbeddingText(c)) || c.TokenCount == 0 {
			t.Fatalf("%s: bad TokenCount", name)
		}
		if c.Embedding != nil {
			t.Fatalf("%s: embedding set", name)
		}
	}
	for l := 1; l <= n; l++ {
		if covered[l] == 0 {
			t.Fatalf("%s: line %d uncovered", name, l)
		}
	}
	for _, s := range syms {
		if s.Kind == model.SymbolKindModule {
			continue
		}
		for _, c := range chunks {
			disjoint := c.EndLine < s.StartLine || c.StartLine > s.EndLine
			inside := c.StartLine >= s.StartLine && c.EndLine <= s.EndLine
			contains := c.StartLine <= s.StartLine && c.EndLine >= s.EndLine
			if !disjoint && !inside && !contains {
				t.Fatalf("%s: chunk %d-%d crosses symbol %s %d-%d", name, c.StartLine, c.EndLine, s.QualifiedName, s.StartLine, s.EndLine)
			}
		}
	}
}

func TestEmptyFile(t *testing.T) {
	got, err := New().Chunk(context.Background(), &model.File{Path: "a.py"}, nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	chunks, syms := run(t, New(), "a.py", "")
	if len(chunks) != 0 {
		t.Fatal("expected no chunks")
	}
	checkInvariants(t, "empty", "", chunks, syms)
}

func TestBlankOnlyFile(t *testing.T) {
	src := "\n\n\n"
	chunks, syms := run(t, New(), "a.py", src)
	checkInvariants(t, "blank", src, chunks, syms)
}

func TestModuleLevelOnly(t *testing.T) {
	src := "import os\nX = 1\nfor i in range(3):\n    print(i)\n"
	chunks, syms := run(t, New(), "a.py", src)
	checkInvariants(t, "module", src, chunks, syms)
	if len(chunks) != 1 || chunks[0].SymbolID != nil {
		t.Fatalf("want one nil-SymbolID chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].ContextHeader, "file: a.py") {
		t.Fatalf("header: %q", chunks[0].ContextHeader)
	}
}

func TestOneChunkPerSymbolAndHeaderSeparate(t *testing.T) {
	src := "import os\nfrom typing import List\n\n\ndef f(x: List[int]):\n    return os.getcwd()\n\n\nclass A:\n    def m(self):\n        pass\n"
	c := NewWithOptions(Options{MergeTokens: 1})
	chunks, syms := run(t, c, "pkg/a.py", src)
	checkInvariants(t, "basic", src, chunks, syms)
	var fchunk *model.Chunk
	for _, ch := range chunks {
		if strings.Contains(ch.Text, "def f") {
			fchunk = ch
		}
	}
	if fchunk == nil || fchunk.SymbolID == nil {
		t.Fatal("no function chunk with SymbolID")
	}
	h := fchunk.ContextHeader
	for _, want := range []string{"file: pkg/a.py", "import os", "from typing import List", "signature: def f("} {
		if !strings.Contains(h, want) {
			t.Errorf("header missing %q:\n%s", want, h)
		}
	}
}

func TestLargeClassSplitsIntoHeaderAndMethods(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("class Big:\n    \"\"\"Doc.\"\"\"\n    attr = 1\n\n")
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&sb, "    def method_%d(self, value):\n        result = value * %d\n        return result + self.attr\n\n", i, i)
	}
	src := strings.TrimRight(sb.String(), "\n") + "\n"
	c := NewWithOptions(Options{ClassWholeTokens: 20, MergeTokens: 1})
	chunks, syms := run(t, c, "a.py", src)
	checkInvariants(t, "bigclass", src, chunks, syms)
	if len(chunks) != 7 {
		t.Fatalf("want header + 6 methods, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "attr = 1") || strings.Contains(chunks[0].Text, "def method_") {
		t.Fatalf("header chunk wrong: %q", chunks[0].Text)
	}
	if !strings.Contains(chunks[3].ContextHeader, "class: Big") {
		t.Fatalf("method header lacks class: %q", chunks[3].ContextHeader)
	}
}

func TestDeeplyNestedClasses(t *testing.T) {
	src := "class A:\n    class B:\n        class C:\n            class D:\n                def deep(self):\n                    return 1\n\n            def c_m(self):\n                return 2\n        def b_m(self):\n            return 3\n    def a_m(self):\n        return 4\n"
	for _, o := range []Options{{ClassWholeTokens: 1, MergeTokens: 1}, {}} {
		chunks, syms := run(t, NewWithOptions(o), "a.py", src)
		checkInvariants(t, "nested", src, chunks, syms)
	}
	c := NewWithOptions(Options{ClassWholeTokens: 1, MergeTokens: 1})
	chunks, _ := run(t, c, "a.py", src)
	var found bool
	for _, ch := range chunks {
		if strings.Contains(ch.Text, "def deep") && strings.Contains(ch.ContextHeader, "class: A.B.C.D") {
			found = true
		}
	}
	if !found {
		t.Fatal("deep method header lacks full enclosing class chain")
	}
}

func TestHugeFunctionSplitsAtStatements(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("def huge(a):\n    \"\"\"Doc.\"\"\"\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "    v%d = compute(a,\n                  %d)\n", i, i)
		if i%50 == 25 {
			fmt.Fprintf(&sb, "    if a:\n        w = 1\n        z = 2\n")
		}
	}
	sb.WriteString("    return a\n")
	src := sb.String()
	chunks, syms := run(t, New(), "a.py", src)
	checkInvariants(t, "huge", src, chunks, syms)
	if len(chunks) < 3 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for _, ch := range chunks {
		// A statement is `vN = compute(a,` + continuation: no piece may end
		// after the first line of a two-line statement, or start on its
		// continuation line.
		first := strings.SplitN(ch.Text, "\n", 2)[0]
		if strings.HasPrefix(first, "                  ") {
			t.Fatalf("chunk starts mid-statement: %q", first)
		}
		last := ch.Text[strings.LastIndex(ch.Text, "\n")+1:]
		if strings.HasSuffix(last, "compute(a,") {
			t.Fatalf("chunk ends mid-statement: %q", last)
		}
		if !strings.Contains(ch.ContextHeader, "part ") {
			t.Fatalf("split chunk header lacks part marker")
		}
	}
}

func TestSingleGiantStatementStaysWhole(t *testing.T) {
	src := "X = [\n" + strings.Repeat("    'abc', 'def', 'ghi', 'jkl',\n", 200) + "]\n"
	chunks, syms := run(t, New(), "a.py", src)
	checkInvariants(t, "giant", src, chunks, syms)
	if len(chunks) != 1 {
		t.Fatalf("giant literal split into %d chunks", len(chunks))
	}
}

func TestTinySiblingsMerge(t *testing.T) {
	src := "def a():\n    return 1\ndef b():\n    return 2\ndef c():\n    return 3\n"
	chunks, syms := run(t, New(), "a.py", src)
	checkInvariants(t, "merge", src, chunks, syms)
	if len(chunks) != 1 {
		t.Fatalf("want 1 merged chunk, got %d", len(chunks))
	}
	chunks, syms = run(t, NewWithOptions(Options{MergeTokens: 1}), "a.py", src)
	checkInvariants(t, "nomerge", src, chunks, syms)
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(chunks))
	}
}

func TestSyntaxErrorsDoNotPanic(t *testing.T) {
	srcs := []string{
		"def f(:\n    pass\nclass\n",
		"class A:\n    def m(self):\n        x = (\n",
		"def ok():\n    return 1\n\n@@@ garbage ((( \n\ndef also():\n    pass\n",
		"\x00\xff\xfe def\n",
	}
	for i, src := range srcs {
		chunks, syms := run(t, New(), "a.py", src)
		checkInvariants(t, fmt.Sprintf("syntax%d", i), src, chunks, syms)
	}
}

func TestNoTrailingNewlineAndCRLF(t *testing.T) {
	for _, src := range []string{"def f():\n    return 1", "def f():\r\n    return 1\r\n\r\nX = 2\r\n"} {
		chunks, syms := run(t, New(), "a.py", src)
		checkInvariants(t, "eol", src, chunks, syms)
	}
}

// Symbols nested in function bodies (as a future internal/symbols may emit)
// must not be cut through.
func TestNestedFunctionSymbolsNotCrossed(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("def outer():\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "    a%d = 1\n", i)
	}
	sb.WriteString("    def inner():\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&sb, "        b%d = 2\n", i)
	}
	sb.WriteString("    return inner\n")
	src := sb.String()
	res, _ := parse.New().Parse(context.Background(), "a.py", []byte(src))
	syms := symbols.Extract(res, 1, "m")
	res.Tree.Close()
	outerID := syms[1].ID
	inner := &model.Symbol{ID: 99, FileID: 1, Kind: model.SymbolKindFunction, Name: "inner", QualifiedName: "m.outer.inner", ParentID: &outerID, StartLine: 42, EndLine: 122, Signature: "def inner():"}
	syms = append(syms, inner)
	c := NewWithOptions(Options{MaxTokens: 64})
	chunks, err := c.Chunk(context.Background(), &model.File{ID: 1, Path: "a.py"}, []byte(src), syms)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, "nestedfn", src, chunks, syms)
	if len(chunks) < 2 {
		t.Fatal("expected outer to be split around inner")
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Chunk(ctx, &model.File{}, []byte("x = 1\n"), nil); err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestCountTokens(t *testing.T) {
	if CountTokens("") != 0 || CountTokens("  \n ") != 0 {
		t.Fatal("whitespace must be free")
	}
	if got := CountTokens("def f(x):"); got != 6 {
		t.Fatalf("got %d", got)
	}
}

func TestFastAPIProperties(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	wd, _ := os.Getwd()
	dir := filepath.Join(wd, "..", "..", "repos", "fastapi")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("skipping: %s not present (run scripts/fetch-target-repo.sh): %v", dir, err)
	}
	files, err := walker.Walk(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := New()
	p := parse.New()
	var tokens []int
	total := 0
	for i, f := range files {
		res, err := p.Parse(context.Background(), f.Path, f.Content)
		if err != nil {
			t.Fatal(err)
		}
		syms := symbols.Extract(res, int64(i+1), f.ModuleName)
		res.Tree.Close()
		file := f.File
		file.ID = int64(i + 1)
		chunks, err := c.Chunk(context.Background(), &file, f.Content, syms)
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		checkInvariants(t, f.Path, string(f.Content), chunks, syms)
		for _, ch := range chunks {
			tokens = append(tokens, ch.TokenCount)
		}
		total += len(chunks)
	}
	sort.Ints(tokens)
	if len(tokens) == 0 {
		t.Fatal("no chunks")
	}
	q := func(p float64) int { return tokens[int(float64(len(tokens)-1)*p)] }
	t.Logf("FastAPI: %d files, %d chunks; tokens min=%d p50=%d p90=%d p99=%d max=%d",
		len(files), total, tokens[0], q(.5), q(.9), q(.99), tokens[len(tokens)-1])
}
