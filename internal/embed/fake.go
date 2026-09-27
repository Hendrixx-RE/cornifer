package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// fakeEmbedder produces deterministic, hash-derived vectors with no network
// access or API key. The same text and dimension always yield the same
// vector, on any machine, so it is stable across runs and lets CI and other
// workers exercise the full pipeline without a real embedding provider.
type fakeEmbedder struct {
	dim int
}

func newFakeEmbedder(dim int) *fakeEmbedder {
	return &fakeEmbedder{dim: dim}
}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = fakeVector(t, f.dim)
	}
	return out, nil
}

// fakeVector derives a unit-length vector from text by seeding a splitmix64
// generator with SHA-256(text). SHA-256 and splitmix64 are both
// platform-independent (no map iteration order, no global math/rand seed),
// so the result is reproducible across processes and machines.
func fakeVector(text string, dim int) []float32 {
	v := make([]float32, dim)
	seed := sha256.Sum256([]byte(text))
	state := binary.BigEndian.Uint64(seed[:8])

	var sumSq float64
	for i := range v {
		state = splitmix64(state)
		f := float64(state>>11)/float64(1<<53)*2 - 1 // uniform in [-1, 1)
		v[i] = float32(f)
		sumSq += f * f
	}
	if norm := float32(math.Sqrt(sumSq)); norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
	return v
}

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}
