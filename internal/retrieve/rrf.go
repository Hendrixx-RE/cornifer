package retrieve

import "sort"

// DefaultRRFK is the Reciprocal Rank Fusion smoothing constant recommended
// by plan.md ("Days 13-14: hybrid fusion": "score = Sum 1 / (k + rank_i),
// k = 60"). It is parameterized on Config so it can be tuned; see
// docs/tuning.md for why 60 is the starting point.
const DefaultRRFK = 60.0

// RankedItem is one retriever's hit for a chunk, before fusion. Score is
// the retriever's native score (BM25 weight, cosine distance, ...) and is
// carried through only for provenance/debugging — RRF itself only uses
// Rank.
type RankedItem struct {
	ChunkID int64
	Score   float64
}

// RankedList is one retriever's full ranking for a query, best result
// first. Weight scales that retriever's contribution to the fused score;
// see Config.
type RankedList struct {
	Retriever Retriever
	Weight    float64
	Results   []RankedItem
}

// Source records that a chunk was returned by a particular retriever, at
// what rank, and with what native score. Hit.Sources carries one Source per
// retriever that surfaced the chunk, which is the per-retriever provenance
// the eval wave depends on (see internal/retrieve doc.go).
type Source struct {
	Retriever Retriever
	Rank      int // 1-based rank within that retriever's own results.
	Score     float64
}

// Hit is one fused, ranked result.
type Hit struct {
	ChunkID int64
	Score   float64
	Sources []Source
}

// Fuse combines any number of RankedLists via weighted Reciprocal Rank
// Fusion: for each chunk, score = Sum over retrievers in which it appears
// of (weight_i / (k + rank_i)), where rank_i is 1-based. A chunk absent
// from a retriever's results contributes nothing for that retriever — RRF
// does not penalize absence beyond simply not adding a term.
//
// k must be positive; callers should default to DefaultRRFK when the
// caller-supplied value is <= 0 (Config.rrfK does this).
//
// Fused results are sorted by score descending. Ties (identical fused
// score, which is common with disjoint single-retriever hits at the same
// rank) are broken deterministically by ascending ChunkID, so results are
// stable across repeated runs and across process restarts — required for
// the eval wave, which compares runs over time.
func Fuse(lists []RankedList, k float64) []Hit {
	if k <= 0 {
		k = DefaultRRFK
	}

	type accum struct {
		chunkID int64
		score   float64
		sources []Source
	}
	order := make([]int64, 0)
	byChunk := make(map[int64]*accum)

	for _, list := range lists {
		weight := list.Weight
		if weight == 0 {
			weight = 1
		}
		for i, item := range list.Results {
			rank := i + 1
			a, ok := byChunk[item.ChunkID]
			if !ok {
				a = &accum{chunkID: item.ChunkID}
				byChunk[item.ChunkID] = a
				order = append(order, item.ChunkID)
			}
			a.score += weight / (k + float64(rank))
			a.sources = append(a.sources, Source{
				Retriever: list.Retriever,
				Rank:      rank,
				Score:     item.Score,
			})
		}
	}

	hits := make([]Hit, 0, len(order))
	for _, id := range order {
		a := byChunk[id]
		hits = append(hits, Hit{ChunkID: a.chunkID, Score: a.score, Sources: a.sources})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ChunkID < hits[j].ChunkID
	})

	return hits
}
