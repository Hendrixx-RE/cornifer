package chunk

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
)

// Chunker turns one file's parsed symbols into retrieval-ready Chunks. file
// and symbols must belong to the same File (symbols is every model.Symbol
// internal/symbols extracted from it, in any order); source is the file's
// raw bytes, needed to slice out each chunk's text by line range.
type Chunker interface {
	Chunk(ctx context.Context, file *model.File, source []byte, symbols []*model.Symbol) ([]*model.Chunk, error)
}

// Options are the token budgets, all measured with CountTokens.
type Options struct {
	// MaxTokens is the target ceiling for ContextHeader+Text. A region larger
	// than this is split at statement boundaries; a single statement larger
	// than this stays whole (never split mid-statement).
	MaxTokens int
	// ClassWholeTokens: a class whose full text is at most this stays one chunk;
	// bigger classes become a header chunk plus per-member chunks.
	ClassWholeTokens int
	// MergeTokens: adjacent sibling symbol chunks are merged while the
	// combined Text stays at most this.
	MergeTokens int
}

// DefaultOptions are the budgets New uses.
func DefaultOptions() Options {
	return Options{MaxTokens: 512, ClassWholeTokens: 256, MergeTokens: 160}
}

// New returns the AST-aware Chunker with DefaultOptions.
func New() Chunker { return NewWithOptions(DefaultOptions()) }

// NewWithOptions returns a Chunker with explicit budgets; non-positive
// fields take their default.
func NewWithOptions(o Options) Chunker {
	d := DefaultOptions()
	if o.MaxTokens <= 0 {
		o.MaxTokens = d.MaxTokens
	}
	if o.ClassWholeTokens <= 0 {
		o.ClassWholeTokens = d.ClassWholeTokens
	}
	if o.MergeTokens <= 0 {
		o.MergeTokens = d.MergeTokens
	}
	return &chunker{opts: o, parser: parse.New()}
}

// EmbeddingText is the exact string an Embedder should receive for c, and
// the string Chunk.TokenCount was measured on.
func EmbeddingText(c *model.Chunk) string {
	if c.ContextHeader == "" {
		return c.Text
	}
	return c.ContextHeader + "\n" + c.Text
}

type chunker struct {
	opts   Options
	parser parse.Parser
}

// seg is a contiguous, 1-indexed inclusive line range that will become one
// or more chunks.
type seg struct {
	start, end int
	owner      *model.Symbol // nil: module-level code
	leaf       bool          // an unsplit symbol region eligible for merging
	sigs       []string
	syms       []*model.Symbol // symbols merged into this seg (for header)
}

type build struct {
	c        *chunker
	file     *model.File
	lines    []string
	prefix   []int // prefix[i] = tokens of lines[0..i-1]
	an       *analysis
	all      []*model.Symbol // non-module symbols
	byID     map[int64]*model.Symbol
	children map[int64][]*model.Symbol
	segs     []*seg
}

func (b *build) tokens(start, end int) int { return b.prefix[end] - b.prefix[start-1] }

