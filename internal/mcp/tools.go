package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// ---- search_code ----

type SearchCodeInput struct {
	Query        string `json:"query" jsonschema:"natural-language or identifier query"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max hits to return (default 10, max 25)"`
	SnippetLines int    `json:"snippet_lines,omitempty" jsonschema:"max lines of code per hit (default 20, max 60)"`
}

type SearchHit struct {
	ChunkID          int64    `json:"chunk_id"`
	Score            float64  `json:"score"`
	File             string   `json:"file,omitempty"`
	Symbol           string   `json:"symbol,omitempty"`
	StartLine        int      `json:"start_line,omitempty"`
	EndLine          int      `json:"end_line,omitempty"`
	Snippet          string   `json:"snippet"`
	SnippetTruncated bool     `json:"snippet_truncated"`
	Sources          []string `json:"sources"`
}

type SearchCodeOutput struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated"`
	Notes     []string    `json:"notes,omitempty"`
}

func (d Deps) searchCode(ctx context.Context, _ *sdk.CallToolRequest, in SearchCodeInput) (*sdk.CallToolResult, SearchCodeOutput, error) {
	out := SearchCodeOutput{Hits: []SearchHit{}}
	if in.Query == "" {
		return nil, out, fmt.Errorf("query is required")
	}
	if d.Search == nil || d.Catalog == nil {
		return nil, out, fmt.Errorf("search backend is not configured")
	}
	limit, err := bound("limit", in.Limit, defaultSearchLimit, maxSearchLimit, &out.Notes)
	if err != nil {
		return nil, out, err
	}
	lines, err := bound("snippet_lines", in.SnippetLines, defaultSnippetLines, maxSnippetLines, &out.Notes)
	if err != nil {
		return nil, out, err
	}

	// Ask for one extra so we can tell the caller more results exist.
	res, err := d.Search.Search(ctx, in.Query, limit+1)
	if err != nil {
		return nil, out, fmt.Errorf("search: %w", err)
	}
	if res == nil {
		return nil, out, nil
	}
	for _, f := range res.Failed {
		out.Notes = append(out.Notes, "degraded: "+f.Error())
	}
	hits := res.Hits
	if len(hits) > limit {
		hits = hits[:limit]
		out.Truncated = true
	}
	if len(hits) == 0 {
		return nil, out, nil
	}

	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.ChunkID
	}
	chunks, err := d.Catalog.GetChunks(ctx, ids)
	if err != nil {
		return nil, out, fmt.Errorf("load chunks: %w", err)
	}
	var symIDs, fileIDs []int64
	for _, c := range chunks {
		if c.SymbolID != nil {
			symIDs = append(symIDs, *c.SymbolID)
		}
		fileIDs = append(fileIDs, c.FileID)
	}
	syms, err := d.Catalog.GetSymbols(ctx, symIDs)
	if err != nil {
		return nil, out, fmt.Errorf("load symbols: %w", err)
	}
	files, err := d.Catalog.GetFiles(ctx, fileIDs)
	if err != nil {
		return nil, out, fmt.Errorf("load files: %w", err)
	}

	for _, h := range hits {
		c := chunks[h.ChunkID]
		if c == nil {
			continue // index and store disagree; skip rather than fail the query
		}
		sh := SearchHit{ChunkID: h.ChunkID, Score: h.Score, Sources: []string{}}
		sh.Snippet, sh.SnippetTruncated = truncateSnippet(c.Text, lines)
		if f := files[c.FileID]; f != nil {
			sh.File = f.Path
		}
		if c.SymbolID != nil {
			if s := syms[*c.SymbolID]; s != nil {
				sh.Symbol, sh.StartLine, sh.EndLine = s.QualifiedName, s.StartLine, s.EndLine
			}
		}
		for _, s := range h.Sources {
			sh.Sources = append(sh.Sources, fmt.Sprintf("%s#%d", s.Retriever, s.Rank))
		}
		out.Hits = append(out.Hits, sh)
	}
	return nil, out, nil
}

