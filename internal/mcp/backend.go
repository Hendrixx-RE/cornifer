package mcp

import (
	"context"
	"sync"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
)

// SymbolFinder is the slice of internal/store.Store used to resolve a
// user-supplied name to symbols. It is the plural FindSymbolsByName on
// purpose: qualified names are not unique in practice (e.g. Python
// @overload stubs), so a single-result lookup would silently pick one.
type SymbolFinder interface {
	FindSymbolsByName(ctx context.Context, repoID int64, name string) ([]*model.Symbol, error)
}

// Searcher runs hybrid retrieval. *retrieve.HybridSearcher satisfies it.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) (*retrieve.Result, error)
}

// Catalog hydrates bare IDs (returned by retrieval and graph traversals)
// into rows a model can read. Missing IDs are simply absent from the
// returned maps. internal/store.Store has no ID-based lookups yet, so a
// real backend needs an adapter (see PR notes).
type Catalog interface {
	GetChunks(ctx context.Context, ids []int64) (map[int64]*model.Chunk, error)
	GetSymbols(ctx context.Context, ids []int64) (map[int64]*model.Symbol, error)
	GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error)
}

// GraphSource yields the call/import graph for the repo being served.
type GraphSource interface {
	Graph(ctx context.Context) (*graph.Graph, error)
}

// Deps is everything the handlers need. Any field may be a fake.
type Deps struct {
	RepoID  int64
	Symbols SymbolFinder
	Search  Searcher
	Catalog Catalog
	Graphs  GraphSource
}

// LazyGraph builds the graph on first use from loader and caches it. A
// failed load is not cached, so a transient DB error heals on retry.
type LazyGraph struct {
	Loader graph.EdgeLoader

	mu sync.Mutex
	g  *graph.Graph
}

// Graph implements GraphSource.
func (l *LazyGraph) Graph(ctx context.Context) (*graph.Graph, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.g != nil {
		return l.g, nil
	}
	g, err := graph.Load(ctx, l.Loader)
	if err != nil {
		return nil, err
	}
	l.g = g
	return g, nil
}
