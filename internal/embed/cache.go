package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cachingEmbedder wraps another Embedder with an on-disk cache keyed by a
// hash of (model identifier, dimension, text), so re-embedding unchanged
// text across runs is free (see plan.md "embeddings and ANN"). Including the
// model identifier and dimension in the key, and re-validating them on read,
// means switching embedding models or dimensions can never silently serve a
// stale vector of the wrong shape.
type cachingEmbedder struct {
	inner Embedder
	dir   string
	model string
	dim   int
}

type cacheEntry struct {
	Model  string    `json:"model"`
	Dim    int       `json:"dim"`
	Vector []float32 `json:"vector"`
}

func (c *cachingEmbedder) cacheKey(text string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00", c.model, c.dim)
	io.WriteString(h, text)
	return hex.EncodeToString(h.Sum(nil))
}

// cachePath fans keys out into two-character subdirectories so the cache
// directory doesn't end up with millions of entries in one flat listing.
func (c *cachingEmbedder) cachePath(key string) string {
	return filepath.Join(c.dir, key[:2], key+".json")
}

// load returns (vector, true) on a valid cache hit. Any read error, decode
// error, or a stored model/dimension/length that doesn't match the current
// configuration is treated as a miss (not a fatal error) so a corrupt entry
// or a model/dimension change just causes a re-embed-and-overwrite.
func (c *cachingEmbedder) load(key string) ([]float32, bool) {
	data, err := os.ReadFile(c.cachePath(key))
	if err != nil {
		return nil, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}
	if entry.Model != c.model || entry.Dim != c.dim || len(entry.Vector) != c.dim {
		return nil, false
	}
	return entry.Vector, true
}

// store writes via a temp file + rename so concurrent readers/writers never
// observe a partially written cache entry (os.Rename is atomic within a
// filesystem).
func (c *cachingEmbedder) store(key string, vec []float32) error {
	path := c.cachePath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheEntry{Model: c.model, Dim: c.dim, Vector: vec})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func (c *cachingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, len(texts))
	keys := make([]string, len(texts))
	var missIdx []int
	var missTexts []string

	for i, t := range texts {
		key := c.cacheKey(t)
		keys[i] = key
		if v, ok := c.load(key); ok {
			out[i] = v
			continue
		}
		missIdx = append(missIdx, i)
		missTexts = append(missTexts, t)
	}

	if len(missTexts) > 0 {
		vecs, err := c.inner.Embed(ctx, missTexts)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(missTexts) {
			return nil, fmt.Errorf("embed: cache: inner embedder returned %d vectors for %d inputs", len(vecs), len(missTexts))
		}
		for j, v := range vecs {
			idx := missIdx[j]
			out[idx] = v
			if err := c.store(keys[idx], v); err != nil {
				return nil, fmt.Errorf("embed: write cache entry: %w", err)
			}
		}
	}

	return out, nil
}
