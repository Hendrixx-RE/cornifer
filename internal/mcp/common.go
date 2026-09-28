package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Output bounds. Every list-returning tool has a default and a hard cap;
// the JSON schemas advertise the same numbers as minimum/maximum.
const (
	defaultSearchLimit = 10
	maxSearchLimit     = 25

	defaultSnippetLines = 20
	maxSnippetLines     = 60
	maxSnippetBytes     = 2400

	defaultRefLimit = 25
	maxRefLimit     = 100

	defaultGraphLimit = 50
	maxGraphLimit     = 200

	defaultDepsDepth = 1
	maxDepsDepth     = 3

	defaultCallDepth = 2
	maxCallDepth     = 4

	defaultBlastDepth = 3
	maxBlastDepth     = 6

	defaultCycleLimit = 10
	maxCycleLimit     = 25

	maxCycleNodes = 20

	maxCandidatesListed = 10
)

// bound resolves a caller-supplied number against its default and cap.
// Zero means "unset" and yields def; values above max are clamped and
// reported through notes so the model knows the result was narrowed.
func bound(name string, v, def, max int, notes *[]string) (int, error) {
	switch {
	case v < 0:
		return 0, fmt.Errorf("invalid %s %d: must be between 1 and %d", name, v, max)
	case v == 0:
		return def, nil
	case v > max:
		*notes = append(*notes, fmt.Sprintf("%s clamped from %d to the maximum of %d", name, v, max))
		return max, nil
	}
	return v, nil
}

