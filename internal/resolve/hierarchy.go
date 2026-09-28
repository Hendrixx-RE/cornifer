package resolve

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func (r *resolver) info(cls *model.Symbol) *classInfo {
	ci := r.classes[cls.ID]
	if ci == nil {
		ci = &classInfo{sym: cls}
		r.classes[cls.ID] = ci
	}
	return ci
}

// onClass resolves a class's base list into EdgeKindInherits edges. Bases
// that are external, non-class, or unbindable become unresolved refs and mark
// the class as having an out-of-repo ancestor (extBase).
func (r *resolver) onClass(fi *fileInfo, n *sitter.Node, sym *model.Symbol, sc *scope) {
	ci := r.info(sym)
	sup := n.ChildByFieldName("superclasses")
	if sup == nil {
		return
	}
	src := fi.in.Result.Source
	for i := 0; i < int(sup.NamedChildCount()); i++ {
		c := sup.NamedChild(i)
		if c.Type() == "keyword_argument" || c.Type() == "comment" {
			continue
		}
		e := c
		if e.Type() == "subscript" { // Generic[T], Base[int]
			if v := e.ChildByFieldName("value"); v != nil {
				e = v
			}
		}
		name := "<expr>"
		rs := unresolvedRes
		if chain, ok := chainOf(e, src); ok {
			name = strings.Join(chain, ".")
			rs = r.resolveChain(fi, sc, chain)
			if rs.out == oResolved && lastSym(rs.syms).Kind != model.SymbolKindClass {
				rs = unresolvedRes // an in-repo name that is not a class
			}
		}
		if rs.out != oResolved {
			ci.extBase = true
		} else {
			base := lastSym(rs.syms)
			ci.bases = append(ci.bases, base)
			ci.baseConf = append(ci.baseConf, rs.conf)
			r.subclasses[base.ID] = append(r.subclasses[base.ID], sym)
			rs = res{out: oResolved, syms: []*model.Symbol{base}, conf: rs.conf}
		}
		r.emit(sym.ID, name, model.EdgeKindInherits, rs)
	}
}

// mro returns cls followed by its ancestors in C3 order over the resolved
// (in-repo) bases; on an inconsistent hierarchy it degrades to a
// left-to-right depth-first order. Best effort: unresolved bases are absent.
func (r *resolver) mro(cls *model.Symbol) []*model.Symbol {
	return r.mroRec(cls, map[int64]bool{})
}

func (r *resolver) mroRec(cls *model.Symbol, visiting map[int64]bool) []*model.Symbol {
	ci := r.info(cls)
	if ci.mroComputed {
		return ci.mro
	}
	if visiting[cls.ID] {
		return []*model.Symbol{cls}
	}
	visiting[cls.ID] = true
	seqs := make([][]*model.Symbol, 0, len(ci.bases)+1)
	for _, b := range ci.bases {
		seqs = append(seqs, r.mroRec(b, visiting))
	}
	seqs = append(seqs, append([]*model.Symbol(nil), ci.bases...))
	merged, ok := c3Merge(seqs)
	if !ok {
		seen := map[int64]bool{}
		merged = nil
		for _, s := range seqs[:len(seqs)-1] {
			for _, k := range s {
				if !seen[k.ID] {
					seen[k.ID] = true
					merged = append(merged, k)
				}
			}
		}
	}
	delete(visiting, cls.ID)
	ci.mro = append([]*model.Symbol{cls}, merged...)
	ci.mroComputed = true
	return ci.mro
}

