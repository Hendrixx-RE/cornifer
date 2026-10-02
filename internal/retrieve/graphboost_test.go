package retrieve

import (
	"context"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestGraphBoostPromotesGraphAdjacentChunk(t *testing.T) {
	// Chunk 2 starts slightly behind chunk 1, but its symbol is adjacent to
	// chunk 1's symbol. Chunk 3 is unrelated and must remain behind it.
	boost := GraphBoost{
		Graph:        graph.Build([]*model.Edge{{SrcSymbolID: 10, DstSymbolID: 20, Kind: model.EdgeKindCalls, Confidence: 1}}),
		ChunkSymbols: map[int64]*int64{1: int64ptr(10), 2: int64ptr(20), 3: int64ptr(30)},
		ChunkFiles:   map[int64]int64{1: 1, 2: 2, 3: 3},
		Weight:       0.01,
	}
	hits, err := boost.Boost(context.Background(), "q", []Hit{{ChunkID: 1, Score: 0.03}, {ChunkID: 2, Score: 0.029}, {ChunkID: 3, Score: 0.028}})
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].ChunkID != 2 {
		t.Fatalf("first boosted chunk = %d, want graph-adjacent 2; hits=%+v", hits[0].ChunkID, hits)
	}
	if hits[2].ChunkID != 3 {
		t.Fatalf("unrelated chunk order = %+v, want chunk 3 last", hits)
	}
}

func TestGraphBoostZeroGraphIsNoop(t *testing.T) {
	hits := []Hit{{ChunkID: 2, Score: 2}, {ChunkID: 1, Score: 1}}
	got, err := (GraphBoost{}).Boost(context.Background(), "q", hits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ChunkID != 2 || got[1].ChunkID != 1 {
		t.Fatalf("nil graph changed hits: %+v", got)
	}
}

func int64ptr(v int64) *int64 { return &v }