func (c *chunker) Chunk(ctx context.Context, file *model.File, source []byte, symbols []*model.Symbol) ([]*model.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text := string(source)
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	n := len(lines)

	b := &build{c: c, file: file, lines: lines, prefix: make([]int, n+1),
		byID: map[int64]*model.Symbol{}, children: map[int64][]*model.Symbol{}}
	for i, l := range lines {
		b.prefix[i+1] = b.prefix[i] + CountTokens(l)
	}

	res, err := c.parser.Parse(ctx, file.Path, source)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		b.an = &analysis{cuts: map[int]int{}}
	} else {
		b.an = analyze(res.Tree.RootNode(), source)
		res.Tree.Close()
	}

	var moduleID int64 = -1
	for _, s := range symbols {
		if s.Kind == model.SymbolKindModule {
			moduleID = s.ID
			b.byID[s.ID] = s
		}
	}
	for _, s := range symbols {
		if s.Kind == model.SymbolKindModule {
			continue
		}
		// Clamp ranges into the file; drop unusable symbols.
		cp := *s
		if cp.StartLine < 1 {
			cp.StartLine = 1
		}
		if cp.EndLine > n {
			cp.EndLine = n
		}
		if cp.StartLine > cp.EndLine {
			continue
		}
		b.all = append(b.all, &cp)
		b.byID[cp.ID] = &cp
	}
	for _, s := range b.all {
		pid := moduleID
		if s.ParentID != nil {
			if _, ok := b.byID[*s.ParentID]; ok {
				pid = *s.ParentID
			}
		}
		b.children[pid] = append(b.children[pid], s)
	}

	b.carve(nil, 1, n, moduleID)
	b.absorbBlank()
	out, err := b.emit(ctx, file)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func structural(k model.SymbolKind) bool {
	return k == model.SymbolKindClass || k == model.SymbolKindFunction || k == model.SymbolKindMethod
}

// kids returns the non-overlapping structural children of container id,
// ordered by position. Children reached only through a function body are
// never returned: functions are leaves, so any def/class nested in one
// stays inside its parent's chunk.
func (b *build) kids(id int64) []*model.Symbol {
	var ks []*model.Symbol
	for _, s := range b.children[id] {
		if structural(s.Kind) {
			ks = append(ks, s)
		}
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if ks[i].StartLine != ks[j].StartLine {
			return ks[i].StartLine < ks[j].StartLine
		}
		return ks[i].EndLine > ks[j].EndLine
	})
	var out []*model.Symbol
	last := 0
	for _, k := range ks {
		if k.StartLine > last {
			out = append(out, k)
			last = k.EndLine
		}
	}
	return out
}

// carve partitions lines [start,end] of container (nil = module) into
// segments: one per structural child, plus gap segments between them.
func (b *build) carve(container *model.Symbol, start, end int, containerID int64) {
	gap := func(s, e int) {
		if s <= e {
			b.segs = append(b.segs, &seg{start: s, end: e, owner: container})
		}
	}
	cur := start
	for _, k := range b.kids(containerID) {
		if k.StartLine < cur || k.EndLine > end {
			continue
		}
		gap(cur, k.StartLine-1)
		b.symbolSegs(k)
		cur = k.EndLine + 1
	}
	gap(cur, end)
}

func (b *build) symbolSegs(s *model.Symbol) {
	if s.Kind == model.SymbolKindClass && b.tokens(s.StartLine, s.EndLine) > b.c.opts.ClassWholeTokens && len(b.kids(s.ID)) > 0 {
		b.carve(s, s.StartLine, s.EndLine, s.ID)
		return
	}
	b.segs = append(b.segs, &seg{start: s.StartLine, end: s.EndLine, owner: s, leaf: true,
		sigs: []string{s.Signature}, syms: []*model.Symbol{s}})
}

func (b *build) blank(sg *seg) bool {
	for i := sg.start; i <= sg.end; i++ {
		if strings.TrimSpace(b.lines[i-1]) != "" {
			return false
		}
	}
	return true
}

// okRange reports whether a chunk over [start,end] partially overlaps no
// symbol (each symbol must contain it, sit inside it, or be disjoint).
func (b *build) okRange(start, end int) bool {
	for _, s := range b.all {
		disjoint := end < s.StartLine || start > s.EndLine
		inside := start >= s.StartLine && end <= s.EndLine
		contains := start <= s.StartLine && end >= s.EndLine
		if !disjoint && !inside && !contains {
			return false
		}
	}
	return true
}

