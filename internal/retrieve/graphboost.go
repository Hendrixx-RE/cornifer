package retrieve

import (
	"context"
	"sort"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
)

// DefaultGraphBoostWeight is deliberately small relative to RRF scores. A
// one-hop graph relationship is a tie-breaker/relevance signal, not a
// replacement for lexical or dense retrieval.
const DefaultGraphBoostWeight = 0.002

// GraphBoost rewards a fused chunk when its owning symbol is one graph hop
// from another top-ranked chunk, or when it shares that chunk's file. Seeds
// are taken from the unboosted fused ranking, so the operation is
// deterministic and cannot feed its own reordered output back into scoring.
//
// ChunkSymbols and ChunkFiles are intentionally plain maps supplied by the
// caller. retrieve remains independent of Store/indexer while CLI and MCP can
// build the maps from their repo-scoped catalog snapshots.
type GraphBoost struct {
	Graph        *graph.Graph
	ChunkSymbols map[int64]*int64
	ChunkFiles   map[int64]int64
	Weight       float64
	SeedLimit    int
}

func (b GraphBoost) Boost(_ context.Context, _ string, hits []Hit) ([]Hit, error) {
	if b.Graph == nil || len(hits) < 2 {
		return hits, nil
	}
	weight := b.Weight
	if weight == 0 {
		weight = DefaultGraphBoostWeight
	}
	if weight < 0 {
		return hits, nil
	}
	seedLimit := b.SeedLimit
	if seedLimit <= 0 {
		seedLimit = 1
	}
	if seedLimit > len(hits) {
		seedLimit = len(hits)
	}

	seedSymbols := make(map[int64]bool)
	seedFiles := make(map[int64]bool)
	neighbors := make(map[int64]bool)
	for _, hit := range hits[:seedLimit] {
		if fileID, ok := b.ChunkFiles[hit.ChunkID]; ok {
			seedFiles[fileID] = true
		}
		if symbol := b.ChunkSymbols[hit.ChunkID]; symbol != nil {
			seedSymbols[*symbol] = true
		}
	}
	for symbolID := range seedSymbols {
		for _, edge := range b.Graph.Out(symbolID) {
			neighbors[edge.DstSymbolID] = true
		}
		for _, edge := range b.Graph.In(symbolID) {
			neighbors[edge.SrcSymbolID] = true
		}
	}

	out := append([]Hit(nil), hits...)
	for i := range out {
		signal := 0
		if fileID, ok := b.ChunkFiles[out[i].ChunkID]; ok && seedFiles[fileID] {
			signal++
		}
		if symbol := b.ChunkSymbols[out[i].ChunkID]; symbol != nil && neighbors[*symbol] {
			signal += 2
		}
		if signal > 0 {
			out[i].Score += weight * float64(signal)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	return out, nil
}
