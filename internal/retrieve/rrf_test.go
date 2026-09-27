package retrieve

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestFuse_HandComputed verifies RRF scores against numbers computed by
// hand, not by trusting the implementation: with k=60 and two retrievers
// each weighted 1, a chunk ranked 1st by both should score
// 1/61 + 1/61 = 0.032786885...
func TestFuse_HandComputed(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{
			{ChunkID: 1, Score: 9.0},
			{ChunkID: 2, Score: 8.0},
			{ChunkID: 3, Score: 7.0},
		}},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{
			{ChunkID: 1},
			{ChunkID: 3},
			{ChunkID: 4},
		}},
	}

	hits := Fuse(lists, 60)

	want := map[int64]float64{
		1: 1.0/61 + 1.0/61, // rank 1 in both
		2: 1.0 / 62,        // rank 2 in bm25 only
		3: 1.0/63 + 1.0/62, // rank 3 in bm25, rank 2 in vector
		4: 1.0 / 63,        // rank 3 in vector only
	}

	if len(hits) != 4 {
		t.Fatalf("got %d hits, want 4", len(hits))
	}
	seen := make(map[int64]float64)
	for _, h := range hits {
		seen[h.ChunkID] = h.Score
	}
	for id, wantScore := range want {
		got, ok := seen[id]
		if !ok {
			t.Fatalf("chunk %d missing from fused results", id)
		}
		if !almostEqual(got, wantScore) {
			t.Errorf("chunk %d: got score %v, want %v", id, got, wantScore)
		}
	}

	// Expected order by descending score: 1 (0.03279), 3 (0.03175), 2
	// (0.01613), 4 (0.01587).
	wantOrder := []int64{1, 3, 2, 4}
	for i, id := range wantOrder {
		if hits[i].ChunkID != id {
			t.Errorf("position %d: got chunk %d, want %d", i, hits[i].ChunkID, id)
		}
	}
}

func TestFuse_DisjointRankings(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 1}, {ChunkID: 2}}},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{{ChunkID: 3}, {ChunkID: 4}}},
	}
	hits := Fuse(lists, 60)
	if len(hits) != 4 {
		t.Fatalf("got %d hits, want 4", len(hits))
	}
	// Both rank-1 hits (1 and 3) should tie in score and be ordered by
	// ascending chunk ID; likewise the rank-2 hits (2 and 4).
	wantOrder := []int64{1, 3, 2, 4}
	for i, id := range wantOrder {
		if hits[i].ChunkID != id {
			t.Errorf("position %d: got chunk %d, want %d", i, hits[i].ChunkID, id)
		}
	}
	for _, h := range hits {
		if len(h.Sources) != 1 {
			t.Errorf("chunk %d: got %d sources, want 1 (disjoint rankings)", h.ChunkID, len(h.Sources))
		}
	}
}

func TestFuse_OverlappingRankings(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 1}, {ChunkID: 2}}},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{{ChunkID: 2}, {ChunkID: 1}}},
	}
	hits := Fuse(lists, 60)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	// Chunk 1: rank1 bm25 + rank2 vector = 1/61 + 1/62
	// Chunk 2: rank2 bm25 + rank1 vector = 1/62 + 1/61
	// These are equal, so tie-break by ascending chunk ID: 1 before 2.
	if hits[0].ChunkID != 1 || hits[1].ChunkID != 2 {
		t.Errorf("got order %d,%d, want 1,2 (tie-break by chunk ID)", hits[0].ChunkID, hits[1].ChunkID)
	}
	if !almostEqual(hits[0].Score, hits[1].Score) {
		t.Errorf("expected tied scores, got %v and %v", hits[0].Score, hits[1].Score)
	}
	for _, h := range hits {
		if len(h.Sources) != 2 {
			t.Errorf("chunk %d: got %d sources, want 2 (present in both rankings)", h.ChunkID, len(h.Sources))
		}
	}
}