// SymbolRef is the readable form of a symbol in every tool response.
type SymbolRef struct {
	ID            int64  `json:"id"`
	QualifiedName string `json:"qualified_name,omitempty"`
	Kind          string `json:"kind,omitempty"`
	File          string `json:"file,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	EndLine       int    `json:"end_line,omitempty"`
	Signature     string `json:"signature,omitempty"`
}

func (r SymbolRef) String() string {
	name := r.QualifiedName
	if name == "" {
		name = fmt.Sprintf("symbol#%d", r.ID)
	}
	s := fmt.Sprintf("id=%d %s", r.ID, name)
	if r.Kind != "" {
		s += " (" + r.Kind + ")"
	}
	if r.File != "" {
		s += fmt.Sprintf(" %s:%d-%d", r.File, r.StartLine, r.EndLine)
	}
	return s
}

func (d Deps) refs(ctx context.Context, syms []*model.Symbol) ([]SymbolRef, error) {
	files := map[int64]*model.File{}
	if d.Catalog != nil {
		seen := map[int64]bool{}
		var ids []int64
		for _, s := range syms {
			if !seen[s.FileID] {
				seen[s.FileID] = true
				ids = append(ids, s.FileID)
			}
		}
		if len(ids) > 0 {
			var err error
			if files, err = d.Catalog.GetFiles(ctx, ids); err != nil {
				return nil, fmt.Errorf("look up files: %w", err)
			}
		}
	}
	out := make([]SymbolRef, 0, len(syms))
	for _, s := range syms {
		r := SymbolRef{ID: s.ID, QualifiedName: s.QualifiedName, Kind: string(s.Kind),
			StartLine: s.StartLine, EndLine: s.EndLine, Signature: truncateString(s.Signature, 300)}
		if f := files[s.FileID]; f != nil {
			r.File = f.Path
		}
		out = append(out, r)
	}
	return out, nil
}

// refsByID hydrates ids. IDs the catalog does not know come back as bare
// {id} refs rather than being dropped, so counts stay honest.
func (d Deps) refsByID(ctx context.Context, ids []int64) (map[int64]SymbolRef, error) {
	out := make(map[int64]SymbolRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var syms []*model.Symbol
	if d.Catalog != nil {
		m, err := d.Catalog.GetSymbols(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("look up symbols: %w", err)
		}
		for _, id := range ids {
			if s := m[id]; s != nil {
				syms = append(syms, s)
			}
		}
	}
	refs, err := d.refs(ctx, syms)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		out[r.ID] = r
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			out[id] = SymbolRef{ID: id}
		}
	}
	return out, nil
}

func describe(refs []SymbolRef) string {
	parts := make([]string, 0, maxCandidatesListed)
	for i, r := range refs {
		if i == maxCandidatesListed {
			parts = append(parts, fmt.Sprintf("... and %d more", len(refs)-i))
			break
		}
		parts = append(parts, r.String())
	}
	return strings.Join(parts, "; ")
}

// candidates finds every symbol matching name. A dotted name must equal a
// qualified name exactly; a bare name matches every symbol so named.
func (d Deps) candidates(ctx context.Context, name string) (exact, byBare []*model.Symbol, err error) {
	if d.Symbols == nil {
		return nil, nil, fmt.Errorf("symbol lookup is not configured")
	}
	bare := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		bare = name[i+1:]
	}
	if bare == "" {
		return nil, nil, fmt.Errorf("invalid symbol %q", name)
	}
	byBare, err = d.Symbols.FindSymbolsByName(ctx, d.RepoID, bare)
	if err != nil {
		return nil, nil, fmt.Errorf("find symbols named %q: %w", bare, err)
	}
	if bare == name {
		return byBare, byBare, nil
	}
	for _, s := range byBare {
		if s.QualifiedName == name {
			exact = append(exact, s)
		}
	}
	return exact, byBare, nil
}

// resolve turns tool input into exactly one symbol, or a descriptive error.
func (d Deps) resolve(ctx context.Context, name string, id int64) (*model.Symbol, error) {
	if id > 0 {
		if d.Catalog == nil {
			return nil, fmt.Errorf("symbol_id lookup is not configured")
		}
		m, err := d.Catalog.GetSymbols(ctx, []int64{id})
		if err != nil {
			return nil, fmt.Errorf("look up symbol_id %d: %w", id, err)
		}
		if s := m[id]; s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("symbol_id %d not found", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("symbol is required (or pass symbol_id)")
	}
	exact, byBare, err := d.candidates(ctx, name)
	if err != nil {
		return nil, err
	}
	switch len(exact) {
	case 1:
		return exact[0], nil
	case 0:
		msg := fmt.Sprintf("symbol not found: %q", name)
		if len(byBare) > 0 {
			refs, rerr := d.refs(ctx, byBare)
			if rerr == nil {
				msg += "; did you mean: " + describe(refs)
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}
	refs, err := d.refs(ctx, exact)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("symbol ambiguous: %d candidates for %q: %s; retry with symbol_id to choose one",
		len(exact), name, describe(refs))
}

func (d Deps) graph(ctx context.Context) (*graph.Graph, error) {
	if d.Graphs == nil {
		return nil, fmt.Errorf("graph backend is not configured")
	}
	g, err := d.Graphs.Graph(ctx)
	if err != nil {
		return nil, fmt.Errorf("load graph: %w", err)
	}
	if g == nil {
		return nil, fmt.Errorf("load graph: backend returned no graph")
	}
	return g, nil
}

func parseKinds(kinds []string) ([]model.EdgeKind, error) {
	var out []model.EdgeKind
	for _, k := range kinds {
		ek := model.EdgeKind(strings.ToLower(strings.TrimSpace(k)))
		if !ek.Valid() {
			return nil, fmt.Errorf("invalid edge kind %q: want one of imports, calls, inherits, implements", k)
		}
		out = append(out, ek)
	}
	return out, nil
}

func parseConfidence(v float64) (model.Confidence, error) {
	if v < 0 || v > 1 {
		return 0, fmt.Errorf("invalid min_confidence %v: must be between 0 and 1", v)
	}
	return model.Confidence(v), nil
}

func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// truncateSnippet limits text to maxLines lines and maxSnippetBytes bytes.
func truncateSnippet(text string, maxLines int) (string, bool) {
	truncated := false
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxSnippetBytes {
		out = truncateString(out, maxSnippetBytes)
		truncated = true
	}
	return out, truncated
}

// GraphNode is one symbol reached by a traversal, with the edge that first
// reached it. Parent is the previously visited symbol on the shortest path.
type GraphNode struct {
	SymbolRef
	Depth      int     `json:"depth"`
	Parent     int64   `json:"parent"`
	EdgeKind   string  `json:"edge_kind"`
	Confidence float64 `json:"confidence"`
}

// Traversal is a bounded traversal result. Total counts every symbol
// reached; Nodes holds at most the requested limit of them.
type Traversal struct {
	Total     int         `json:"total"`
	Returned  int         `json:"returned"`
	Truncated bool        `json:"truncated"`
	Nodes     []GraphNode `json:"nodes"`
}

// shape sorts paths (shallowest, then most confident, then lowest ID, so
// truncation is deterministic), keeps the first limit and hydrates them.
func (d Deps) shape(ctx context.Context, paths []graph.Path, limit int) (Traversal, error) {
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if a.Depth() != b.Depth() {
			return a.Depth() < b.Depth()
		}
		ca, cb := a.Edges[len(a.Edges)-1].Confidence, b.Edges[len(b.Edges)-1].Confidence
		if ca != cb {
			return ca > cb
		}
		return a.End() < b.End()
	})
	t := Traversal{Total: len(paths), Truncated: len(paths) > limit, Nodes: []GraphNode{}}
	if len(paths) > limit {
		paths = paths[:limit]
	}
	ids := make([]int64, len(paths))
	for i, p := range paths {
		ids[i] = p.End()
	}
	refs, err := d.refsByID(ctx, ids)
	if err != nil {
		return Traversal{}, err
	}
	for _, p := range paths {
		last := p.Edges[len(p.Edges)-1]
		t.Nodes = append(t.Nodes, GraphNode{
			SymbolRef:  refs[p.End()],
			Depth:      p.Depth(),
			Parent:     p.Nodes[len(p.Nodes)-2],
			EdgeKind:   string(last.Kind),
			Confidence: float64(last.Confidence),
		})
	}
	t.Returned = len(t.Nodes)
	return t, nil
}
