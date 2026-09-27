package retrieve

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// fakeSparse is a stub bm25.SparseIndex returning canned results or an
// error, for testing HybridSearcher without a real index.
type fakeSparse struct {
	results []bm25.Result
	err     error
}

func (f *fakeSparse) Index(ctx context.Context, chunks []*model.Chunk) error { return nil }

func (f *fakeSparse) Search(ctx context.Context, query string, limit int) ([]bm25.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := f.results
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fakeVector is a stub VectorSearcher returning canned chunks or an error.
type fakeVector struct {
	chunks []*model.Chunk
	err    error
}

func (f *fakeVector) VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := f.chunks
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func newFakeEmbedder(t *testing.T) embed.Embedder {
	t.Helper()
	e, err := embed.New(embed.Config{Provider: embed.ProviderFake})
	if err != nil {
		t.Fatalf("embed.New(fake): %v", err)
	}
	return e
}

func TestHybridSearch_FusesBothRetrievers(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{
		{ChunkID: 1, Score: 9},
		{ChunkID: 2, Score: 5},
	}}
	vector := &fakeVector{chunks: []*model.Chunk{
		{ID: 2},
		{ID: 3},
	}}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	res, err := hs.Search(context.Background(), "rate limiting", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failed)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("got %d hits, want 3", len(res.Hits))
	}

	byID := make(map[int64]Hit)
	for _, h := range res.Hits {
		byID[h.ChunkID] = h
	}

	hit2 := byID[2]
	if len(hit2.Sources) != 2 {
		t.Errorf("chunk 2 (in both rankings): got %d sources, want 2", len(hit2.Sources))
	}
	hit1 := byID[1]
	if len(hit1.Sources) != 1 || hit1.Sources[0].Retriever != RetrieverBM25 {
		t.Errorf("chunk 1 (bm25 only): got sources %+v", hit1.Sources)
	}
	hit3 := byID[3]
	if len(hit3.Sources) != 1 || hit3.Sources[0].Retriever != RetrieverVector {
		t.Errorf("chunk 3 (vector only): got sources %+v", hit3.Sources)
	}

	// Chunk 2 is present in both rankings, so it should outrank chunk 1
	// and chunk 3, which each only appear once.
	if res.Hits[0].ChunkID != 2 {
		t.Errorf("got %d first, want chunk 2 (present in both retrievers)", res.Hits[0].ChunkID)
	}
}

func TestHybridSearch_LimitTruncates(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{
		{ChunkID: 1}, {ChunkID: 2}, {ChunkID: 3}, {ChunkID: 4},
	}}
	hs := NewHybridSearcher(sparse, nil, nil, Config{})

	res, err := hs.Search(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits, want 2 (limit)", len(res.Hits))
	}
}

func TestHybridSearch_OneRetrieverErrors_DegradesGracefully(t *testing.T) {
	sparse := &fakeSparse{err: errors.New("index corrupt")}
	vector := &fakeVector{chunks: []*model.Chunk{{ID: 1}, {ID: 2}}}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	res, err := hs.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search should degrade gracefully, got error: %v", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Retriever != RetrieverBM25 {
		t.Fatalf("got Failed=%+v, want one BM25 failure", res.Failed)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits from surviving vector retriever, want 2", len(res.Hits))
	}
	for _, h := range res.Hits {
		if len(h.Sources) != 1 || h.Sources[0].Retriever != RetrieverVector {
			t.Errorf("chunk %d: got sources %+v, want single vector source", h.ChunkID, h.Sources)
		}
	}
}

func TestHybridSearch_OtherRetrieverErrors_DegradesGracefully(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{{ChunkID: 1}}}
	vector := &fakeVector{err: errors.New("db unreachable")}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	res, err := hs.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search should degrade gracefully, got error: %v", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Retriever != RetrieverVector {
		t.Fatalf("got Failed=%+v, want one vector failure", res.Failed)
	}
	if len(res.Hits) != 1 || res.Hits[0].ChunkID != 1 {
		t.Fatalf("got hits=%+v, want single bm25 hit", res.Hits)
	}
}

