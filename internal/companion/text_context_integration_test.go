package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
)

// These explicitly offline fixtures use fake vectors only for their Python
// chunks. Generic text has no vectors. No repository/provider is fetched.
func TestPostgresGenericCompanionContext(t *testing.T) {
	dsn := os.Getenv("CORNIFER_COMPANION_TEST_DSN")
	if dsn == "" {
		t.Skip("set CORNIFER_COMPANION_TEST_DSN for isolated database regression")
	}
	for _, mixed := range []bool{false, true} {
		name := "text-only"
		if mixed {
			name = "mixed-python-text"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root, cache := t.TempDir(), t.TempDir()
			write := func(path, text string) {
				t.Helper()
				full := filepath.Join(root, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write("docs/README.md", "# Offline lexical fixture\n\nquartzmanual describes blue widgets.\n")
			write("src/router.ts", "export function amberdispatch() {\n  return 'local fixture';\n}\n")
			write("old.txt", "obsoletefixture removal evidence\n")
			write("empty.md", "")
			write("docs/long.txt", strings.Repeat("多字 ", 2500)+"oversizedsource\n")
			write("ignored.bin", "excludedextension sentinel\n")
			if mixed {
				write("app.py", "def target():\n    return 1\n\ndef caller():\n    return target()\n")
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Offline Cornifer fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("fixture git %v: %s: %v", args, out, err)
				}
				return strings.TrimSpace(string(out))
			}
			git("init")
			git("add", ".")
			git("commit", "-m", "explicit offline generic text fixture")
			sha := git("rev-parse", "HEAD")
			engine, err := corestore.NewPostgres(ctx, corestore.Config{DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			product, err := NewPostgresStore(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(product.Close)
			cfg := indexer.Config{RepoRoot: root, CacheDir: cache, IncludeText: true, Embedder: embed.Config{Provider: embed.ProviderFake}}
			stats, err := indexer.Index(ctx, engine, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = product.pool.Exec(ctx, "DELETE FROM repos WHERE id=$1", stats.RepoID) })
			repo, _, err := product.UpsertRepository(ctx, Repository{CanonicalURL: "https://github.com/cornifer-offline-test/" + newID() + ".git", Status: StatusReady})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = product.pool.Exec(ctx, "DELETE FROM companion_repositories WHERE id=$1", repo.ID) })
			repo.CheckoutPath, repo.CacheDir, repo.ResolvedCommitSHA, repo.EngineRepoID = root, cache, sha, stats.RepoID
			repo.SafeMessage = "Verification fixture: offline generic lexical regression"
			if err := product.UpdateRepository(ctx, repo); err != nil {
				t.Fatal(err)
			}
			service := NewService(product, nil, EngineContextBuilder{Engine: engine})
			assertEvidence := func(question, path, needle string) ContextPack {
				t.Helper()
				pack, session, err := service.BuildContext(ctx, repo.ID, question, "", ContextOptions{})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("shared BM25 query %q returned %d citations at SHA %s", question, len(pack.Evidence), pack.Repository.CommitSHA[:12])
				if session.CommitSHA != sha || pack.Repository.CommitSHA != sha || !pack.Retrieval.Degraded || len(pack.Retrieval.SystemsUsed) != 1 || pack.Retrieval.SystemsUsed[0] != "bm25" {
					t.Fatalf("incorrect shared context provenance: %+v", pack)
				}
				for _, c := range pack.Evidence {
					if c.Path != path || !strings.Contains(c.Snippet, needle) {
						continue
					}
					if strings.Contains(c.Path, "\\") || c.StartLine < 1 || c.EndLine < c.StartLine || c.Symbol != "" || len(c.Sources) == 0 {
						t.Fatalf("invalid generic citation: %+v", c)
					}
					sum := sha256.Sum256([]byte(c.Snippet))
					if c.ExcerptSHA != hex.EncodeToString(sum[:]) {
						t.Fatal("excerpt hash mismatch")
					}
					source, err := (Explorer{Engine: engine}).Source(ctx, repo, c.Path, c.StartLine, c.EndLine)
					if err != nil || !strings.Contains(source.Content, c.Snippet) {
						t.Fatalf("citation source=%+v err=%v evidence=%+v", source, err, c)
					}
					return pack
				}
				t.Fatalf("no actual generic evidence for %s: %+v", question, pack.Evidence)
				return ContextPack{}
			}
			assertEvidence("quartzmanual", "docs/README.md", "quartzmanual")
			assertEvidence("amberdispatch", "src/router.ts", "amberdispatch")
			assertEvidence("obsoletefixture", "old.txt", "obsoletefixture")
			assertEvidence("oversizedsource", "docs/long.txt", "oversizedsource")
			request := httptest.NewRequest(http.MethodPost, "/api/context", strings.NewReader(fmt.Sprintf(`{"repository_id":%q,"question":"amberdispatch http evidence"}`, repo.ID)))
			response := httptest.NewRecorder()
			HTTPHandler(service, RuntimeConfig{}, Explorer{Engine: engine}).ServeHTTP(response, request)
			var wire struct {
				Context ContextPack `json:"context"`
				Session Session     `json:"session"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &wire) != nil || wire.Session.CommitSHA != sha {
				t.Fatalf("website context failed: status=%d body=%s", response.Code, response.Body)
			}
			foundHTTP := false
			for _, citation := range wire.Context.Evidence {
				foundHTTP = foundHTTP || citation.Path == "src/router.ts" && strings.Contains(citation.Snippet, "amberdispatch")
			}
			if !foundHTTP {
				t.Fatalf("website context omitted generic source evidence: %s", response.Body)
			}
			graph, err := (Explorer{Engine: engine}).Graph(ctx, repo, "")
			if err != nil {
				t.Fatal(err)
			}
			if mixed && (len(graph.Nodes) == 0 || len(graph.Edges) == 0) || !mixed && (len(graph.Nodes) != 0 || len(graph.Edges) != 0 || stats.Embedded != 0) {
				t.Fatalf("structural path changed: nodes=%d edges=%d embedded=%d", len(graph.Nodes), len(graph.Edges), stats.Embedded)
			}
			t.Logf("offline fixture: files=%d chunks=%d embedded Python chunks=%d nodes=%d edges=%d", stats.Files, stats.Chunks, stats.Embedded, len(graph.Nodes), len(graph.Edges))
			for _, n := range graph.Nodes {
				if !strings.HasSuffix(n.Path, ".py") {
					t.Fatal("generic file fabricated structural symbol")
				}
			}
			var genericVectors int
			if err := product.pool.QueryRow(ctx, "SELECT count(*) FROM chunks c JOIN files f ON f.id=c.file_id WHERE f.repo_id=$1 AND f.language <> 'python' AND c.embedding IS NOT NULL", stats.RepoID).Scan(&genericVectors); err != nil || genericVectors != 0 {
				t.Fatalf("generic vectors=%d err=%v", genericVectors, err)
			}
			before, err := engine.ListChunks(ctx, stats.RepoID)
			if err != nil {
				t.Fatal(err)
			}
			cacheFile := filepath.Join(cache, fmt.Sprintf("bm25-%d.json", stats.RepoID))
			cacheBefore, err := os.ReadFile(cacheFile)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := indexer.IncrementalIndex(ctx, engine, cfg)
			if err != nil || unchanged.Embedded != 0 {
				t.Fatalf("unchanged reindex=%+v err=%v", unchanged, err)
			}
			after, _ := engine.ListChunks(ctx, stats.RepoID)
			cacheAfter, _ := os.ReadFile(cacheFile)
			if chunkIDs(before) != chunkIDs(after) || string(cacheBefore) != string(cacheAfter) {
				t.Fatal("unchanged content rewrote chunks/cache")
			}
			write("docs/README.md", "# Updated lexical fixture\n\nceruleanmanual describes revised widgets.\n")
			write("added.go", "package offline\n// violetaddition is indexed text, not Go structural data.\n")
			if err := os.Remove(filepath.Join(root, "old.txt")); err != nil {
				t.Fatal(err)
			}
			updated, err := indexer.IncrementalIndex(ctx, engine, cfg)
			if err != nil || updated.Embedded != 0 {
				t.Fatalf("generic-only incremental=%+v err=%v", updated, err)
			}
			assertEvidence("ceruleanmanual", "docs/README.md", "ceruleanmanual")
			assertEvidence("violetaddition", "added.go", "violetaddition")
			// Use the shared builder directly to bypass the immutable snapshot's
			// ten-minute context cache during deliberate working-tree edits.
			pack, err := (EngineContextBuilder{Engine: engine}).Build(ctx, repo, "obsoletefixture", ContextOptions{})
			if err != nil || len(pack.Evidence) != 0 {
				t.Fatalf("deleted generic evidence remained: %+v err=%v", pack.Evidence, err)
			}
			pack, err = (EngineContextBuilder{Engine: engine}).Build(ctx, repo, "quartzmanual", ContextOptions{})
			if err != nil || len(pack.Evidence) != 0 {
				t.Fatalf("changed generic file retained old text: %+v err=%v", pack.Evidence, err)
			}
			files, _ := engine.ListFiles(ctx, stats.RepoID)
			for _, f := range files {
				if f.Path == "old.txt" || f.Path == "ignored.bin" {
					t.Fatalf("deleted/unsupported file retained: %s", f.Path)
				}
				if f.Path == "src/router.ts" {
					// Simulate the earlier bug: file/hash persisted, no chunks.
					if err := engine.DeleteFileContents(ctx, f.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := indexer.IncrementalIndex(ctx, engine, cfg); err != nil {
				t.Fatal(err)
			}
			assertEvidence("amberdispatch backfill", "src/router.ts", "amberdispatch")
			finalGraph, err := (Explorer{Engine: engine}).Graph(ctx, repo, "")
			if err != nil || len(finalGraph.Nodes) != len(graph.Nodes) || len(finalGraph.Edges) != len(graph.Edges) {
				t.Fatalf("generic reindex altered Python graph: %+v err=%v", finalGraph, err)
			}
		})
	}
}

func chunkIDs(chunks []*model.Chunk) string {
	var ids []string
	for _, c := range chunks {
		ids = append(ids, fmt.Sprint(c.ID))
	}
	return strings.Join(ids, ",")
}
