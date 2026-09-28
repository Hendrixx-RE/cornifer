package resolve

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// scope is one lexical scope on the walker's stack. Scopes exist only for
// defs/classes that internal/symbols extracted as symbols; a def nested in a
// function body that has no symbol contributes no scope, so its calls are
// attributed to the nearest enclosing symbol. When such nested symbols start
// being extracted, they are picked up automatically by matchDef.
type scope struct {
	sym      *model.Symbol
	parent   *scope
	locals   *localSet     // names local to a function scope; nil for module/class scopes
	ctors    *localSet     // single-assignment constructor bindings visible for variable lookup
	class    *model.Symbol // class whose instance `selfName` refers to
	selfName string
}

type (
	classVisitor func(fi *fileInfo, n *sitter.Node, sym *model.Symbol, sc *scope)
	callVisitor  func(fi *fileInfo, n *sitter.Node, sc *scope)
)

func (r *resolver) walkFile(fi *fileInfo, onClass classVisitor, onCall callVisitor) {
	src := fi.in.Result.Source
	var visit func(n *sitter.Node, sc *scope)
	visitChildren := func(n *sitter.Node, sc *scope) {
		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i), sc)
		}
	}
	visit = func(n *sitter.Node, sc *scope) {
		switch n.Type() {
		case "class_definition":
			sym := r.matchDef(fi, n)
			if sym == nil && sc.locals != nil {
				if nm := n.ChildByFieldName("name"); nm != nil {
					sc.locals.add(nm.Content(src))
				}
			}
			if sup := n.ChildByFieldName("superclasses"); sup != nil {
				visitChildren(sup, sc)
			}
			if onClass != nil && sym != nil {
				onClass(fi, n, sym, sc)
			}
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			if sym != nil {
				visitChildren(body, &scope{sym: sym, parent: sc})
			} else {
				visitChildren(body, sc)
			}
			return
		case "function_definition":
			sym := r.matchDef(fi, n)
			if p := n.ChildByFieldName("parameters"); p != nil {
				visit(p, sc)
			}
			if rt := n.ChildByFieldName("return_type"); rt != nil {
				visit(rt, sc)
			}
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			if sym == nil {
				if sc.locals != nil {
					if nm := n.ChildByFieldName("name"); nm != nil {
						sc.locals.add(nm.Content(src))
					}
					collectParams(n.ChildByFieldName("parameters"), src, sc.locals)
					collectBodyLocals(body, src, sc.locals)
				}
				visitChildren(body, sc)
				return
			}
			ns := &scope{sym: sym, parent: sc, locals: newLocalSet()}
			ns.ctors = ns.locals
			collectParams(n.ChildByFieldName("parameters"), src, ns.locals)
			collectBodyLocals(body, src, ns.locals)
			if sym.Kind == model.SymbolKindMethod && sc.sym.Kind == model.SymbolKindClass {
				ns.class = sc.sym
				ns.selfName = firstParam(n.ChildByFieldName("parameters"), src)
			} else {
				ns.class, ns.selfName = sc.class, sc.selfName
			}
			visitChildren(body, ns)
			return
		case "call":
			if onCall != nil {
				onCall(fi, n, sc)
			}
		}
		visitChildren(n, sc)
	}
	rootNode := fi.in.Result.Tree.RootNode()
	modLocals := newLocalSet()
	collectBodyLocals(rootNode, src, modLocals)
	visit(rootNode, &scope{sym: fi.module, ctors: modLocals})
}

// matchDef finds the symbol extracted for a class/function definition node:
// same name, span covering the node (a symbol's span includes decorators),
// preferring the tightest such span.
func (r *resolver) matchDef(fi *fileInfo, n *sitter.Node) *model.Symbol {
	nm := n.ChildByFieldName("name")
	if nm == nil {
		return nil
	}
	start, end := int(n.StartPoint().Row)+1, int(n.EndPoint().Row)+1
	var best *model.Symbol
	for _, s := range fi.byName[nm.Content(fi.in.Result.Source)] {
		switch s.Kind {
		case model.SymbolKindClass, model.SymbolKindFunction, model.SymbolKindMethod:
		default:
			continue
		}
		if s.StartLine > start || s.EndLine < end {
			continue
		}
		if best == nil || s.EndLine-s.StartLine < best.EndLine-best.StartLine {
			best = s
		}
	}
	return best
}