// ---- find_definition ----

type FindDefinitionInput struct {
	Symbol   string `json:"symbol,omitempty" jsonschema:"qualified (pkg.mod.Class.method) or bare symbol name"`
	SymbolID int64  `json:"symbol_id,omitempty" jsonschema:"exact symbol id, to disambiguate an ambiguous name"`
}

type FindDefinitionOutput struct {
	Definition SymbolRef `json:"definition"`
	Docstring  string    `json:"docstring,omitempty"`
}

func (d Deps) findDefinition(ctx context.Context, _ *sdk.CallToolRequest, in FindDefinitionInput) (*sdk.CallToolResult, FindDefinitionOutput, error) {
	var out FindDefinitionOutput
	sym, err := d.resolve(ctx, in.Symbol, in.SymbolID)
	if err != nil {
		return nil, out, err
	}
	refs, err := d.refs(ctx, []*model.Symbol{sym})
	if err != nil {
		return nil, out, err
	}
	out.Definition = refs[0]
	out.Docstring = truncateString(sym.Docstring, 800)
	return nil, out, nil
}

// ---- find_references ----

type FindReferencesInput struct {
	Symbol        string   `json:"symbol,omitempty" jsonschema:"qualified or bare symbol name"`
	SymbolID      int64    `json:"symbol_id,omitempty" jsonschema:"exact symbol id"`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"edge kinds to include: imports, calls, inherits, implements (default all)"`
	MinConfidence float64  `json:"min_confidence,omitempty" jsonschema:"drop edges below this resolution confidence, 0-1"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max references (default 25, max 100)"`
}

// TraversalOutput is the shared response shape of the graph-backed tools.
type TraversalOutput struct {
	Target SymbolRef `json:"target"`
	Traversal
	Notes []string `json:"notes,omitempty"`
}

// traverse resolves the target, runs walk on the graph and shapes the
// bounded result. It is the common spine of find_references,
// get_dependencies and get_blast_radius.
func (d Deps) traverse(ctx context.Context, name string, id int64, limit int, notes []string,
	walk func(g *graph.Graph, start int64) []graph.Path) (TraversalOutput, error) {
	out := TraversalOutput{Notes: notes, Traversal: Traversal{Nodes: []GraphNode{}}}
	sym, err := d.resolve(ctx, name, id)
	if err != nil {
		return out, err
	}
	refs, err := d.refs(ctx, []*model.Symbol{sym})
	if err != nil {
		return out, err
	}
	out.Target = refs[0]
	g, err := d.graph(ctx)
	if err != nil {
		return out, err
	}
	if !g.HasNode(sym.ID) {
		out.Notes = append(out.Notes, "symbol has no resolved edges in the graph (resolution may not have run, or nothing references it)")
		return out, nil
	}
	t, err := d.shape(ctx, walk(g, sym.ID), limit)
	if err != nil {
		return out, err
	}
	out.Traversal = t
	return out, nil
}

func (d Deps) findReferences(ctx context.Context, _ *sdk.CallToolRequest, in FindReferencesInput) (*sdk.CallToolResult, TraversalOutput, error) {
	var notes []string
	limit, err := bound("limit", in.Limit, defaultRefLimit, maxRefLimit, &notes)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	kinds, err := parseKinds(in.Kinds)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	conf, err := parseConfidence(in.MinConfidence)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	out, err := d.traverse(ctx, in.Symbol, in.SymbolID, limit, notes, func(g *graph.Graph, id int64) []graph.Path {
		return graph.Callers(g, id, graph.TraversalOptions{Kinds: kinds, MinConfidence: conf, MaxDepth: 1})
	})
	return nil, out, err
}

// ---- get_dependencies ----

