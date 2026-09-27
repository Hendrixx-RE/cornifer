package embed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUnknownProvider(t *testing.T) {
	_, err := New(Config{Provider: "bogus"})
	if err == nil {
		t.Fatal("New() err = nil, want error for unknown provider")
	}
}

func TestNewFakeEmbedderRoundTrip(t *testing.T) {
	e, err := New(Config{Provider: ProviderFake, Dimension: 8})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 8 {
			t.Errorf("vecs[%d] len = %d, want 8", i, len(v))
		}
	}
}

func TestNewVoyageMissingAPIKey(t *testing.T) {
	t.Setenv(VoyageAPIKeyEnvVar, "")
	_, err := New(Config{Provider: ProviderVoyage, Dimension: 4})
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("New() err = %v, want ErrMissingAPIKey", err)
	}
}

func TestNewSidecarMissingEndpoint(t *testing.T) {
	_, err := New(Config{Provider: ProviderSidecar, Dimension: 4})
	if err == nil {
		t.Fatal("New() err = nil, want error for missing sidecar endpoint")
	}
}

func TestNewFakeWithCacheDirIsCached(t *testing.T) {
	dir := t.TempDir()
	e, err := New(Config{Provider: ProviderFake, Dimension: 4, CacheDir: dir})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, ok := e.(*cachingEmbedder); !ok {
		t.Fatalf("New() returned %T, want *cachingEmbedder when CacheDir is set", e)
	}
	v1, err := e.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	v2, err := e.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if v1[0][0] != v2[0][0] {
		t.Errorf("cached vector changed between calls: %v != %v", v1, v2)
	}
}

func TestMaybeCacheNoDir(t *testing.T) {
	inner := newFakeEmbedder(4)
	got := maybeCache(inner, "", "fake", 4)
	if got != Embedder(inner) {
		t.Errorf("maybeCache with empty dir should return inner unchanged")
	}
}

func TestMaybeCacheWithDir(t *testing.T) {
	inner := newFakeEmbedder(4)
	got := maybeCache(inner, t.TempDir(), "fake", 4)
	if _, ok := got.(*cachingEmbedder); !ok {
		t.Errorf("maybeCache with dir set should wrap in *cachingEmbedder, got %T", got)
	}
}

func TestNewSidecarWithCacheDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[[1,2,3,4]]`))
	}))
	defer srv.Close()

	e, err := New(Config{
		Provider:  ProviderSidecar,
		Dimension: 4,
		CacheDir:  t.TempDir(),
		Sidecar:   SidecarConfig{Endpoint: srv.URL, HTTPClient: srv.Client()},
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"hi"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
}

func TestNewVoyageWithCacheDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"embedding":[1,2,3,4],"index":0}]}`))
	}))
	defer srv.Close()

	e, err := New(Config{
		Provider:  ProviderVoyage,
		Dimension: 4,
		CacheDir:  t.TempDir(),
		Voyage:    VoyageConfig{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()},
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"hi"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
}
