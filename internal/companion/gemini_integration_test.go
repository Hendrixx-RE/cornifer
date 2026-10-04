package companion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type offlineGeminiRunner struct {
	engine      corestore.Store
	root, cache string
	config      RuntimeConfig
}

func (r offlineGeminiRunner) Run(ctx context.Context, repo Repository, progress func(Progress)) (Repository, error) {
	cfg, err := r.config.embedConfig(false, filepath.Join(r.config.DataDir, "embeddings"))
	if err != nil {
		return repo, err
	}
	stats, err := indexer.Index(ctx, r.engine, indexer.Config{RepoRoot: r.root, CacheDir: r.cache, IncludeText: true, Embedder: cfg, Progress: func(p indexer.Progress) {
		progress(Progress{Phase: p.Phase, FilesSeen: p.Files, Chunks: p.Chunks, Edges: p.Edges, Cancellable: true})
	}})
	if err != nil {
		return repo, err
	}
	if repo.ResolvedCommitSHA != stats.CommitSHA {
		return repo, fmt.Errorf("retry changed pinned commit")
	}
	repo.CheckoutPath, repo.CacheDir, repo.EngineRepoID = r.root, r.cache, stats.RepoID
	repo.ProviderFingerprint = r.config.ProviderFingerprint()
	return repo, nil
}