type GetDependenciesInput struct {
	Symbol        string   `json:"symbol,omitempty" jsonschema:"qualified or bare symbol name (a module name gives its imports)"`
	SymbolID      int64    `json:"symbol_id,omitempty" jsonschema:"exact symbol id"`
	Depth         int      `json:"depth,omitempty" jsonschema:"hops to follow (default 1, max 3)"`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"edge kinds to follow: imports, calls, inherits, implements (default all)"`
	MinConfidence float64  `json:"min_confidence,omitempty" jsonschema:"drop edges below this resolution confidence, 0-1"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max symbols (default 50, max 200)"`
}

func (d Deps) getDependencies(ctx context.Context, _ *sdk.CallToolRequest, in GetDependenciesInput) (*sdk.CallToolResult, TraversalOutput, error) {
	var notes []string
	depth, err := bound("depth", in.Depth, defaultDepsDepth, maxDepsDepth, &notes)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	limit, err := bound("limit", in.Limit, defaultGraphLimit, maxGraphLimit, &notes)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	kinds, err := parseKinds(in.Kinds)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	conf, err := parseConfidence(in.MinConfidence)
	if err != nil {
		return nil, TraversalOutput{}, err
	}
	out, err := d.traverse(ctx, in.Symbol, in.SymbolID, limit, notes, func(g *graph.Graph, id int64) []graph.Path {
		return graph.Callees(g, id, graph.TraversalOptions{Kinds: kinds, MinConfidence: conf, MaxDepth: depth})
	})
	return nil, out, err
}

// ---- get_blast_radius ----

type GetBlastRadiusInput struct {
	Symbol        string   `json:"symbol,omitempty" jsonschema:"qualified or bare symbol name"`
	SymbolID      int64    `json:"symbol_id,omitempty" jsonschema:"exact symbol id"`
	Depth         int      `json:"depth,omitempty" jsonschema:"hops to walk in reverse (default 3, max 6)"`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"edge kinds to follow (default imports and calls)"`
	MinConfidence float64  `json:"min_confidence,omitempty" jsonschema:"drop edges below this resolution confidence, 0-1"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max affected symbols listed (default 50, max 200)"`
}

// DepthCount is how many affected symbols sit at one hop distance.
type DepthCount struct {
	Depth int `json:"depth"`
	Count int `json:"count"`
}

type BlastRadiusOutput struct {
	TraversalOutput
	ByDepth []DepthCount `json:"by_depth"`
}

func (d Deps) getBlastRadius(ctx context.Context, _ *sdk.CallToolRequest, in GetBlastRadiusInput) (*sdk.CallToolResult, BlastRadiusOutput, error) {
	var notes []string
	fail := func(err error) (*sdk.CallToolResult, BlastRadiusOutput, error) {
		return nil, BlastRadiusOutput{TraversalOutput: TraversalOutput{Traversal: Traversal{Nodes: []GraphNode{}}}, ByDepth: []DepthCount{}}, err
	}
	depth, err := bound("depth", in.Depth, defaultBlastDepth, maxBlastDepth, &notes)
	if err != nil {
		return fail(err)
	}
	limit, err := bound("limit", in.Limit, defaultGraphLimit, maxGraphLimit, &notes)
	if err != nil {
		return fail(err)
	}
	kinds, err := parseKinds(in.Kinds)
	if err != nil {
		return fail(err)
	}
	conf, err := parseConfidence(in.MinConfidence)
	if err != nil {
		return fail(err)
	}
	counts := map[int]int{}
	out, err := d.traverse(ctx, in.Symbol, in.SymbolID, limit, notes, func(g *graph.Graph, id int64) []graph.Path {
		paths := graph.BlastRadius(g, id, graph.TraversalOptions{Kinds: kinds, MinConfidence: conf, MaxDepth: depth})
		for _, p := range paths {
			counts[p.Depth()]++
		}
		return paths
	})
	res := BlastRadiusOutput{TraversalOutput: out, ByDepth: []DepthCount{}}
	for dp, n := range counts {
		res.ByDepth = append(res.ByDepth, DepthCount{Depth: dp, Count: n})
	}
	sort.Slice(res.ByDepth, func(i, j int) bool { return res.ByDepth[i].Depth < res.ByDepth[j].Depth })
	return nil, res, err
}

