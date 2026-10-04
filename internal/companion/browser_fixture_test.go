package companion

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
)

// Explicit opt-in offline browser fixture, only for an isolated verification
// database. It remains until that test database/directory is removed. The fake
// embedding provider is used only inside this test; graph/source are real.
func TestCreateSequentialBrowserFixture(t *testing.T) {
	root, dsn := os.Getenv("CORNIFER_BROWSER_FIXTURE_DIR"), os.Getenv("CORNIFER_COMPANION_TEST_DSN")
	if root == "" || dsn == "" {
		t.Skip("explicit offline browser fixture requires directory and isolated test DSN")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || !(strings.Contains(parsed.Path, "_validation_") || strings.Contains(parsed.Path, "_test_") || strings.HasSuffix(parsed.Path, "_test")) {
		t.Fatal("browser fixture requires an explicitly named validation/test database; refusing user database")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"router.py": "from policy import accepts\n\nclass Router:\n    def dispatch(self, path):\n        \"\"\"Dispatch a request using the path policy.\"\"\"\n        return accepts(path)\n\ndef handle_request(path):\n    \"\"\"Handle request routing through Router dispatch.\"\"\"\n    router = Router()\n    return router.dispatch(path)\n",
		"policy.py": "def accepts(path):\n    \"\"\"Accept paths beginning with /api.\"\"\"\n    return path.startswith('/api')\n",
		"README.md": "# Explicit offline browser fixture\n\nRequest routing calls Router.dispatch, which delegates to policy.accepts.\nThis is local source evidence, not a hosted-provider validation.\n",
		"client.ts": "export const clientTimeout = 2500; // lexical-only TypeScript source\n",
	}
	for path, text := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"commit", "-m", "explicit offline sequential browser fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Offline Cornifer fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %s: %v", out, err)
		}
	}
	ctx := context.Background()
	engine, err := corestore.NewPostgres(ctx, corestore.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	cache := filepath.Join(filepath.Dir(root), "sequential-cache")
	stats, err := indexer.Index(ctx, engine, indexer.Config{RepoRoot: root, CacheDir: cache, IncludeText: true, Embedder: embed.Config{Provider: embed.ProviderFake}})
	if err != nil {
		t.Fatal(err)
	}
	product, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer product.Close()
	repo, _, err := product.UpsertRepository(ctx, Repository{CanonicalURL: "https://github.com/cornifer-offline-test/sequential-fixture.git", Status: StatusReady, Capabilities: []string{"python_structural_graph", "generic_lexical_text"}})
	if err != nil {
		t.Fatal(err)
	}
	repo.CheckoutPath, repo.CacheDir, repo.ResolvedCommitSHA, repo.EngineRepoID = root, cache, stats.CommitSHA, stats.RepoID
	repo.SafeMessage = "Verification fixture: offline sequential UI source/graph test; no hosted provider validation"
	if err := product.UpdateRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := product.CreateJob(ctx, Job{ID: newID(), RepositoryID: repo.ID, Phase: string(StatusReady)}); err != nil {
		t.Fatal(err)
	}
	// A merged class chunk must expose enclosed definitions and their real
	// immediate edges through the shared builder, not only its class owner.
	pack, err := (EngineContextBuilder{Engine: engine}).Build(ctx, repo, "How is request routing handled by Router dispatch?", ContextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ContextSymbol{}
	for _, symbol := range pack.Symbols {
		byID[symbol.ID] = symbol
		if symbol.QualifiedName == "router.Router.dispatch" && (!symbol.Evidence || symbol.StartLine != 4 || symbol.EndLine != 6 || symbol.Signature != "def dispatch(self, path):") {
			t.Fatalf("merged method metadata inaccurate: %+v", symbol)
		}
	}
	foundCall := false
	for _, edge := range pack.Relationships {
		if byID[edge.FromSymbolID].QualifiedName == "router.Router.dispatch" && byID[edge.ToSymbolID].QualifiedName == "policy.accepts" {
			foundCall = edge.Kind == "calls" && edge.Confidence > 0 && strings.HasPrefix(edge.FromCitationID, "e")
		}
	}
	if !foundCall || len(pack.Symbols) != 4 || len(pack.Relationships) != 3 || pack.Repository.CommitSHA != stats.CommitSHA {
		t.Fatalf("merged definitions/dependencies missing or not snapshot scoped: %+v", pack)
	}
	t.Logf("EXPLICIT OFFLINE FIXTURE id=%s sha=%s files=%d symbols=%d edges=%d chunks=%d", repo.ID, stats.CommitSHA, stats.Files, stats.Symbols, stats.Edges, stats.Chunks)
}
