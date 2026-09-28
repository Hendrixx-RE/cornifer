package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
)

type fakeBackend struct {
	symbols []*model.Symbol
	chunks  map[int64]*model.Chunk
	files   map[int64]*model.File
	edges   []*model.Edge
	hits    []retrieve.Hit
	failed  []retrieve.RetrieverError

	symErr, searchErr, catalogErr, graphErr error
	lastSearchLimit                         int
}

func (f *fakeBackend) FindSymbolsByName(_ context.Context, _ int64, name string) ([]*model.Symbol, error) {
	if f.symErr != nil {
		return nil, f.symErr
	}
	var out []*model.Symbol
	for _, s := range f.symbols {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeBackend) Search(_ context.Context, _ string, limit int) (*retrieve.Result, error) {
	f.lastSearchLimit = limit
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	hits := f.hits
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return &retrieve.Result{Hits: hits, Failed: f.failed}, nil
}

func (f *fakeBackend) GetChunks(_ context.Context, ids []int64) (map[int64]*model.Chunk, error) {
	if f.catalogErr != nil {
		return nil, f.catalogErr
	}
	out := map[int64]*model.Chunk{}
	for _, id := range ids {
		if c := f.chunks[id]; c != nil {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeBackend) GetSymbols(_ context.Context, ids []int64) (map[int64]*model.Symbol, error) {
	if f.catalogErr != nil {
		return nil, f.catalogErr
	}
	out := map[int64]*model.Symbol{}
	for _, id := range ids {
		for _, s := range f.symbols {
			if s.ID == id {
				out[id] = s
			}
		}
	}
	return out, nil
}

func (f *fakeBackend) GetFiles(_ context.Context, ids []int64) (map[int64]*model.File, error) {
	if f.catalogErr != nil {
		return nil, f.catalogErr
	}
	out := map[int64]*model.File{}
	for _, id := range ids {
		if fl := f.files[id]; fl != nil {
			out[id] = fl
		}
	}
	return out, nil
}

func (f *fakeBackend) Graph(context.Context) (*graph.Graph, error) {
	if f.graphErr != nil {
		return nil, f.graphErr
	}
	return graph.Build(f.edges), nil
}

func (f *fakeBackend) deps() Deps {
	return Deps{RepoID: 1, Symbols: f, Search: f, Catalog: f, Graphs: f}
}

func sym(id int64, qn, name string, kind model.SymbolKind) *model.Symbol {
	return &model.Symbol{ID: id, FileID: 1, Kind: kind, Name: name, QualifiedName: qn, StartLine: int(id), EndLine: int(id) + 1}
}

func edge(src, dst int64, k model.EdgeKind) *model.Edge {
	return &model.Edge{SrcSymbolID: src, DstSymbolID: dst, Kind: k, Confidence: model.ConfidenceExact}
}

func newFake() *fakeBackend {
	return &fakeBackend{
		files: map[int64]*model.File{1: {ID: 1, Path: "pkg/mod.py"}},
		symbols: []*model.Symbol{
			sym(1, "pkg.a", "a", model.SymbolKindModule),
			sym(2, "pkg.b", "b", model.SymbolKindModule),
			sym(3, "pkg.c", "c", model.SymbolKindModule),
			sym(4, "pkg.a.run", "run", model.SymbolKindFunction),
			sym(5, "pkg.b.run", "run", model.SymbolKindFunction),
			sym(6, "pkg.dup", "dup", model.SymbolKindFunction),
			sym(7, "pkg.dup", "dup", model.SymbolKindFunction),
		},
		// import cycle a -> b -> c -> a, plus calls 4 -> 5.
		edges: []*model.Edge{
			edge(1, 2, model.EdgeKindImports), edge(2, 3, model.EdgeKindImports), edge(3, 1, model.EdgeKindImports),
			edge(4, 5, model.EdgeKindCalls),
		},
	}
}

func TestFindDefinition(t *testing.T) {
	f := newFake()
	d := f.deps()
	_, out, err := d.findDefinition(context.Background(), nil, FindDefinitionInput{Symbol: "pkg.a.run"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Definition.ID != 4 || out.Definition.File != "pkg/mod.py" {
		t.Fatalf("got %+v", out.Definition)
	}
}

func TestFindDefinitionAmbiguous(t *testing.T) {
	d := newFake().deps()
	_, _, err := d.findDefinition(context.Background(), nil, FindDefinitionInput{Symbol: "pkg.dup"})
	if err == nil || !strings.Contains(err.Error(), "symbol ambiguous: 2 candidates") {
		t.Fatalf("want ambiguity error, got %v", err)
	}
	for _, want := range []string{"id=6", "id=7", "symbol_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	// Bare name shared by two different qualified names is also ambiguous.
	_, _, err = d.findDefinition(context.Background(), nil, FindDefinitionInput{Symbol: "run"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("bare name: got %v", err)
	}
	// symbol_id resolves the ambiguity.
	_, out, err := d.findDefinition(context.Background(), nil, FindDefinitionInput{SymbolID: 7})
	if err != nil || out.Definition.ID != 7 {
		t.Fatalf("symbol_id: %+v %v", out, err)
	}
}

func TestFindDefinitionErrors(t *testing.T) {
	f := newFake()
	d := f.deps()
	ctx := context.Background()
	if _, _, err := d.findDefinition(ctx, nil, FindDefinitionInput{}); err == nil {
		t.Error("empty input should error")
	}
	_, _, err := d.findDefinition(ctx, nil, FindDefinitionInput{Symbol: "pkg.x.run"})
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("not found with suggestions: got %v", err)
	}
	if _, _, err := d.findDefinition(ctx, nil, FindDefinitionInput{Symbol: "nothing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("got %v", err)
	}
	if _, _, err := d.findDefinition(ctx, nil, FindDefinitionInput{SymbolID: 99}); err == nil {
		t.Error("unknown symbol_id should error")
	}
	f.symErr = errors.New("db down")
	if _, _, err := d.findDefinition(ctx, nil, FindDefinitionInput{Symbol: "pkg.a"}); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("backend error not surfaced: %v", err)
	}
}

func TestSearchCode(t *testing.T) {
	f := newFake()
	long := strings.Repeat("line\n", 100)
	sid := int64(4)
	f.chunks = map[int64]*model.Chunk{
		10: {ID: 10, FileID: 1, SymbolID: &sid, Text: long},
		11: {ID: 11, FileID: 1, Text: "x = 1"},
		12: {ID: 12, FileID: 1, Text: "y = 2"},
	}
	f.hits = []retrieve.Hit{
		{ChunkID: 10, Score: 0.9, Sources: []retrieve.Source{{Retriever: retrieve.RetrieverBM25, Rank: 1}}},
		{ChunkID: 11, Score: 0.5}, {ChunkID: 12, Score: 0.1},
	}
	d := f.deps()
	ctx := context.Background()

	_, out, err := d.searchCode(ctx, nil, SearchCodeInput{Query: "q", Limit: 2, SnippetLines: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 2 || !out.Truncated || f.lastSearchLimit != 3 {
		t.Fatalf("hits=%d truncated=%v searchLimit=%d", len(out.Hits), out.Truncated, f.lastSearchLimit)
	}
	h := out.Hits[0]
	if !h.SnippetTruncated || strings.Count(h.Snippet, "\n") != 4 || h.Symbol != "pkg.a.run" || h.File != "pkg/mod.py" || h.Sources[0] != "bm25#1" {
		t.Fatalf("hit %+v", h)
	}

	_, out, err = d.searchCode(ctx, nil, SearchCodeInput{Query: "q", Limit: 1000, SnippetLines: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if f.lastSearchLimit != maxSearchLimit+1 || len(out.Notes) != 2 || out.Truncated {
		t.Fatalf("cap not enforced: limit=%d notes=%v", f.lastSearchLimit, out.Notes)
	}
	if len(out.Hits[0].Snippet) > maxSnippetBytes {
		t.Fatalf("snippet %d bytes exceeds cap", len(out.Hits[0].Snippet))
	}
}

func TestSearchCodeErrorsAndEmpty(t *testing.T) {
	f := newFake()
	d := f.deps()
	ctx := context.Background()
	if _, _, err := d.searchCode(ctx, nil, SearchCodeInput{}); err == nil {
		t.Error("empty query should error")
	}
	if _, _, err := d.searchCode(ctx, nil, SearchCodeInput{Query: "q", Limit: -1}); err == nil {
		t.Error("negative limit should error")
	}
	_, out, err := d.searchCode(ctx, nil, SearchCodeInput{Query: "q"})
	if err != nil || out.Hits == nil || len(out.Hits) != 0 {
		t.Fatalf("empty result: %+v %v", out, err)
	}
	f.failed = []retrieve.RetrieverError{{Retriever: retrieve.RetrieverVector, Err: errors.New("no key")}}
	if _, out, _ = d.searchCode(ctx, nil, SearchCodeInput{Query: "q"}); len(out.Notes) != 1 || !strings.Contains(out.Notes[0], "vector") {
		t.Errorf("degradation not reported: %v", out.Notes)
	}
	f.searchErr = errors.New("index offline")
	if _, _, err := d.searchCode(ctx, nil, SearchCodeInput{Query: "q"}); err == nil || !strings.Contains(err.Error(), "index offline") {
		t.Errorf("got %v", err)
	}
	f.searchErr = nil
	f.hits = []retrieve.Hit{{ChunkID: 10}}
	f.catalogErr = errors.New("catalog down")
	if _, _, err := d.searchCode(ctx, nil, SearchCodeInput{Query: "q"}); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Errorf("got %v", err)
	}
	if _, _, err := (Deps{}).searchCode(ctx, nil, SearchCodeInput{Query: "q"}); err == nil {
		t.Error("unconfigured backend should error")
	}
}

func TestFindReferences(t *testing.T) {
	f := newFake()
	d := f.deps()
	ctx := context.Background()
	_, out, err := d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.b.run"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || out.Nodes[0].ID != 4 || out.Nodes[0].EdgeKind != "calls" || out.Nodes[0].QualifiedName != "pkg.a.run" {
		t.Fatalf("%+v", out)
	}
	// Kind filter excludes the call edge.
	_, out, _ = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.b.run", Kinds: []string{"imports"}})
	if out.Total != 0 || out.Nodes == nil {
		t.Fatalf("kind filter: %+v", out)
	}
	// Symbol with no edges: empty plus explanation, not an error.
	_, out, err = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.dup", SymbolID: 6})
	if err != nil || out.Total != 0 || len(out.Notes) == 0 {
		t.Fatalf("no edges: %+v %v", out, err)
	}
	if _, _, err = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.dup"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("want ambiguity, got %v", err)
	}
	if _, _, err = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.a", Kinds: []string{"bogus"}}); err == nil {
		t.Error("bad kind should error")
	}
	if _, _, err = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.a", MinConfidence: 2}); err == nil {
		t.Error("bad confidence should error")
	}
	f.graphErr = errors.New("edges unavailable")
	if _, _, err = d.findReferences(ctx, nil, FindReferencesInput{Symbol: "pkg.b.run"}); err == nil || !strings.Contains(err.Error(), "edges unavailable") {
		t.Errorf("got %v", err)
	}
}

// chain builds 1 -> 2 -> ... -> n calls plus a wide fan-out from node 1.
func chainFake(n, fan int) *fakeBackend {
	f := &fakeBackend{files: map[int64]*model.File{1: {ID: 1, Path: "f.py"}}}
	for i := int64(1); i <= int64(n); i++ {
		f.symbols = append(f.symbols, sym(i, fmt.Sprintf("m.f%d", i), fmt.Sprintf("f%d", i), model.SymbolKindFunction))
		if i > 1 {
			f.edges = append(f.edges, edge(i-1, i, model.EdgeKindCalls))
		}
	}
	for j := 0; j < fan; j++ {
		id := int64(1000 + j)
		f.symbols = append(f.symbols, sym(id, fmt.Sprintf("m.leaf%d", j), fmt.Sprintf("leaf%d", j), model.SymbolKindFunction))
		f.edges = append(f.edges, edge(1, id, model.EdgeKindCalls))
	}
	return f
}

func TestGetDependenciesDepthAndLimit(t *testing.T) {
	d := chainFake(10, 0).deps()
	ctx := context.Background()
	_, out, err := d.getDependencies(ctx, nil, GetDependenciesInput{Symbol: "m.f1"})
	if err != nil || out.Total != defaultDepsDepth {
		t.Fatalf("default depth: total=%d err=%v", out.Total, err)
	}
	_, out, _ = d.getDependencies(ctx, nil, GetDependenciesInput{Symbol: "m.f1", Depth: 50})
	if out.Total != maxDepsDepth || len(out.Notes) != 1 {
		t.Fatalf("depth cap: total=%d notes=%v", out.Total, out.Notes)
	}
	if out.Nodes[0].Depth != 1 || out.Nodes[2].Depth != 3 {
		t.Fatalf("not ordered by depth: %+v", out.Nodes)
	}

	d = chainFake(2, 300).deps()
	_, out, _ = d.getDependencies(ctx, nil, GetDependenciesInput{Symbol: "m.f1", Limit: 10})
	if out.Total != 301 || out.Returned != 10 || !out.Truncated || len(out.Nodes) != 10 {
		t.Fatalf("limit: %+v", out.Traversal)
	}
	_, out, _ = d.getDependencies(ctx, nil, GetDependenciesInput{Symbol: "m.f1", Limit: 9999})
	if out.Returned != maxGraphLimit || !out.Truncated || len(out.Notes) != 1 {
		t.Fatalf("limit cap: returned=%d notes=%v", out.Returned, out.Notes)
	}
	if _, _, err = d.getDependencies(ctx, nil, GetDependenciesInput{Symbol: "m.f1", Depth: -1}); err == nil {
		t.Error("negative depth should error")
	}
}

func TestGetCallGraph(t *testing.T) {
	f := chainFake(6, 0)
	d := f.deps()
	ctx := context.Background()
	_, out, err := d.getCallGraph(ctx, nil, GetCallGraphInput{Symbol: "m.f3", Direction: "both", Depth: 99})
	if err != nil {
		t.Fatal(err)
	}
	// depth capped at 4: callees f4,f5,f6 (3), callers f2,f1 (2)
	if out.Callees == nil || out.Callers == nil || out.Callees.Total != 3 || out.Callers.Total != 2 || len(out.Notes) != 1 {
		t.Fatalf("%+v %+v notes=%v", out.Callees, out.Callers, out.Notes)
	}
	_, out, _ = d.getCallGraph(ctx, nil, GetCallGraphInput{Symbol: "m.f3", Depth: 1})
	if out.Callers != nil || out.Callees.Total != 1 {
		t.Fatalf("default direction should be callees: %+v", out)
	}
	if _, _, err = d.getCallGraph(ctx, nil, GetCallGraphInput{Symbol: "m.f3", Direction: "sideways"}); err == nil {
		t.Error("bad direction should error")
	}
	// Only call edges are followed: an import edge is excluded.
	f.edges = append(f.edges, edge(3, 2, model.EdgeKindImports))
	_, out, _ = d.getCallGraph(ctx, nil, GetCallGraphInput{Symbol: "m.f3", Direction: "callees", Depth: 1})
	if out.Callees.Total != 1 {
		t.Fatalf("import edge leaked into call graph: %+v", out.Callees)
	}
	f.graphErr = errors.New("boom")
	if _, _, err = d.getCallGraph(ctx, nil, GetCallGraphInput{Symbol: "m.f3"}); err == nil {
		t.Error("graph error should surface")
	}
}

func TestGetBlastRadius(t *testing.T) {
	d := chainFake(8, 0).deps()
	ctx := context.Background()
	_, out, err := d.getBlastRadius(ctx, nil, GetBlastRadiusInput{Symbol: "m.f8"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != defaultBlastDepth || len(out.ByDepth) != 3 || out.ByDepth[0] != (DepthCount{1, 1}) {
		t.Fatalf("%+v %+v", out.Traversal, out.ByDepth)
	}
	_, out, _ = d.getBlastRadius(ctx, nil, GetBlastRadiusInput{Symbol: "m.f8", Depth: 100})
	// The chain has 7 upstream nodes; the depth cap of 6 reaches only 6.
	if out.Total != maxBlastDepth || len(out.Notes) != 1 {
		t.Fatalf("depth cap: total=%d notes=%v", out.Total, out.Notes)
	}

	big := chainFake(2, 0)
	for i := int64(0); i < 500; i++ {
		id := 2000 + i
		big.symbols = append(big.symbols, sym(id, fmt.Sprintf("m.caller%d", i), "caller", model.SymbolKindFunction))
		big.edges = append(big.edges, edge(id, 2, model.EdgeKindCalls))
	}
	_, out, _ = big.deps().getBlastRadius(ctx, nil, GetBlastRadiusInput{Symbol: "m.f2"})
	if out.Total != 501 || out.Returned != defaultGraphLimit || !out.Truncated {
		t.Fatalf("truncation: total=%d returned=%d truncated=%v", out.Total, out.Returned, out.Truncated)
	}
	if out.ByDepth[0].Count != 501 {
		t.Fatalf("by_depth must count everything, not just returned: %+v", out.ByDepth)
	}
	if _, out, err = d.getBlastRadius(ctx, nil, GetBlastRadiusInput{Symbol: "nope"}); err == nil || out.Nodes == nil {
		t.Errorf("unknown symbol: %v", err)
	}
}

func TestFindCycles(t *testing.T) {
	f := newFake()
	d := f.deps()
	ctx := context.Background()
	_, out, err := d.findCycles(ctx, nil, FindCyclesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || out.Cycles[0].Size != 3 || len(out.Cycles[0].ShortestCycle) != 4 {
		t.Fatalf("%+v", out)
	}
	if out.Cycles[0].ShortestCycle[0].QualifiedName == "" {
		t.Error("cycle members should be hydrated")
	}

	// Empty graph: no cycles, not an error, non-nil slice.
	f.edges = nil
	_, out, err = d.findCycles(ctx, nil, FindCyclesInput{})
	if err != nil || out.Cycles == nil || len(out.Cycles) != 0 || len(out.Notes) == 0 {
		t.Fatalf("empty: %+v %v", out, err)
	}

	// Many cycles and one huge one: bounded by limit and member cap.
	f = newFake()
	f.edges = nil
	for c := int64(0); c < 40; c++ {
		a, b := 100+2*c, 101+2*c
		f.edges = append(f.edges, edge(a, b, model.EdgeKindImports), edge(b, a, model.EdgeKindImports))
	}
	for i := int64(0); i < 60; i++ {
		f.edges = append(f.edges, edge(500+i, 500+(i+1)%60, model.EdgeKindImports))
	}
	_, out, _ = f.deps().findCycles(ctx, nil, FindCyclesInput{Limit: 1000})
	if out.Total != 41 || out.Returned != maxCycleLimit || !out.Truncated || len(out.Notes) != 1 {
		t.Fatalf("limit: %+v", out)
	}
	if big := out.Cycles[0]; big.Size != 60 || len(big.Members) != maxCycleNodes || big.MembersOmitted != 40 {
		t.Fatalf("largest cycle first and member cap: size=%d members=%d omitted=%d", big.Size, len(big.Members), big.MembersOmitted)
	}
	if _, _, err = f.deps().findCycles(ctx, nil, FindCyclesInput{Kinds: []string{"x"}}); err == nil {
		t.Error("bad kind should error")
	}
	f.graphErr = errors.New("boom")
	if _, _, err = f.deps().findCycles(ctx, nil, FindCyclesInput{}); err == nil {
		t.Error("graph error should surface")
	}
}

func TestLazyGraphRetriesAfterFailure(t *testing.T) {
	calls := 0
	l := &LazyGraph{Loader: graph.EdgeLoaderFunc(func(context.Context) ([]*model.Edge, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("transient")
		}
		return []*model.Edge{edge(1, 2, model.EdgeKindCalls)}, nil
	})}
	if _, err := l.Graph(context.Background()); err == nil {
		t.Fatal("first load should fail")
	}
	g, err := l.Graph(context.Background())
	if err != nil || !g.HasNode(1) {
		t.Fatalf("retry: %v", err)
	}
	if _, _ = l.Graph(context.Background()); calls != 2 {
		t.Fatalf("successful load must be cached, calls=%d", calls)
	}
}

// End to end over an in-memory MCP transport: registration succeeds, schemas
// advertise bounds, and out-of-range input is rejected by the SDK.
func TestServerOverTransport(t *testing.T) {
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	Register(server, newFake().deps())

	ct, st := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"search_code": false, "find_definition": false, "find_references": false,
		"get_dependencies": false, "get_call_graph": false, "get_blast_radius": false, "find_cycles": false}
	for _, tl := range tools.Tools {
		want[tl.Name] = true
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("tool %s not registered", n)
		}
	}

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "find_cycles", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("find_cycles: %v %+v", err, res)
	}

	// Schema-level enforcement: over-cap depth and missing target are rejected.
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_blast_radius", Arguments: map[string]any{"symbol": "pkg.a", "depth": 99}})
	if err == nil && !res.IsError {
		t.Error("depth above schema maximum should be rejected")
	}
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "find_references", Arguments: map[string]any{}})
	if err == nil && !res.IsError {
		t.Error("missing symbol/symbol_id should be rejected")
	}

	// Handler errors surface as tool errors with the useful message.
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "find_definition", Arguments: map[string]any{"symbol": "pkg.dup"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content[0].(*sdk.TextContent).Text), "symbol ambiguous") {
		t.Fatalf("want ambiguity tool error, got %+v", res)
	}
}