// ---- get_call_graph ----

type GetCallGraphInput struct {
	Symbol        string  `json:"symbol,omitempty" jsonschema:"qualified or bare symbol name"`
	SymbolID      int64   `json:"symbol_id,omitempty" jsonschema:"exact symbol id"`
	Direction     string  `json:"direction,omitempty" jsonschema:"callees (default), callers, or both"`
	Depth         int     `json:"depth,omitempty" jsonschema:"hops to walk (default 2, max 4)"`
	MinConfidence float64 `json:"min_confidence,omitempty" jsonschema:"drop edges below this resolution confidence, 0-1"`
	Limit         int     `json:"limit,omitempty" jsonschema:"max symbols per direction (default 50, max 200)"`
}

type CallGraphOutput struct {
	Root    SymbolRef  `json:"root"`
	Callees *Traversal `json:"callees,omitempty"`
	Callers *Traversal `json:"callers,omitempty"`
	Notes   []string   `json:"notes,omitempty"`
}

func (d Deps) getCallGraph(ctx context.Context, _ *sdk.CallToolRequest, in GetCallGraphInput) (*sdk.CallToolResult, CallGraphOutput, error) {
	var out CallGraphOutput
	dir := in.Direction
	if dir == "" {
		dir = "callees"
	}
	if dir != "callees" && dir != "callers" && dir != "both" {
		return nil, out, fmt.Errorf("invalid direction %q: want callees, callers or both", in.Direction)
	}
	depth, err := bound("depth", in.Depth, defaultCallDepth, maxCallDepth, &out.Notes)
	if err != nil {
		return nil, out, err
	}
	limit, err := bound("limit", in.Limit, defaultGraphLimit, maxGraphLimit, &out.Notes)
	if err != nil {
		return nil, out, err
	}
	conf, err := parseConfidence(in.MinConfidence)
	if err != nil {
		return nil, out, err
	}
	sym, err := d.resolve(ctx, in.Symbol, in.SymbolID)
	if err != nil {
		return nil, out, err
	}
	refs, err := d.refs(ctx, []*model.Symbol{sym})
	if err != nil {
		return nil, out, err
	}
	out.Root = refs[0]
	g, err := d.graph(ctx)
	if err != nil {
		return nil, out, err
	}
	opts := graph.TraversalOptions{Kinds: []model.EdgeKind{model.EdgeKindCalls}, MinConfidence: conf, MaxDepth: depth}
	if dir == "callees" || dir == "both" {
		t, err := d.shape(ctx, graph.Callees(g, sym.ID, opts), limit)
		if err != nil {
			return nil, out, err
		}
		out.Callees = &t
	}
	if dir == "callers" || dir == "both" {
		t, err := d.shape(ctx, graph.Callers(g, sym.ID, opts), limit)
		if err != nil {
			return nil, out, err
		}
		out.Callers = &t
	}
	if !g.HasNode(sym.ID) {
		out.Notes = append(out.Notes, "symbol has no resolved edges in the graph (resolution may not have run)")
	}
	return nil, out, nil
}

// ---- find_cycles ----

type FindCyclesInput struct {
	Kinds         []string `json:"kinds,omitempty" jsonschema:"edge kinds to consider (default imports)"`
	MinConfidence float64  `json:"min_confidence,omitempty" jsonschema:"drop edges below this resolution confidence, 0-1"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max cycles, largest first (default 10, max 25)"`
}

type Cycle struct {
	Size           int         `json:"size"`
	ShortestCycle  []SymbolRef `json:"shortest_cycle"`
	Members        []SymbolRef `json:"members"`
	MembersOmitted int         `json:"members_omitted,omitempty"`
}