// localSet tracks names bound in a scope and how often, so a name bound
// exactly once by `x = Ctor(...)` can be treated as an instance of Ctor.
type localSet struct {
	count map[string]int
	ctor  map[string][]string
}

func newLocalSet() *localSet {
	return &localSet{count: map[string]int{}, ctor: map[string][]string{}}
}

func (l *localSet) add(name string) {
	if l != nil {
		l.count[name]++
	}
}

func (l *localSet) has(name string) bool { return l != nil && l.count[name] > 0 }

// ctorOf returns the constructor call chain for name, if it is bound exactly
// once and that binding is `name = <chain>(...)`.
func (l *localSet) ctorOf(name string) ([]string, bool) {
	if l == nil || l.count[name] != 1 {
		return nil, false
	}
	c, ok := l.ctor[name]
	return c, ok
}

func paramName(c *sitter.Node, src []byte) string {
	switch c.Type() {
	case "identifier":
		return c.Content(src)
	case "default_parameter", "typed_default_parameter":
		if nm := c.ChildByFieldName("name"); nm != nil {
			return nm.Content(src)
		}
	case "typed_parameter", "list_splat_pattern", "dictionary_splat_pattern":
		if c.NamedChildCount() > 0 {
			f := c.NamedChild(0)
			if f.Type() == "identifier" {
				return f.Content(src)
			}
			return paramName(f, src)
		}
	}
	return ""
}

func firstParam(params *sitter.Node, src []byte) string {
	if params == nil || params.NamedChildCount() == 0 {
		return ""
	}
	return paramName(params.NamedChild(0), src)
}

func collectParams(params *sitter.Node, src []byte, into *localSet) {
	if params == nil {
		return
	}
	for i := 0; i < int(params.NamedChildCount()); i++ {
		if n := paramName(params.NamedChild(i), src); n != "" {
			into.add(n)
		}
	}
}

func collectTargets(n *sitter.Node, src []byte, into *localSet) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "identifier":
		into.add(n.Content(src))
	case "pattern_list", "tuple_pattern", "list_pattern", "tuple", "list", "parenthesized_expression",
		"list_splat_pattern", "as_pattern_target", "expression_list":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			collectTargets(n.NamedChild(i), src, into)
		}
	}
}

// collectBodyLocals records names bound by assignment-like statements in a
// function body, without descending into nested defs/classes/lambdas (whose
// own names are recorded but whose bodies are separate scopes). Imports are
// deliberately not recorded: they bind file-wide in this flow-insensitive
// model.
func collectBodyLocals(n *sitter.Node, src []byte, into *localSet) {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		switch c.Type() {
		case "function_definition", "class_definition":
			if nm := c.ChildByFieldName("name"); nm != nil {
				into.add(nm.Content(src))
			}
			continue
		case "lambda":
			continue
		case "assignment", "augmented_assignment", "for_statement", "for_in_clause":
			left := c.ChildByFieldName("left")
			if c.Type() == "assignment" && left != nil && left.Type() == "identifier" {
				if right := c.ChildByFieldName("right"); right != nil && right.Type() == "call" {
					if fn := right.ChildByFieldName("function"); fn != nil {
						if chain, ok := chainOf(fn, src); ok && chain[0] != left.Content(src) {
							into.ctor[left.Content(src)] = chain
						}
					}
				}
			}
			collectTargets(left, src, into)
		case "named_expression":
			collectTargets(c.ChildByFieldName("name"), src, into)
		case "as_pattern":
			if c.NamedChildCount() > 0 {
				collectTargets(c.NamedChild(int(c.NamedChildCount())-1), src, into)
			}
		}
		collectBodyLocals(c, src, into)
	}
}

// chainOf flattens `a.b.c` into ["a","b","c"]. ok is false when any link is
// not a plain identifier/attribute (a call, subscript, ...).
func chainOf(n *sitter.Node, src []byte) ([]string, bool) {
	switch n.Type() {
	case "identifier":
		return []string{n.Content(src)}, true
	case "attribute":
		obj, attr := n.ChildByFieldName("object"), n.ChildByFieldName("attribute")
		if obj == nil || attr == nil {
			return nil, false
		}
		head, ok := chainOf(obj, src)
		if !ok {
			return nil, false
		}
		return append(head, attr.Content(src)), true
	}
	return nil, false
}