// absorbBlank folds whitespace-only gap segments into a neighbour when doing
// so crosses no symbol boundary, so blank lines do not become junk chunks.
// Where neither neighbour qualifies (e.g. blank lines between a nested
// class's last member and the next top-level def) the blank gap stays its
// own chunk, keeping every line covered.
func (b *build) absorbBlank() {
	var out []*seg
	var pending *seg
	for _, sg := range b.segs {
		if !sg.leaf && b.blank(sg) {
			if n := len(out); n > 0 && b.okRange(out[n-1].start, sg.end) {
				out[n-1].end = sg.end
				continue
			}
			if pending != nil { // consecutive stuck blanks
				out = append(out, pending)
			}
			pending = sg
			continue
		}
		if pending != nil {
			if b.okRange(pending.start, sg.end) {
				sg.start = pending.start
			} else {
				out = append(out, pending)
			}
			pending = nil
		}
		out = append(out, sg)
	}
	if pending != nil {
		out = append(out, pending)
	}
	b.segs = out
}

// merge joins adjacent unsplit sibling symbol segs while they stay tiny.
func (b *build) merge() {
	var out []*seg
	for _, sg := range b.segs {
		if n := len(out); n > 0 {
			p := out[n-1]
			if p.leaf && sg.leaf && p.end+1 == sg.start && sameParent(p.owner, sg.owner) &&
				b.tokens(p.start, sg.end) <= b.c.opts.MergeTokens {
				p.end = sg.end
				p.sigs = append(p.sigs, sg.sigs...)
				p.syms = append(p.syms, sg.syms...)
				continue
			}
		}
		out = append(out, sg)
	}
	b.segs = out
}

func sameParent(a, b *model.Symbol) bool {
	if a.ParentID == nil || b.ParentID == nil {
		return a.ParentID == b.ParentID
	}
	return *a.ParentID == *b.ParentID
}

func (b *build) emit(ctx context.Context, file *model.File) ([]*model.Chunk, error) {
	b.merge()
	var out []*model.Chunk
	for _, sg := range b.segs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hdr := b.header(sg, "")
		budget := b.c.opts.MaxTokens - CountTokens(hdr)
		if min := b.c.opts.MaxTokens / 4; budget < min {
			budget = min
		}
		pieces := b.split(sg, budget)
		for i, p := range pieces {
			h := hdr
			if len(pieces) > 1 {
				h = b.header(sg, fmt.Sprintf("part %d of %d", i+1, len(pieces)))
			}
			text := strings.Join(b.lines[p[0]-1:p[1]], "\n")
			ch := &model.Chunk{
				FileID:        file.ID,
				StartLine:     p[0],
				EndLine:       p[1],
				Text:          text,
				ContextHeader: h,
			}
			if len(sg.syms) > 0 {
				id := sg.syms[0].ID
				ch.SymbolID = &id
			} else if sg.owner != nil {
				id := sg.owner.ID
				ch.SymbolID = &id
			}
			ch.TokenCount = CountTokens(EmbeddingText(ch))
			out = append(out, ch)
		}
	}
	return out, nil
}

// split cuts sg at statement boundaries so each piece's Text fits budget
// where possible. It only ever cuts on lines the AST reports as the start of
// a statement, and never inside another symbol's range.
func (b *build) split(sg *seg, budget int) [][2]int {
	if b.tokens(sg.start, sg.end) <= budget {
		return [][2]int{{sg.start, sg.end}}
	}
	var cands []int
	for l := sg.start + 1; l <= sg.end; l++ {
		if _, ok := b.an.cuts[l]; ok && b.legalCut(l, sg) {
			cands = append(cands, l)
		}
	}
	var out [][2]int
	start := sg.start
	for start <= sg.end {
		if b.tokens(start, sg.end) <= budget {
			out = append(out, [2]int{start, sg.end})
			break
		}
		var fit []int
		next := -1
		for _, c := range cands {
			if c <= start {
				continue
			}
			if next == -1 {
				next = c
			}
			if b.tokens(start, c-1) <= budget {
				fit = append(fit, c)
			}
		}
		if next == -1 { // one unsplittable statement remains
			out = append(out, [2]int{start, sg.end})
			break
		}
		cut := next
		if len(fit) > 0 {
			// Prefer the shallowest boundary that still fills at least half
			// the budget; among equals take the furthest.
			cut = fit[len(fit)-1]
			best := -1
			for _, c := range fit {
				if b.tokens(start, c-1) < budget/2 {
					continue
				}
				if d := b.an.cuts[c]; best == -1 || d <= best {
					best, cut = d, c
				}
			}
		}
		out = append(out, [2]int{start, cut - 1})
		start = cut
	}
	return out
}

