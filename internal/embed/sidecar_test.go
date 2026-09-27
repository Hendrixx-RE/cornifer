package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSidecarClientBareArrayResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sidecarRequest
		json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Inputs))
		for i := range out {
			out[i] = []float32{float32(i), float32(i)}
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	client, err := newSidecarClient(SidecarConfig{Endpoint: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("newSidecarClient() err = %v", err)
	}
	vecs, err := client.doEmbed(t.Context(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("doEmbed() err = %v", err)
	}
	if len(vecs) != 2 || vecs[1][0] != 1 {
		t.Fatalf("vecs = %v, want [[0 0] [1 1]]", vecs)
	}
}

func TestSidecarClientWrappedObjectResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sidecarRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := sidecarResponseObject{Embeddings: make([][]float32, len(req.Inputs))}
		for i := range resp.Embeddings {
			resp.Embeddings[i] = []float32{1, 2, 3}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := newSidecarClient(SidecarConfig{Endpoint: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("newSidecarClient() err = %v", err)
	}
	vecs, err := client.doEmbed(t.Context(), []string{"a"})
	if err != nil {
		t.Fatalf("doEmbed() err = %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("vecs = %v, want one 3-dim vector", vecs)
	}
}

func TestNewSidecarClientDefaults(t *testing.T) {
	client, err := newSidecarClient(SidecarConfig{Endpoint: "http://example.invalid/embed"})
	if err != nil {
		t.Fatalf("newSidecarClient() err = %v", err)
	}
	if client.model != client.endpoint {
		t.Errorf("model = %q, want it to default to endpoint %q", client.model, client.endpoint)
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil, want a default client")
	}
}

func TestSidecarClientRequiresEndpoint(t *testing.T) {
	_, err := newSidecarClient(SidecarConfig{})
	if err == nil {
		t.Fatal("newSidecarClient() err = nil, want error for missing Endpoint")
	}
}

func TestSidecarClient500IsRetryable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := newSidecarClient(SidecarConfig{Endpoint: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("newSidecarClient() err = %v", err)
	}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 1, dim: 4}
	_, err = b.Embed(t.Context(), []string{"a"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestSidecarClientMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	client, err := newSidecarClient(SidecarConfig{Endpoint: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("newSidecarClient() err = %v", err)
	}
	_, err = client.doEmbed(t.Context(), []string{"a"})
	if err == nil {
		t.Fatal("doEmbed() err = nil, want decode error")
	}
}