func c3Merge(seqs [][]*model.Symbol) ([]*model.Symbol, bool) {
	seqs = append([][]*model.Symbol(nil), seqs...)
	var out []*model.Symbol
	for {
		var cand *model.Symbol
		empty := true
		for _, s := range seqs {
			if len(s) == 0 {
				continue
			}
			empty = false
			head := s[0]
			inTail := false
			for _, o := range seqs {
				for _, k := range o[min(1, len(o)):] {
					if k.ID == head.ID {
						inTail = true
					}
				}
			}
			if !inTail {
				cand = head
				break
			}
		}
		if empty {
			return out, true
		}
		if cand == nil {
			return nil, false
		}
		out = append(out, cand)
		for i, s := range seqs {
			if len(s) > 0 && s[0].ID == cand.ID {
				seqs[i] = s[1:]
			}
		}
	}
}

func (r *resolver) resetMRO() {
	for _, ci := range r.classes {
		ci.mro, ci.mroComputed = nil, false
	}
}

func (r *resolver) hasExternalBase(cls *model.Symbol) bool {
	for _, c := range r.mro(cls) {
		if r.info(c).extBase {
			return true
		}
	}
	return false
}

// memberDefs returns the symbols named name directly inside class c.
func (r *resolver) memberDefs(c *model.Symbol, name string) []*model.Symbol {
	return r.children[c.ID][name]
}

func (r *resolver) methodDefs(c *model.Symbol, name string) []*model.Symbol {
	var out []*model.Symbol
	for _, s := range r.children[c.ID][name] {
		if s.Kind == model.SymbolKindMethod {
			out = append(out, s)
		}
	}
	return out
}

// lookupMethod walks cls's MRO for a member called name. Found on cls
// itself is Exact; found on an ancestor is High (a heuristic hierarchy
// walk).
func (r *resolver) lookupMethod(cls *model.Symbol, name string, skipSelf bool) ([]*model.Symbol, model.Confidence, bool) {
	for i, c := range r.mro(cls) {
		if skipSelf && i == 0 {
			continue
		}
		if defs := r.memberDefs(c, name); len(defs) > 0 {
			if i == 0 {
				return defs, model.ConfidenceExact, true
			}
			return defs, model.ConfidenceHigh, true
		}
	}
	return nil, 0, false
}

// descendantMethods finds overrides of name on (transitive) subclasses of
// cls, for `self.name()` calls where cls itself never defines name (the
// template-method / abstract-hook pattern).
func (r *resolver) descendantMethods(cls *model.Symbol, name string) []*model.Symbol {
	var out []*model.Symbol
	seen := map[int64]bool{cls.ID: true}
	queue := []*model.Symbol{cls}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, sub := range r.subclasses[c.ID] {
			if seen[sub.ID] {
				continue
			}
			seen[sub.ID] = true
			out = append(out, r.methodDefs(sub, name)...)
			queue = append(queue, sub)
		}
	}
	return out
}

// emitImplements links each method to the method it overrides. For every
// direct base of the method's class it takes the nearest definer along that
// base's MRO, so a diamond override links to each branch's definer, and a
// deep chain links each level to the level above (graph.Callers follows
// these edges in reverse to reach overrides).
func (r *resolver) emitImplements() {
	r.resetMRO()
	ks := r.out.Stats.kind(model.EdgeKindImplements)
	for _, fi := range r.files {
		for _, m := range fi.in.Symbols {
			if m.Kind != model.SymbolKindMethod || m.ParentID == nil {
				continue
			}
			cls := r.byID[*m.ParentID]
			if cls == nil || cls.Kind != model.SymbolKindClass {
				continue
			}
			ci := r.info(cls)
			seen := map[int64]bool{}
			for i, b := range ci.bases {
				for j, k := range r.mro(b) {
					defs := r.methodDefs(k, m.Name)
					if len(defs) == 0 {
						continue
					}
					target := lastSym(defs)
					conf := model.ConfidenceHigh
					if j == 0 && ci.baseConf[i] == model.ConfidenceExact && len(defs) == 1 {
						conf = model.ConfidenceExact
					}
					if !seen[target.ID] {
						seen[target.ID] = true
						r.addEdge(m.ID, target.ID, model.EdgeKindImplements, conf)
						ks.Resolved++
					}
					break
				}
			}
		}
	}
}
