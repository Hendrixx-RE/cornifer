package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Whether the Voyage client has ever been exercised against the real Voyage
// API: no. All tests below run against an httptest.Server double; no
// network access is used or required, and CI never needs a VOYAGE_API_KEY.

func TestVoyageClientSendsAuthAndBody(t *testing.T) {
	var gotAuth string
	var gotReq voyageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := voyageResponse{Data: make([]voyageEmbeddingData, len(gotReq.Input))}
		for i := range gotReq.Input {
			resp.Data[i] = voyageEmbeddingData{Embedding: make([]float32, 4), Index: i}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := newVoyageClient(VoyageConfig{APIKey: "test-key", BaseURL: srv.URL, HTTPClient: srv.Client()}, 4)
	if err != nil {
		t.Fatalf("newVoyageClient() err = %v", err)
	}

	vecs, err := client.doEmbed(t.Context(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("doEmbed() err = %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotReq.Model != DefaultVoyageModel {
		t.Errorf("Model = %q, want %q", gotReq.Model, DefaultVoyageModel)
	}
	if gotReq.OutputDimension != 4 {
		t.Errorf("OutputDimension = %d, want 4", gotReq.OutputDimension)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
}

func TestVoyageClientHandlesOutOfOrderIndices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req voyageRequest
		json.NewDecoder(r.Body).Decode(&req)
		// Return results out of order to verify doEmbed re-sorts by Index.
		resp := voyageResponse{Data: []voyageEmbeddingData{
			{Embedding: []float32{2, 2}, Index: 1},
			{Embedding: []float32{1, 1}, Index: 0},
		}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := newVoyageClient(VoyageConfig{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}, 2)
	if err != nil {
		t.Fatalf("newVoyageClient() err = %v", err)
	}
	vecs, err := client.doEmbed(t.Context(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("doEmbed() err = %v", err)
	}
	if vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Errorf("vecs not reordered by Index: %v", vecs)
	}
}

func TestVoyageClient429IsRetryable(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"detail":"rate limited"}`))
	}))
	defer srv.Close()

	client, err := newVoyageClient(VoyageConfig{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}, 4)
	if err != nil {
		t.Fatalf("newVoyageClient() err = %v", err)
	}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 1, dim: 4}

	_, err = b.Embed(t.Context(), []string{"a"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error after exhausting retries")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 { // 1 initial + 1 retry
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestVoyageClient400IsNotRetryable(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"detail":"bad request"}`))
	}))
	defer srv.Close()

	client, err := newVoyageClient(VoyageConfig{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}, 4)
	if err != nil {
		t.Fatalf("newVoyageClient() err = %v", err)
	}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 5, dim: 4}

	_, err = b.Embed(t.Context(), []string{"a"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (400 must not retry)", got)
	}
}

func TestNewVoyageClientDefaults(t *testing.T) {
	t.Setenv(VoyageAPIKeyEnvVar, "env-key")
	client, err := newVoyageClient(VoyageConfig{}, 4)
	if err != nil {
		t.Fatalf("newVoyageClient() err = %v", err)
	}
	if client.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want %q (should fall back to env var)", client.apiKey, "env-key")
	}
	if client.model != DefaultVoyageModel {
		t.Errorf("model = %q, want %q", client.model, DefaultVoyageModel)
	}
	if client.baseURL != DefaultVoyageBaseURL {
		t.Errorf("baseURL = %q, want %q", client.baseURL, DefaultVoyageBaseURL)
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil, want a default client")
	}
}

func TestNewVoyageMissingAPIKeyIsActionable(t *testing.T) {
	t.Setenv(VoyageAPIKeyEnvVar, "")
	_, err := newVoyageClient(VoyageConfig{}, 4)
	if err == nil {
		t.Fatal("newVoyageClient() err = nil, want ErrMissingAPIKey")
	}
	if err.Error() == "" {
		t.Fatal("error message is empty")
	}
}
