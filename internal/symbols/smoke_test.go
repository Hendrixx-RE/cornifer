package symbols

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
	"github.com/Hendrixx-RE/cornifer/internal/walker"
)

// targetRepoDir is where scripts/fetch-target-repo.sh checks out the pinned
// FastAPI commit (see TARGET_REPO at the repo root). It is gitignored and
// not fetched automatically by `go test`, so this smoke test skips itself
// rather than failing when the checkout is missing.
func targetRepoDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/symbols -> repo root -> repos/fastapi
	root := filepath.Join(wd, "..", "..")
	dir := filepath.Join(root, "repos", "fastapi")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("skipping: %s not present (run scripts/fetch-target-repo.sh first): %v", dir, err)
	}
	return dir
}

// TestFastAPISmoke walks and parses every Python file in the pinned FastAPI
// checkout end to end (walker -> parse -> symbols) and asserts a sane symbol
// count with zero panics. It is not a correctness test for any specific
// symbol (that's what the golden fixtures are for); it exists to catch
// crashes and wildly wrong output against a real, large, idiomatic
// codebase that the small hand-written fixtures can't exercise.
func TestFastAPISmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in -short mode")
	}
	root := targetRepoDir(t)

	files, err := walker.Walk(context.Background(), root)
	if err != nil {
		t.Fatalf("walker.Walk() error = %v", err)
	}
	if len(files) < 50 {
		t.Fatalf("walker.Walk() found %d files, want at least 50 for a FastAPI checkout", len(files))
	}

	p := parse.New()
	var (
		totalSymbols  int
		totalErrors   int
		moduleSymbols int
	)
	var nextFileID int64 = 1
	for _, f := range files {
		fileID := nextFileID
		nextFileID++

		res, err := p.Parse(context.Background(), f.Path, f.Content)
		if err != nil {
			t.Fatalf("Parse(%s) error = %v", f.Path, err)
		}

		syms := Extract(res, fileID, f.ModuleName)
		res.Tree.Close()

		totalErrors += len(res.Errors)
		for _, s := range syms {
			totalSymbols++
			if s.Kind == model.SymbolKindModule {
				moduleSymbols++
			}
		}
	}

	if moduleSymbols != len(files) {
		t.Errorf("got %d module symbols, want exactly %d (one per file)", moduleSymbols, len(files))
	}
	// FastAPI is a large, well-typed codebase; a healthy extraction run
	// should find symbols well into the thousands. This is a coarse sanity
	// bound, not a precise expectation — it exists to catch a wholesale
	// extraction failure (e.g. a wrong node-type check that silently
	// matches nothing), not to pin an exact count that would need updating
	// on every FastAPI release.
	const minSymbols = 3000
	if totalSymbols < minSymbols {
		t.Errorf("extracted %d symbols total, want at least %d", totalSymbols, minSymbols)
	}

	t.Logf("walked %d files, extracted %d symbols (%d module-level), %d syntax diagnostics", len(files), totalSymbols, moduleSymbols, totalErrors)
}