// legalCut reports whether cutting before line l stays clear of every symbol
// that does not enclose the whole seg.
func (b *build) legalCut(l int, sg *seg) bool {
	for _, s := range b.all {
		if s.StartLine < l && l <= s.EndLine && !(s.StartLine <= sg.start && s.EndLine >= sg.end) {
			return false
		}
	}
	return true
}

func (b *build) header(sg *seg, part string) string {
	var h strings.Builder
	h.WriteString("file: " + b.file.Path)
	var first *model.Symbol
	if len(sg.syms) > 0 {
		first = sg.syms[0]
	} else {
		first = sg.owner
	}
	var text string
	if first == nil {
		h.WriteString("\nscope: module-level code")
		text = strings.Join(b.lines[sg.start-1:sg.end], "\n")
	} else {
		if len(sg.syms) > 1 {
			names := make([]string, len(sg.syms))
			for i, s := range sg.syms {
				names[i] = s.QualifiedName
			}
			h.WriteString("\nsymbols: " + strings.Join(names, ", "))
		} else {
			h.WriteString("\nsymbol: " + first.QualifiedName)
		}
		if classes := b.enclosingClasses(first, len(sg.syms) == 0); classes != "" {
			h.WriteString("\nclass: " + classes)
		}
		text = strings.Join(b.lines[sg.start-1:sg.end], "\n")
		if len(sg.syms) == 0 {
			h.WriteString("\nscope: class body (signature, docstring, attributes)")
		}
	}
	if imps := b.usedImports(text); len(imps) > 0 {
		h.WriteString("\nimports: " + strings.Join(imps, "; "))
	}
	for _, s := range sg.sigs {
		if s != "" {
			h.WriteString("\nsignature: " + s)
		}
	}
	if len(sg.sigs) == 0 && first != nil && first.Signature != "" {
		h.WriteString("\nsignature: " + first.Signature)
	}
	if part != "" {
		h.WriteString("\n" + part)
	}
	return h.String()
}

// enclosingClasses returns the dotted names of the class scopes s lives in.
// includeSelf adds s itself when it is a class (used for class-body segs).
func (b *build) enclosingClasses(s *model.Symbol, includeSelf bool) string {
	var names []string
	if s.Kind == model.SymbolKindClass && includeSelf {
		names = append(names, s.Name)
	}
	cur := s
	for i := 0; i < 64 && cur.ParentID != nil; i++ {
		p, ok := b.byID[*cur.ParentID]
		if !ok || p.Kind == model.SymbolKindModule {
			break
		}
		if p.Kind == model.SymbolKindClass {
			names = append([]string{p.Name}, names...)
		}
		cur = p
	}
	return strings.Join(names, ".")
}

const maxHeaderImports = 12

// usedImports returns the file's imports whose bound names occur in text.
func (b *build) usedImports(text string) []string {
	if len(b.an.imports) == 0 {
		return nil
	}
	words := map[string]bool{}
	start := -1
	for i, r := range text + " " {
		word := r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
		if word && start < 0 {
			start = i
		} else if !word && start >= 0 {
			words[text[start:i]] = true
			start = -1
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, imp := range b.an.imports {
		for _, nm := range imp.names {
			if words[nm] && !seen[imp.text] {
				seen[imp.text] = true
				out = append(out, imp.text)
				break
			}
		}
		if len(out) == maxHeaderImports {
			break
		}
	}
	return out
}