type FindCyclesOutput struct {
	Total     int      `json:"total"`
	Returned  int      `json:"returned"`
	Truncated bool     `json:"truncated"`
	Cycles    []Cycle  `json:"cycles"`
	Notes     []string `json:"notes,omitempty"`
}

func (d Deps) findCycles(ctx context.Context, _ *sdk.CallToolRequest, in FindCyclesInput) (*sdk.CallToolResult, FindCyclesOutput, error) {
	out := FindCyclesOutput{Cycles: []Cycle{}}
	limit, err := bound("limit", in.Limit, defaultCycleLimit, maxCycleLimit, &out.Notes)
	if err != nil {
		return nil, out, err
	}
	kinds, err := parseKinds(in.Kinds)
	if err != nil {
		return nil, out, err
	}
	conf, err := parseConfidence(in.MinConfidence)
	if err != nil {
		return nil, out, err
	}
	g, err := d.graph(ctx)
	if err != nil {
		return nil, out, err
	}
	sccs := graph.FindCycles(g, graph.TraversalOptions{Kinds: kinds, MinConfidence: conf})
	sort.Slice(sccs, func(i, j int) bool {
		if len(sccs[i].Nodes) != len(sccs[j].Nodes) {
			return len(sccs[i].Nodes) > len(sccs[j].Nodes)
		}
		return minID(sccs[i].Nodes) < minID(sccs[j].Nodes)
	})
	out.Total = len(sccs)
	if len(sccs) > limit {
		sccs, out.Truncated = sccs[:limit], true
	}

	var ids []int64
	members := make([][]int64, len(sccs))
	for i, s := range sccs {
		m := append([]int64(nil), s.Nodes...)
		sort.Slice(m, func(a, b int) bool { return m[a] < m[b] })
		if len(m) > maxCycleNodes {
			m = m[:maxCycleNodes]
		}
		members[i] = m
		ids = append(ids, m...)
		ids = append(ids, s.ShortestCycle...)
	}
	refs, err := d.refsByID(ctx, ids)
	if err != nil {
		return nil, out, err
	}
	for i, s := range sccs {
		c := Cycle{Size: len(s.Nodes), ShortestCycle: []SymbolRef{}, Members: []SymbolRef{}}
		for _, id := range s.ShortestCycle {
			c.ShortestCycle = append(c.ShortestCycle, refs[id])
		}
		for _, id := range members[i] {
			c.Members = append(c.Members, refs[id])
		}
		c.MembersOmitted = len(s.Nodes) - len(members[i])
		out.Cycles = append(out.Cycles, c)
	}
	out.Returned = len(out.Cycles)
	if out.Total == 0 {
		out.Notes = append(out.Notes, "no cycles found over the selected edge kinds")
	}
	return nil, out, nil
}

func minID(ids []int64) int64 {
	m := ids[0]
	for _, id := range ids[1:] {
		if id < m {
			m = id
		}
	}
	return m
}

// ---- registration ----

func f64(v float64) *float64 { return &v }

// tightSchema infers the input schema from T and adds numeric bounds and
// defaults, so out-of-range requests are rejected by the SDK before they
// reach a handler. Keys are JSON property names.
func tightSchema[T any](bounds map[string][3]int) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp: infer schema: %v", err))
	}
	for name, b := range bounds { // b = {default, min, max}
		p := s.Properties[name]
		if p == nil {
			panic("mcp: no property " + name)
		}
		p.Minimum, p.Maximum = f64(float64(b[1])), f64(float64(b[2]))
		p.Default = json.RawMessage(fmt.Sprint(b[0]))
	}
	return s
}

func confidenceBound(s *jsonschema.Schema) *jsonschema.Schema {
	p := s.Properties["min_confidence"]
	p.Minimum, p.Maximum = f64(0), f64(1)
	return s
}