func TestHybridSearch_AllRetrieversError_ReturnsError(t *testing.T) {
	sparse := &fakeSparse{err: errors.New("index corrupt")}
	vector := &fakeVector{err: errors.New("db unreachable")}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	_, err := hs.Search(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error when every retriever fails")
	}
}

func TestHybridSearch_EmptyResultsFromBothRetrievers(t *testing.T) {
	sparse := &fakeSparse{}
	vector := &fakeVector{}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	res, err := hs.Search(context.Background(), "nonsense query", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("got %d hits, want 0", len(res.Hits))
	}
	if len(res.Failed) != 0 {
		t.Errorf("got Failed=%+v, want none (empty result is not a failure)", res.Failed)
	}
}

func TestHybridSearch_ZeroLimit(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{{ChunkID: 1}}}
	hs := NewHybridSearcher(sparse, nil, nil, Config{})

	res, err := hs.Search(context.Background(), "q", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("got %d hits for limit=0, want 0", len(res.Hits))
	}
}

func TestHybridSearch_NoRetrieversConfigured(t *testing.T) {
	hs := NewHybridSearcher(nil, nil, nil, Config{})
	res, err := hs.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("got %d hits with no retrievers, want 0", len(res.Hits))
	}
}

func TestHybridSearch_VectorWithoutEmbedderErrors(t *testing.T) {
	vector := &fakeVector{chunks: []*model.Chunk{{ID: 1}}}
	hs := NewHybridSearcher(nil, vector, nil, Config{})

	_, err := hs.Search(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error: vector retriever configured without an embedder and no other retriever to fall back on")
	}
}

func TestHybridSearch_WeightsAffectRanking(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{{ChunkID: 1}, {ChunkID: 2}}}
	vector := &fakeVector{chunks: []*model.Chunk{{ID: 2}, {ID: 1}}}

	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{WeightVector: 10})
	res, err := hs.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Hits[0].ChunkID != 2 {
		t.Errorf("with vector weight=10, got chunk %d first, want chunk 2 (rank 1 in heavily weighted vector list)", res.Hits[0].ChunkID)
	}
}

// TestHybridSearch_Determinism runs the same hybrid search repeatedly and
// requires identical output every time, since eval depends on stable
// rankings across runs.
func TestHybridSearch_Determinism(t *testing.T) {
	sparse := &fakeSparse{results: []bm25.Result{
		{ChunkID: 5, Score: 1}, {ChunkID: 1, Score: 1}, {ChunkID: 9, Score: 1},
	}}
	vector := &fakeVector{chunks: []*model.Chunk{{ID: 9}, {ID: 5}, {ID: 1}}}
	hs := NewHybridSearcher(sparse, vector, newFakeEmbedder(t), Config{})

	var prev []Hit
	for i := 0; i < 20; i++ {
		res, err := hs.Search(context.Background(), "q", 10)
		if err != nil {
			t.Fatalf("run %d: Search: %v", i, err)
		}
		if prev != nil {
			if len(res.Hits) != len(prev) {
				t.Fatalf("run %d: hit count changed", i)
			}
			for j := range res.Hits {
				if res.Hits[j].ChunkID != prev[j].ChunkID {
					t.Fatalf("run %d: non-deterministic order at position %d", i, j)
				}
			}
		}
		prev = res.Hits
	}
}

func TestNoBoost_IsIdentity(t *testing.T) {
	hits := []Hit{{ChunkID: 1, Score: 0.5}, {ChunkID: 2, Score: 0.3}}
	out, err := NoBoost{}.Boost(context.Background(), "q", hits)
	if err != nil {
		t.Fatalf("NoBoost.Boost: %v", err)
	}
	if len(out) != len(hits) {
		t.Fatalf("got %d hits, want %d", len(out), len(hits))
	}
	for i := range hits {
		if out[i].ChunkID != hits[i].ChunkID || out[i].Score != hits[i].Score {
			t.Errorf("position %d: got %+v, want %+v", i, out[i], hits[i])
		}
	}
}
