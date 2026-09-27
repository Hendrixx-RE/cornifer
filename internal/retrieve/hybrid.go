package retrieve

import (
	"context"
	"fmt"
	"sync"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Retriever identifies which underlying search produced a Source.
type Retriever string

const (
	RetrieverBM25   Retriever = "bm25"
	RetrieverVector Retriever = "vector"
)

// VectorSearcher is the narrow slice of internal/store's Store interface
// that hybrid search needs: nearest-neighbor lookup over chunk embeddings.
// It is defined here, rather than importing internal/store, so this
// package does not couple to the store package's build (which is being
// developed in parallel) or drag in Postgres/pgvector as a dependency for
// callers that only need retrieval. internal/store's Store implementation
// satisfies this interface as-is (see Store.VectorSearch), so no adapter
// is needed to wire a real store in.
type VectorSearcher interface {
	// VectorSearch returns the limit chunks with embeddings nearest to
	// query, ordered closest first. len(query) must equal the embedder's
	// output dimension.
	VectorSearch(ctx context.Context, query []float32, limit int) ([]*model.Chunk, error)
}

// BoostStage is a post-fusion hook, applied after RRF and before the final
// result truncation. It is the seam graph-adjacency boosting (plan.md
// "Days 13-14: hybrid fusion": "boost hits that are graph-adjacent
// ... to other top-N hits") and cross-encoder rerank plug into later; both
// are deferred out of this wave (rerank is explicitly optional/eval-gated,
// and graph boosting depends on internal/graph, which is being built by a
// parallel worker and not yet available to import here).
//
// A future graph-aware BoostStage should typically:
//  1. Take the fused hits (already ranked, with per-retriever provenance).
//  2. Look up graph adjacency for the query's seed chunks/symbols (e.g. via
//     a graph.Graph loaded by the caller) and compute an adjacency score
//     per hit — callers/callees/same-module to other top-N hits.
//  3. Combine that adjacency score with Hit.Score (e.g. additive boost or
//     re-fusion as a third RRF input list) and re-sort, preserving
//     Hit.Sources so provenance survives boosting.
//
// The identity boost (NoBoost) is the default and used by all tests in
// this package; it is a no-op so hybrid search behaves identically until a
// real BoostStage is wired in by the caller.
type BoostStage interface {
	Boost(ctx context.Context, query string, hits []Hit) ([]Hit, error)
}

// NoBoost is the default BoostStage: it returns hits unchanged. Use it (or
// leave Config.Boost nil, which HybridSearcher treats the same way) until
// graph-adjacency boosting is implemented.
type NoBoost struct{}

func (NoBoost) Boost(_ context.Context, _ string, hits []Hit) ([]Hit, error) {
	return hits, nil
}

// Config configures a HybridSearcher.
type Config struct {
	// RRFK is the k in RRF's score = weight / (k + rank). Zero or negative
	// selects DefaultRRFK.
	RRFK float64

	// WeightBM25 and WeightVector scale each retriever's contribution to
	// the fused RRF score. Zero selects a weight of 1 (equal contribution);
	// use a small positive value, not zero, to down-weight a retriever
	// without disabling it, since Fuse treats a zero weight as "unset" and
	// substitutes 1.
	WeightBM25   float64
	WeightVector float64

	// Boost is applied to fused hits before the result list is truncated
	// to the caller's requested limit. Nil means NoBoost (see BoostStage).
	Boost BoostStage
}

// RetrieverError records that a single retriever failed during a hybrid
// search. HybridSearcher.Search degrades gracefully when at least one
// retriever succeeds: it returns the surviving retriever's results plus
// one RetrieverError per failure, rather than failing the whole query.
type RetrieverError struct {
	Retriever Retriever
	Err       error
}

func (e RetrieverError) Error() string {
	return fmt.Sprintf("retrieve: %s retriever failed: %v", e.Retriever, e.Err)
}

// Result is the outcome of a hybrid search: fused, ranked hits plus any
// per-retriever failures that were tolerated to produce them.
type Result struct {
	Hits   []Hit
	Failed []RetrieverError
}

// HybridSearcher queries a lexical (BM25) index and a dense (vector) index
// concurrently, fuses their rankings with Reciprocal Rank Fusion, and
// returns results with per-retriever provenance intact.
type HybridSearcher struct {
	Sparse   bm25.SparseIndex
	Vector   VectorSearcher
	Embedder embed.Embedder
	Config   Config
}

// NewHybridSearcher builds a HybridSearcher. sparse and vector may not both
// be nil. embedder is required whenever vector is non-nil (it turns the
// query text into the vector VectorSearch needs).
func NewHybridSearcher(sparse bm25.SparseIndex, vector VectorSearcher, embedder embed.Embedder, cfg Config) *HybridSearcher {
	return &HybridSearcher{Sparse: sparse, Vector: vector, Embedder: embedder, Config: cfg}
}

// Search runs BM25 and vector search concurrently for query, each asked
// for up to limit results, fuses them with weighted RRF, applies the
// configured BoostStage (a no-op unless Config.Boost is set), and returns
// the top limit fused hits.
//
// If exactly one retriever is configured (the other is nil) or exactly one
// configured retriever errors, Search degrades gracefully: it fuses and
// returns whatever succeeded, and reports the failure via Result.Failed
// rather than failing the query. Search only returns a non-nil error when
// no retriever produced results at all (both failed, or both are
// unconfigured).
func (h *HybridSearcher) Search(ctx context.Context, query string, limit int) (*Result, error) {
	if limit <= 0 {
		return &Result{}, nil
	}

	var (
		wg       sync.WaitGroup
		bm25List RankedList
		vecList  RankedList
		bm25Err  error
		vecErr   error
		haveBM25 bool
		haveVec  bool
	)

	if h.Sparse != nil {
		haveBM25 = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := h.Sparse.Search(ctx, query, limit)
			if err != nil {
				bm25Err = err
				return
			}
			items := make([]RankedItem, len(results))
			for i, r := range results {
				items[i] = RankedItem{ChunkID: r.ChunkID, Score: r.Score}
			}
			bm25List = RankedList{Retriever: RetrieverBM25, Weight: h.Config.WeightBM25, Results: items}
		}()
	}

	if h.Vector != nil {
		haveVec = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.Embedder == nil {
				vecErr = fmt.Errorf("retrieve: vector retriever configured without an Embedder")
				return
			}
			vecs, err := h.Embedder.Embed(ctx, []string{query})
			if err != nil {
				vecErr = fmt.Errorf("embed query: %w", err)
				return
			}
			if len(vecs) == 0 {
				vecErr = fmt.Errorf("embed query: no vector returned")
				return
			}
			chunks, err := h.Vector.VectorSearch(ctx, vecs[0], limit)
			if err != nil {
				vecErr = err
				return
			}
			items := make([]RankedItem, len(chunks))
			for i, c := range chunks {
				items[i] = RankedItem{ChunkID: c.ID}
			}
			vecList = RankedList{Retriever: RetrieverVector, Weight: h.Config.WeightVector, Results: items}
		}()
	}

	wg.Wait()

	var (
		lists  []RankedList
		failed []RetrieverError
	)
	if haveBM25 {
		if bm25Err != nil {
			failed = append(failed, RetrieverError{Retriever: RetrieverBM25, Err: bm25Err})
		} else {
			lists = append(lists, bm25List)
		}
	}
	if haveVec {
		if vecErr != nil {
			failed = append(failed, RetrieverError{Retriever: RetrieverVector, Err: vecErr})
		} else {
			lists = append(lists, vecList)
		}
	}

	if len(lists) == 0 {
		if len(failed) > 0 {
			return nil, fmt.Errorf("retrieve: all retrievers failed: %v", failed)
		}
		return &Result{}, nil
	}

	hits := Fuse(lists, h.Config.RRFK)

	boost := h.Config.Boost
	if boost == nil {
		boost = NoBoost{}
	}
	boosted, err := boost.Boost(ctx, query, hits)
	if err != nil {
		return nil, fmt.Errorf("retrieve: boost stage: %w", err)
	}
	hits = boosted

	if len(hits) > limit {
		hits = hits[:limit]
	}

	return &Result{Hits: hits, Failed: failed}, nil
}
