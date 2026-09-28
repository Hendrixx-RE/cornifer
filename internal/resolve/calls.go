package resolve

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// maxFallbackCandidates bounds the name-only fallback: a method name shared
// by more repo classes than this carries no signal and stays unresolved.
const maxFallbackCandidates = 5

type outcome int

const (
	oUnresolved outcome = iota
	oResolved
	oExternal
)

type res struct {
	out  outcome
	syms []*model.Symbol
	conf model.Confidence
}

var (
	unresolvedRes = res{}
	externalRes   = res{out: oExternal}
)

// resolved builds a resolution from same-scope candidates. Several
// candidates under one name (overload stubs, conditional redefinitions) are
// collapsed to the last definition — Python's last-binding-wins — at
// reduced confidence.
func resolved(syms []*model.Symbol, conf model.Confidence) res {
	if len(syms) > 1 {
		last := syms[0]
		for _, s := range syms[1:] {
			if s.StartLine >= last.StartLine {
				last = s
			}
		}
		syms, conf = []*model.Symbol{last}, min(conf, model.ConfidenceHigh)
	}
	return res{out: oResolved, syms: syms, conf: conf}
}

func lastSym(syms []*model.Symbol) *model.Symbol { return resolved(syms, 1).syms[0] }

func (r *resolver) onCall(fi *fileInfo, n *sitter.Node, sc *scope) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	src := fi.in.Result.Source
	var (
		rs   res
		name string
	)
	switch fn.Type() {
	case "identifier":
		name = fn.Content(src)
		rs = r.resolveChain(fi, sc, []string{name})
	case "attribute":
		attr := fn.ChildByFieldName("attribute")
		obj := fn.ChildByFieldName("object")
		if attr == nil || obj == nil {
			return
		}
		a := attr.Content(src)
		if isSuperCall(obj, src) {
			name, rs = "super()."+a, r.resolveSuper(sc, a)
			break
		}
		if chain, ok := chainOf(fn, src); ok {
			name, rs = strings.Join(chain, "."), r.resolveChain(fi, sc, chain)
		} else {
			name, rs = "?."+a, r.fallbackByName(a)
		}
	default:
		name, rs = "<dynamic>", unresolvedRes
	}
	r.emit(sc.sym.ID, name, model.EdgeKindCalls, rs)
}

func isSuperCall(n *sitter.Node, src []byte) bool {
	if n.Type() != "call" {
		return false
	}
	f := n.ChildByFieldName("function")
	return f != nil && f.Type() == "identifier" && f.Content(src) == "super"
}

func (r *resolver) emit(src int64, name string, kind model.EdgeKind, rs res) {
	ks := r.out.Stats.kind(kind)
	switch rs.out {
	case oResolved:
		ks.Resolved++
		for _, s := range rs.syms {
			r.addEdge(src, s.ID, kind, rs.conf)
		}
	case oExternal:
		ks.External++
		r.addUnresolved(src, name, kind, true)
	default:
		ks.Unresolved++
		r.addUnresolved(src, name, kind, false)
	}
}

// lookupRoot resolves the first identifier of a reference: function locals,
// enclosing lexical scopes (class scopes are only visible from their own
// body), import bindings, star imports, then builtins.
func (r *resolver) lookupRoot(fi *fileInfo, sc *scope, name string) value {
	for s := sc; s != nil; s = s.parent {
		if s.sym.Kind == model.SymbolKindClass && s != sc {
			continue
		}
		if s.locals.has(name) {
			ch, _ := s.locals.ctorOf(name)
			return value{kind: vkLocal, ctor: ch}
		}
		if syms := r.children[s.sym.ID][name]; len(syms) > 0 {
			if ch, ok := s.ctors.ctorOf(name); ok && s.locals == nil && allVariables(syms) {
				return value{kind: vkLocal, ctor: ch}
			}
			return symsValue(syms, model.ConfidenceExact)
		}
	}
	if b := fi.bindings[name]; b != nil {
		return r.resolveBinding(b, map[string]bool{})
	}
	sawExternal := false
	for _, s := range fi.stars {
		if s.external {
			sawExternal = true
			continue
		}
		if v := r.resolveExport(s.modName, name, map[string]bool{}); v.kind == vkSyms || v.kind == vkModule {
			v.conf = min(v.conf, model.ConfidenceHigh)
			return v
		}
	}
	if sawExternal || builtinNames[name] {
		return value{kind: vkExternal}
	}
	return value{}
}

func allVariables(syms []*model.Symbol) bool {
	for _, s := range syms {
		if s.Kind != model.SymbolKindVariable {
			return false
		}
	}
	return true
}

