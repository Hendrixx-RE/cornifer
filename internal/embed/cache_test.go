package embed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type countingEmbedder struct {
	calls int
	fn    func(texts []string) ([][]float32, error)
}

func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.calls++
	return c.fn(texts)
}

func TestCachingEmbedderMissThenHit(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}

	if _, err := c.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner.calls = %d, want 1", inner.calls)
	}

	if _, err := c.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner.calls after repeat = %d, want 1 (should be served from cache)", inner.calls)
	}
}

func TestCachingEmbedderPartialHit(t *testing.T) {
	dir := t.TempDir()
	var seen [][]string
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		seen = append(seen, append([]string(nil), texts...))
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}

	if _, err := c.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	out, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2", inner.calls)
	}
	if !equalStrings(seen[1], []string{"b"}) {
		t.Errorf("second call requested %v, want [b] (a should be a cache hit)", seen[1])
	}
}

func TestCachingEmbedderModelChangeInvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	}}
	c1 := &cachingEmbedder{inner: inner, dir: dir, model: "model-a", dim: 3}
	c2 := &cachingEmbedder{inner: inner, dir: dir, model: "model-b", dim: 3}

	if _, err := c1.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if _, err := c2.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2 (different model must not share cache entries)", inner.calls)
	}
}

func TestCachingEmbedderDimensionChangeInvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = make([]float32, 5)
		}
		return out, nil
	}}
	c1 := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}
	c2 := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 5}

	inner.fn = func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	}
	if _, err := c1.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	inner.fn = func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3, 4, 5}
		}
		return out, nil
	}
	if _, err := c2.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2 (different dim must not share cache entries)", inner.calls)
	}
}

func TestCachingEmbedderCorruptEntryIsTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 2, 3}
		}
		return out, nil
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}

	key := c.cacheKey("hello")
	path := c.cachePath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := c.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed() err = %v, want corrupt entry to be treated as a miss, not a fatal error", err)
	}
	if len(out) != 1 || len(out[0]) != 3 {
		t.Fatalf("out = %v, want one 3-dim vector re-embedded from the miss", out)
	}
	if inner.calls != 1 {
		t.Fatalf("inner.calls = %d, want 1", inner.calls)
	}

	// The corrupt entry should have been overwritten with a valid one.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after repair: %v", err)
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("cache entry still invalid after repair: %v", err)
	}
}

func TestCachingEmbedderTruncatedVectorIsTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{9, 9, 9}
		}
		return out, nil
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}

	key := c.cacheKey("hello")
	path := c.cachePath(key)
	os.MkdirAll(filepath.Dir(path), 0o755)
	bad, _ := json.Marshal(cacheEntry{Model: "m", Dim: 3, Vector: []float32{1}}) // wrong length
	os.WriteFile(path, bad, 0o644)

	out, err := c.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if len(out[0]) != 3 || out[0][0] != 9 {
		t.Fatalf("out = %v, want re-embedded [9 9 9]", out)
	}
}

func TestCachingEmbedderEmptyInput(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		t.Fatal("inner should not be called for empty input")
		return nil, nil
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}
	out, err := c.Embed(context.Background(), nil)
	if err != nil || out != nil {
		t.Fatalf("Embed(nil) = %v, %v, want nil, nil", out, err)
	}
}

func TestCachingEmbedderStoreErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where the cache expects to make a subdirectory,
	// so MkdirAll inside store() fails.
	c := &cachingEmbedder{
		inner: &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i := range out {
				out[i] = []float32{1, 2, 3}
			}
			return out, nil
		}},
		dir: dir, model: "m", dim: 3,
	}
	key := c.cacheKey("x")
	blocker := filepath.Join(dir, key[:2])
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error when the cache directory can't be created")
	}
}

func TestCachingEmbedderPropagatesInnerError(t *testing.T) {
	dir := t.TempDir()
	wantErr := context.DeadlineExceeded
	inner := &countingEmbedder{fn: func(texts []string) ([][]float32, error) {
		return nil, wantErr
	}}
	c := &cachingEmbedder{inner: inner, dir: dir, model: "m", dim: 3}
	_, err := c.Embed(context.Background(), []string{"x"})
	if err != wantErr {
		t.Fatalf("Embed() err = %v, want %v", err, wantErr)
	}
}
