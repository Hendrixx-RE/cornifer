package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All Gemini tests use loopback HTTP or a mock transport. No real key or
// Google request, Files API, hosted billing or local inference is involved.
func geminiTestConfig(server *httptest.Server, role, model string) GeminiConfig {
	return GeminiConfig{APIKey: "mock-key-never-real", Model: model, InputType: role, BaseURL: server.URL, HTTPClient: server.Client(), RequestsPerMinute: 600, RetryBase: time.Millisecond, RetryMax: 10 * time.Millisecond}
}
func writeGeminiVector(w http.ResponseWriter, dim int, first float32) {
	vec := make([]float32, dim)
	vec[0] = first
	_ = json.NewEncoder(w).Encode(map[string]any{"embedding": map[string]any{"values": vec}})
}
func TestGeminiNativeTaskFormatsAnd1024Dimension(t *testing.T) {
	for _, tc := range []struct {
		model, role, text, task, title string
		normalize                      bool
	}{
		{DefaultGeminiModel, "document", "title: none | text: def route(): pass", "", "", false},
		{DefaultGeminiModel, "query", "task: code retrieval | query: def route(): pass", "", "", false},
		{"gemini-embedding-001", "document", "def route(): pass", "RETRIEVAL_DOCUMENT", "none", true},
		{"gemini-embedding-001", "query", "def route(): pass", "CODE_RETRIEVAL_QUERY", "", true},
	} {
		t.Run(tc.model+"/"+tc.role, func(t *testing.T) {
			var got geminiRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/models/"+tc.model+":embedContent" || r.URL.RawQuery != "" || r.Header.Get("x-goog-api-key") != "mock-key-never-real" || r.Header.Get("Authorization") != "" {
					t.Errorf("incorrect native URL/auth configuration")
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				vec := make([]float32, 1024)
				vec[0], vec[1] = .3, .4
				_ = json.NewEncoder(w).Encode(map[string]any{"embedding": map[string]any{"values": vec}})
			}))
			defer server.Close()
			e, err := New(Config{Provider: ProviderGemini, Dimension: 1024, Gemini: geminiTestConfig(server, tc.role, tc.model)})
			if err != nil {
				t.Fatal(err)
			}
			vecs, err := e.Embed(t.Context(), []string{"def route(): pass"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != "models/"+tc.model || len(got.Content.Parts) != 1 || got.Content.Parts[0].Text != tc.text || got.Config.OutputDimensionality != 1024 || got.Config.TaskType != tc.task || got.Config.Title != tc.title || got.Config.AutoTruncate {
				t.Fatalf("incorrect payload: %+v", got)
			}
			want := float32(.3)
			if tc.normalize {
				want = .6
			}
			if len(vecs) != 1 || len(vecs[0]) != 1024 || math.Abs(float64(vecs[0][0]-want)) > .00001 {
				t.Fatal("dimension/cardinality/normalization wrong")
			}
		})
	}
}
func TestGeminiDistinctInputsOrderedAndConcurrencyBounded(t *testing.T) {
	var active, maximum, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		calls.Add(1)
		var req geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		var index int
		_, _ = fmt.Sscanf(req.Content.Parts[0].Text, "title: none | text: chunk%d", &index)
		if index == 0 {
			time.Sleep(175 * time.Millisecond)
		}
		writeGeminiVector(w, 128, float32(index+1))
	}))
	defer server.Close()
	e, err := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: geminiTestConfig(server, "document", "")})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Embed(t.Context(), []string{"chunk0", "chunk1", "chunk2", "chunk3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 4 || calls.Load() != 4 || maximum.Load() > 2 {
		t.Fatalf("cardinality/concurrency: vectors=%d calls=%d active=%d", len(vecs), calls.Load(), maximum.Load())
	}
	for i, v := range vecs {
		if v[0] != float32(i+1) {
			t.Fatal("separate input vectors reordered or aggregated")
		}
	}
}
func TestGeminiResponseValidationAndRedaction(t *testing.T) {
	for _, body := range []string{`{}`, `{"embeddings":[{"values":[1]},{"values":[2]}]}`, `{"embedding":{"values":[1]}}`, `{"embedding":{"values":[1e100]}}`, `{"embedding":{"values":["NaN"]}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
		e, _ := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: geminiTestConfig(server, "document", "")})
		if _, err := e.Embed(t.Context(), []string{"source"}); err == nil {
			t.Fatalf("invalid provider response accepted: %s", body)
		}
		server.Close()
	}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, `{"error":"mock-key-never-real x-goog-api-key source-secret"}`)
	}))
	defer server.Close()
	e, _ := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: geminiTestConfig(server, "document", "")})
	_, err := e.Embed(t.Context(), []string{"source-secret"})
	if err == nil || attempts.Load() != 1 || strings.Contains(err.Error(), "mock-key") || strings.Contains(err.Error(), "source-secret") || strings.Contains(err.Error(), "x-goog") {
		t.Fatalf("auth response leaked or retried: %v", err)
	}
	// Even a transport error that echoes sensitive headers must be sanitized.
	cfg := geminiTestConfig(server, "document", "")
	cfg.HTTPClient = &http.Client{Transport: geminiRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("mock-key-never-real x-goog-api-key source-secret")
	})}
	client, _ := newGeminiEmbedder(cfg, 128, 0)
	_, err = client.Embed(t.Context(), []string{"source-secret"})
	if err == nil || strings.Contains(err.Error(), "mock-key") || strings.Contains(err.Error(), "x-goog") {
		t.Fatalf("transport leaked credentials: %v", err)
	}
}

type geminiRoundTrip func(*http.Request) (*http.Response, error)

func (f geminiRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGeminiRejectsZeroVectorAndOversizeBeforeAnyRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeGeminiVector(w, 128, 0) }))
	defer server.Close()
	cfg := geminiTestConfig(server, "document", "")
	e, _ := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: cfg})
	for _, texts := range [][]string{{"normal", strings.Repeat("x", Gemini2MaxInputBytes)}, {"\xff"}, {" "}} {
		if _, err := e.Embed(t.Context(), texts); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid later input made earlier provider call")
	}
	if _, err := e.Embed(t.Context(), []string{"valid"}); err == nil {
		t.Fatal("zero vector accepted")
	}
	for _, dim := range []int{127, 3073} {
		if _, err := New(Config{Provider: ProviderGemini, Dimension: dim, Gemini: cfg}); err == nil {
			t.Fatal("unsupported dimensions accepted")
		}
	}
	cfg.Model = "invented-model"
	if _, err := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: cfg}); err == nil {
		t.Fatal("silent model fallback")
	}
}
func TestGemini429BoundAndRetryAfterCancellation(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
	}))
	defer server.Close()
	e, _ := New(Config{Provider: ProviderGemini, Dimension: 128, MaxRetries: 1, Gemini: geminiTestConfig(server, "query", "")})
	if _, err := e.Embed(t.Context(), []string{"find routing"}); err == nil || attempts.Load() != 2 {
		t.Fatalf("unbounded retry: %d %v", attempts.Load(), err)
	}
	attempts.Store(0)
	slower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
	}))
	defer slower.Close()
	cfg := geminiTestConfig(slower, "query", "")
	cfg.RetryMax = 2 * time.Second
	e, _ = New(Config{Provider: ProviderGemini, Dimension: 128, MaxRetries: 3, Gemini: cfg})
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	_, err := e.Embed(ctx, []string{"find routing"})
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("Retry-After ignored/cancellation failed: %d %v", attempts.Load(), err)
	}
	cfg.RetryMax = 10 * time.Millisecond
	e, _ = New(Config{Provider: ProviderGemini, Dimension: 128, MaxRetries: 3, Gemini: cfg})
	attempts.Store(0)
	_, err = e.Embed(t.Context(), []string{"find routing"})
	if err == nil || attempts.Load() != 1 || !strings.Contains(err.Error(), "bounded retry window") {
		t.Fatal("long Retry-After shortened unsafely")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := geminiRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now); got != 3*time.Second {
		t.Fatal("HTTP-date Retry-After unsupported")
	}
	if geminiRetryAfter("9223372036854775807", now) != time.Duration(math.MaxInt64) {
		t.Fatal("Retry-After overflow")
	}
}
func TestGemini5xxRetryAndRedirectDoesNotForwardKey(t *testing.T) {
	var attempts, forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	e, _ := New(Config{Provider: ProviderGemini, Dimension: 128, Gemini: geminiTestConfig(redirect, "document", "")})
	_, err := e.Embed(t.Context(), []string{"code"})
	if err == nil || forwarded.Load() != 0 {
		t.Fatal("API key forwarded through redirect")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		writeGeminiVector(w, 128, 1)
	}))
	defer server.Close()
	e, _ = New(Config{Provider: ProviderGemini, Dimension: 128, MaxRetries: 1, Gemini: geminiTestConfig(server, "document", "")})
	if _, err := e.Embed(t.Context(), []string{"code"}); err != nil || attempts.Load() != 2 {
		t.Fatalf("5xx retry failed: %v", err)
	}
}
func TestGeminiCacheAndProvenanceIsolation(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var formats []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		formats = append(formats, req.Content.Parts[0].Text)
		mu.Unlock()
		writeGeminiVector(w, req.Config.OutputDimensionality, 1)
	}))
	defer server.Close()
	dir := t.TempDir()
	base := Config{Provider: ProviderGemini, Dimension: 128, CacheDir: dir, Gemini: geminiTestConfig(server, "document", "")}
	run := func(cfg Config) {
		t.Helper()
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Embed(t.Context(), []string{"same code"}); err != nil {
			t.Fatal(err)
		}
	}
	run(base)
	run(base)
	if calls.Load() != 1 {
		t.Fatal("unchanged document cache missed")
	}
	query := base
	query.Gemini.InputType = "query"
	run(query)
	older := base
	older.Gemini.Model = "gemini-embedding-001"
	run(older)
	otherDim := base
	otherDim.Dimension = 256
	run(otherDim)
	if calls.Load() != 4 {
		t.Fatal("role/model/dimension cache collision")
	}
	identity, _ := GeminiIdentity(base.Gemini, 128)
	if err := ValidateQuerySpace("gemini", identity, query); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQuerySpace("voyage", identity, query); err == nil {
		t.Fatal("provider spaces mixed")
	}
	if err := ValidateQuerySpace("gemini", identity, older); err == nil {
		t.Fatal("model spaces mixed")
	}
	if err := ValidateQuerySpace("gemini", identity, otherDim); err == nil {
		t.Fatal("dimension spaces mixed")
	}
	shifted := base
	shifted.Gemini.BaseURL = server.URL + "/different-api"
	run(shifted)
	if calls.Load() != 5 {
		t.Fatal("endpoint cache collision")
	}
	cached := &cachingEmbedder{model: "gemini;" + identity + ";input_type=document", dim: 128}
	voyage := &cachingEmbedder{model: DefaultGeminiModel, dim: 128}
	if cached.cacheKey("same code") == voyage.cacheKey("same code") {
		t.Fatal("provider cache collision")
	}
}