func (r *resolver) resolveChain(fi *fileInfo, sc *scope, chain []string) res {
	root, tail := chain[0], chain[len(chain)-1]
	if len(chain) >= 2 && sc.class != nil && sc.selfName != "" && root == sc.selfName {
		return r.resolveSelf(sc.class, chain[1:])
	}
	v := r.lookupRoot(fi, sc, root)
	if len(chain) == 1 {
		switch v.kind {
		case vkSyms:
			return resolved(v.syms, v.conf)
		case vkExternal:
			return externalRes
		}
		return unresolvedRes
	}
	switch v.kind {
	case vkExternal:
		return externalRes
	case vkLocal:
		if v.ctor != nil && r.ctorDepth < 3 {
			r.ctorDepth++
			inst := r.resolveChain(fi, sc, v.ctor)
			r.ctorDepth--
			switch {
			case inst.out == oExternal:
				return externalRes // instance of an out-of-repo class
			case inst.out == oResolved && lastSym(inst.syms).Kind == model.SymbolKindClass:
				return r.resolveClassAttr(lastSym(inst.syms), chain[1:], inst.conf)
			}
		}
		return r.fallbackByName(tail)
	case vkSyms:
		if last := lastSym(v.syms); last.Kind == model.SymbolKindClass {
			return r.resolveClassAttr(last, chain[1:], v.conf)
		}
		return r.fallbackByName(tail)
	case vkModule:
		return r.resolveModuleChain(v.modName, chain[1:])
	}
	return unresolvedRes
}

func (r *resolver) resolveSelf(cls *model.Symbol, rest []string) res {
	if len(rest) > 1 {
		return r.fallbackByName(rest[len(rest)-1])
	}
	if syms, conf, ok := r.lookupMethod(cls, rest[0], false); ok {
		return resolved(syms, conf)
	}
	if d := r.descendantMethods(cls, rest[0]); len(d) > 0 {
		conf := model.ConfidenceMedium
		if len(d) > maxFallbackCandidates {
			conf = model.ConfidenceLow
		}
		return res{out: oResolved, syms: d, conf: conf}
	}
	if r.hasExternalBase(cls) {
		return externalRes
	}
	return unresolvedRes
}

func (r *resolver) resolveSuper(sc *scope, name string) res {
	cls := sc.class
	if cls == nil {
		return unresolvedRes
	}
	ci := r.info(cls)
	if syms, _, ok := r.lookupMethod(cls, name, true); ok {
		conf := model.ConfidenceHigh
		if len(ci.bases) == 1 && !ci.extBase && syms[0].ParentID != nil && *syms[0].ParentID == ci.bases[0].ID {
			conf = model.ConfidenceExact
		}
		return resolved(syms, conf)
	}
	if ci.extBase || len(ci.bases) == 0 {
		return externalRes // inherited from object or an out-of-repo base
	}
	return unresolvedRes
}

func (r *resolver) resolveClassAttr(cls *model.Symbol, rest []string, conf model.Confidence) res {
	if len(rest) == 1 {
		if syms, c, ok := r.lookupMethod(cls, rest[0], false); ok {
			return resolved(syms, min(conf, c))
		}
		if r.hasExternalBase(cls) {
			return externalRes
		}
		return unresolvedRes
	}
	if defs := r.memberDefs(cls, rest[0]); len(defs) > 0 {
		if inner := lastSym(defs); inner.Kind == model.SymbolKindClass {
			return r.resolveClassAttr(inner, rest[1:], conf)
		}
	}
	return r.fallbackByName(rest[len(rest)-1])
}

// resolveModuleChain resolves `mod.sub.name[.attr]` starting from an
// imported module, descending through submodules and re-exports.
func (r *resolver) resolveModuleChain(modName string, rest []string) res {
	cur := modName
	for i, a := range rest {
		last := i == len(rest)-1
		v := r.resolveExport(cur, a, map[string]bool{})
		switch v.kind {
		case vkModule:
			if last {
				return unresolvedRes
			}
			cur = v.modName
		case vkSyms:
			if last {
				return resolved(v.syms, v.conf)
			}
			if c := lastSym(v.syms); c.Kind == model.SymbolKindClass {
				return r.resolveClassAttr(c, rest[i+1:], v.conf)
			}
			return r.fallbackByName(rest[len(rest)-1])
		case vkExternal:
			return externalRes
		default:
			return unresolvedRes
		}
	}
	return unresolvedRes
}

// fallbackByName is the last resort for `receiver.name()` when the
// receiver's type is unknown: link to every repo method called name, at
// Medium confidence for a unique candidate and Low for several.
func (r *resolver) fallbackByName(name string) res {
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return unresolvedRes
	}
	c := r.methodsByName[name]
	switch {
	case len(c) == 1:
		return res{out: oResolved, syms: c, conf: model.ConfidenceMedium}
	case len(c) > 1 && len(c) <= maxFallbackCandidates:
		return res{out: oResolved, syms: c, conf: model.ConfidenceLow}
	}
	return unresolvedRes
}