// Explicit isolated PostgreSQL + native Gemini loopback mock. Proves credential
// requeue, persisted 1024-d model/role provenance and shared HTTP/MCP query
// context. No GitHub clone, Google call, billable key or local model is used.
func TestPostgresGeminiRetryAndSharedContext(t *testing.T) {
	dsn := os.Getenv("CORNIFER_COMPANION_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated companion test database required")
	}
	ctx := t.Context()
	root, cache := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("def target():\n    return 1\n\ndef caller():\n    return target()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Mocked Gemini fixture\nCaller delegates to target. Offline protocol verification only.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"commit", "-m", "offline Gemini protocol fixture"}} {
		command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Offline Gemini fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %s %v", out, err)
		}
	}
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	shaBytes, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(shaBytes))
	var documents, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content            struct{ Parts []struct{ Text string } }
			EmbedContentConfig struct {
				OutputDimensionality int
				TaskType             string
			}
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Content.Parts) != 1 || body.EmbedContentConfig.OutputDimensionality != 1024 || body.EmbedContentConfig.TaskType != "" || r.Header.Get("x-goog-api-key") != "mock-gemini-key" {
			t.Error("native mock protocol mismatch")
			w.WriteHeader(400)
			return
		}
		text := body.Content.Parts[0].Text
		if strings.HasPrefix(text, "title: none | text:") {
			documents.Add(1)
		} else if strings.HasPrefix(text, "task: code retrieval | query:") {
			queries.Add(1)
		} else {
			t.Error("incorrect task prefix")
		}
		vec := make([]float32, 1024)
		vec[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": map[string]any{"values": vec}})
	}))
	defer server.Close()
	engine, err := corestore.NewPostgres(ctx, corestore.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	product, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer product.Close()
	repo, _, err := product.UpsertRepository(ctx, Repository{CanonicalURL: "https://github.com/cornifer-offline-test/" + newID() + ".git", Status: StatusAwaitingCredential})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = product.pool.Exec(context.Background(), "DELETE FROM companion_repositories WHERE id=$1", repo.ID)
	}()
	repo.ResolvedCommitSHA = sha
	if err := product.UpdateRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	old := Job{ID: newID(), RepositoryID: repo.ID, Phase: "awaiting_credentials"}
	if err := product.CreateJob(ctx, old); err != nil {
		t.Fatal(err)
	}
	cfg := RuntimeConfig{DataDir: t.TempDir(), EmbeddingProvider: "gemini", EmbeddingModel: embed.DefaultGeminiModel, EmbeddingAPIKey: "mock-gemini-key", EmbeddingBaseURL: server.URL, EmbeddingDimension: 1024, GeminiRequestsPerMinute: 600}
	service := NewService(product, offlineGeminiRunner{engine, root, cache, cfg}, EngineContextBuilder{Engine: engine, Config: cfg})
	queued, job, reused, err := service.Ingest(ctx, repo.CanonicalURL, repo.RequestedRef)
	if err != nil || reused || queued.ID != repo.ID || job.ID == old.ID || queued.ResolvedCommitSHA != sha {
		t.Fatalf("credential retry changed snapshot or did not create job: %+v %v", queued, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		repo, err = product.GetRepository(ctx, repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		if repo.Status == StatusReady || repo.Status == StatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if repo.Status != StatusReady || repo.ResolvedCommitSHA != sha || documents.Load() == 0 {
		t.Fatalf("mocked Gemini indexing failed: %+v", repo)
	}
	defer func() {
		_, _ = product.pool.Exec(context.Background(), "DELETE FROM repos WHERE id=$1", repo.EngineRepoID)
	}()
	indexed, err := engine.GetRepoByCommit(ctx, root, sha)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := embed.GeminiIdentity(embed.GeminiConfig{Model: embed.DefaultGeminiModel, BaseURL: server.URL}, 1024)
	if indexed.EmbeddingProvider != "gemini" || indexed.EmbeddingModel != identity {
		t.Fatal("persisted Gemini vector-space identity incorrect")
	}
	question := "Where does caller call target?"
	response := httptest.NewRecorder()
	HTTPHandler(service, cfg).ServeHTTP(response, httptest.NewRequest("POST", "/api/context", strings.NewReader(fmt.Sprintf(`{"repository_id":%q,"question":%q}`, repo.ID, question))))
	var browser struct {
		Context ContextPack `json:"context"`
		Session Session     `json:"session"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &browser) != nil || len(browser.Context.Evidence) == 0 || len(browser.Context.Relationships) != 1 || browser.Session.CommitSHA != sha || queries.Load() != 1 {
		t.Fatalf("shared Gemini HTTP query failed: %s", response.Body)
	}
	ct, st := sdk.NewInMemoryTransports()
	ss, err := NewMCPServer(service).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "offline Gemini parity", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: "get_context", Arguments: map[string]any{"repository_id": repo.ID, "question": question}})
	if err != nil || result.IsError {
		t.Fatalf("shared MCP failed: %v", err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Context ContextPack `json:"context"`
	}
	if json.Unmarshal(raw, &mcp) != nil || !reflect.DeepEqual(browser.Context, mcp.Context) || queries.Load() != 1 {
		t.Fatal("HTTP/MCP pack/cache parity lost")
	}
	changed := cfg
	changed.EmbeddingModel = "gemini-embedding-001"
	mismatch := NewService(product, nil, EngineContextBuilder{Engine: engine, Config: changed})
	if _, _, err := mismatch.BuildContext(ctx, repo.ID, question, "", ContextOptions{}); err == nil || queries.Load() != 1 {
		t.Fatal("changed model reused old context cache or issued mismatched query")
	}
	changed = cfg
	changed.EmbeddingProvider = "voyage"
	mismatch = NewService(product, nil, EngineContextBuilder{Engine: engine, Config: changed})
	if _, _, err := mismatch.BuildContext(ctx, repo.ID, question, "", ContextOptions{}); err == nil || queries.Load() != 1 {
		t.Fatal("provider spaces silently mixed")
	}
	changed = cfg
	changed.EmbeddingAPIKey = ""
	lexical := NewService(product, nil, EngineContextBuilder{Engine: engine, Config: changed})
	sparse, _, err := lexical.BuildContext(ctx, repo.ID, question, "", ContextOptions{})
	if err != nil || !reflect.DeepEqual(sparse.Retrieval.SystemsUsed, []string{"bm25"}) || queries.Load() != 1 {
		t.Fatal("disabled embeddings reused dense context cache")
	}
	// Same exact input/model/task/dimension is retrieved from the embedding
	// cache even across a newly built client, without issuing another request.
	queryCfg, _ := cfg.embedConfig(true, filepath.Join(cfg.DataDir, "embeddings"))
	e, err := indexer.BuildEmbedder(queryCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(ctx, []string{question}); err != nil || queries.Load() != 1 {
		t.Fatal("unchanged matching embedding input re-embedded")
	}
	t.Logf("mocked Gemini: %d individual documents, one code query; persisted1024d; HTTP/MCP context parity; retry pin/model/cache isolation", documents.Load())
}