func kindsEnum(s *jsonschema.Schema) *jsonschema.Schema {
	if p := s.Properties["kinds"]; p != nil && p.Items != nil {
		for _, k := range []model.EdgeKind{model.EdgeKindImports, model.EdgeKindCalls, model.EdgeKindInherits, model.EdgeKindImplements} {
			p.Items.Enum = append(p.Items.Enum, string(k))
		}
	}
	return s
}

// symbolTarget requires one of symbol / symbol_id.
func symbolTarget(s *jsonschema.Schema) *jsonschema.Schema {
	s.AnyOf = []*jsonschema.Schema{{Required: []string{"symbol"}}, {Required: []string{"symbol_id"}}}
	if p := s.Properties["symbol_id"]; p != nil {
		p.Minimum = f64(1)
	}
	return s
}

// Register adds the seven tools to server, backed by d.
func Register(server *sdk.Server, d Deps) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}

	sdk.AddTool(server, &sdk.Tool{
		Name:        "search_code",
		Description: "Hybrid (BM25 + embedding) search over the indexed repo. Returns ranked code chunks with truncated snippets. Use for intent or vocabulary-mismatch queries.",
		Annotations: ro,
		InputSchema: tightSchema[SearchCodeInput](map[string][3]int{
			"limit":         {defaultSearchLimit, 1, maxSearchLimit},
			"snippet_lines": {defaultSnippetLines, 1, maxSnippetLines},
		}),
	}, d.searchCode)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "find_definition",
		Description: "Locate where a symbol is defined (file, lines, signature, docstring). Errors with the candidate list if the name is ambiguous; retry with symbol_id.",
		Annotations: ro,
		InputSchema: symbolTarget(tightSchema[FindDefinitionInput](nil)),
	}, d.findDefinition)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "find_references",
		Description: "Symbols that directly reference (call, import, inherit, override) the target, with edge kind and resolution confidence.",
		Annotations: ro,
		InputSchema: kindsEnum(confidenceBound(symbolTarget(tightSchema[FindReferencesInput](map[string][3]int{
			"limit": {defaultRefLimit, 1, maxRefLimit},
		})))),
	}, d.findReferences)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_dependencies",
		Description: "What the target depends on (its imports, calls, base classes), following edges forward up to depth hops.",
		Annotations: ro,
		InputSchema: kindsEnum(confidenceBound(symbolTarget(tightSchema[GetDependenciesInput](map[string][3]int{
			"depth": {defaultDepsDepth, 1, maxDepsDepth},
			"limit": {defaultGraphLimit, 1, maxGraphLimit},
		})))),
	}, d.getDependencies)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_call_graph",
		Description: "Bounded call graph around a symbol: its callees, callers, or both, up to depth hops.",
		Annotations: ro,
		InputSchema: confidenceBound(symbolTarget(tightSchema[GetCallGraphInput](map[string][3]int{
			"depth": {defaultCallDepth, 1, maxCallDepth},
			"limit": {defaultGraphLimit, 1, maxGraphLimit},
		}))),
	}, d.getCallGraph)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_blast_radius",
		Description: "What could break if the target changes: everything that transitively imports or calls it, nearest first, with per-depth counts.",
		Annotations: ro,
		InputSchema: kindsEnum(confidenceBound(symbolTarget(tightSchema[GetBlastRadiusInput](map[string][3]int{
			"depth": {defaultBlastDepth, 1, maxBlastDepth},
			"limit": {defaultGraphLimit, 1, maxGraphLimit},
		})))),
	}, d.getBlastRadius)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "find_cycles",
		Description: "Circular dependencies (import cycles by default): each cycle's shortest loop and members, largest first.",
		Annotations: ro,
		InputSchema: kindsEnum(confidenceBound(tightSchema[FindCyclesInput](map[string][3]int{
			"limit": {defaultCycleLimit, 1, maxCycleLimit},
		}))),
	}, d.findCycles)
}