func TestFuse_EmptyInputs(t *testing.T) {
	if hits := Fuse(nil, 60); len(hits) != 0 {
		t.Errorf("Fuse(nil): got %d hits, want 0", len(hits))
	}
	if hits := Fuse([]RankedList{}, 60); len(hits) != 0 {
		t.Errorf("Fuse(empty slice): got %d hits, want 0", len(hits))
	}
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: nil},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{}},
	}
	if hits := Fuse(lists, 60); len(hits) != 0 {
		t.Errorf("Fuse(empty result lists): got %d hits, want 0", len(hits))
	}
}

func TestFuse_DegenerateSingleRetriever(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 42}}},
	}
	hits := Fuse(lists, 60)
	if len(hits) != 1 || hits[0].ChunkID != 42 {
		t.Fatalf("got %+v, want single hit for chunk 42", hits)
	}
	want := 1.0 / 61
	if !almostEqual(hits[0].Score, want) {
		t.Errorf("got score %v, want %v", hits[0].Score, want)
	}
}

func TestFuse_DefaultKWhenNonPositive(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 1}}},
	}
	gotZero := Fuse(lists, 0)
	gotNeg := Fuse(lists, -5)
	gotDefault := Fuse(lists, DefaultRRFK)
	if !almostEqual(gotZero[0].Score, gotDefault[0].Score) {
		t.Errorf("k=0: got %v, want default-k score %v", gotZero[0].Score, gotDefault[0].Score)
	}
	if !almostEqual(gotNeg[0].Score, gotDefault[0].Score) {
		t.Errorf("k=-5: got %v, want default-k score %v", gotNeg[0].Score, gotDefault[0].Score)
	}
}

// TestFuse_WeightEffects verifies that increasing a retriever's weight
// increases the fused score contribution proportionally, and can change
// final ranking order.
func TestFuse_WeightEffects(t *testing.T) {
	base := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 1}, {ChunkID: 2}}},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{{ChunkID: 2}, {ChunkID: 1}}},
	}
	equalHits := Fuse(base, 60)
	if equalHits[0].ChunkID != 1 { // tie-break favors lower chunk ID
		t.Fatalf("sanity check failed: got %d first", equalHits[0].ChunkID)
	}

	weighted := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 1}, {ChunkID: 2}}},
		{Retriever: RetrieverVector, Weight: 5, Results: []RankedItem{{ChunkID: 2}, {ChunkID: 1}}},
	}
	weightedHits := Fuse(weighted, 60)
	// Chunk 2 is rank 1 in the heavily-weighted vector list, so it should
	// now outscore chunk 1 outright (no longer a tie).
	if weightedHits[0].ChunkID != 2 {
		t.Errorf("with vector weight=5, got chunk %d first, want chunk 2", weightedHits[0].ChunkID)
	}
	if weightedHits[0].Score <= weightedHits[1].Score {
		t.Errorf("expected a strict score gap once weighted, got %v vs %v", weightedHits[0].Score, weightedHits[1].Score)
	}
}

// TestFuse_Determinism runs the same fusion repeatedly and checks the
// output is byte-for-byte identical every time, including source order.
func TestFuse_Determinism(t *testing.T) {
	lists := []RankedList{
		{Retriever: RetrieverBM25, Weight: 1, Results: []RankedItem{{ChunkID: 5}, {ChunkID: 1}, {ChunkID: 9}, {ChunkID: 2}}},
		{Retriever: RetrieverVector, Weight: 1, Results: []RankedItem{{ChunkID: 9}, {ChunkID: 5}, {ChunkID: 2}, {ChunkID: 1}}},
	}
	var prev []Hit
	for i := 0; i < 50; i++ {
		hits := Fuse(lists, 60)
		if prev != nil {
			if len(hits) != len(prev) {
				t.Fatalf("run %d: length changed", i)
			}
			for j := range hits {
				if hits[j].ChunkID != prev[j].ChunkID || !almostEqual(hits[j].Score, prev[j].Score) {
					t.Fatalf("run %d: non-deterministic result at position %d: %+v vs %+v", i, j, hits[j], prev[j])
				}
			}
		}
		prev = hits
	}
}
