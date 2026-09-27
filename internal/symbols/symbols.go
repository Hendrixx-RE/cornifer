package symbols

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
)

// Extract walks res's syntax tree and returns every model.Symbol found in
// the file: exactly one SymbolKindModule symbol for the file itself, plus a
// symbol for every class, function, method, and module/class-level variable
// found by walking the module body and each class body (see doc.go for the
// full scoping contract). fileID is copied onto every returned Symbol's
// FileID field as-is — Extract does not require it to be a real,
// store-assigned ID, so callers still deciding on a File's ID may pass 0.
// moduleName becomes both the Name and QualifiedName of the module symbol
// and the root of every other symbol's QualifiedName; pass "" if it could
// not be derived (see internal/walker), in which case qualified names start
// directly from the symbol's own name.
//
// Extract does not fail on malformed input: a syntax error inside a
// def/class body may simply produce a symbol with a truncated or missing
// signature/docstring rather than an error, matching parse.Result's own
// collect-don't-abort contract.
func Extract(res *parse.Result, fileID int64, moduleName string) []*model.Symbol {
	e := &extractor{src: res.Source, fileID: fileID, nextID: 1}

	root := res.Tree.RootNode()

	moduleID := e.allocID()
	module := &model.Symbol{
		ID:            moduleID,
		FileID:        fileID,
		Kind:          model.SymbolKindModule,
		Name:          moduleName,
		QualifiedName: moduleName,
		ParentID:      nil,
		StartLine:     1,
		EndLine:       int(root.EndPoint().Row) + 1,
		Docstring:     extractDocstring(root, e.src),
	}
	e.out = append(e.out, module)

	e.walkBlock(root, &scope{kind: scopeModule, qualifiedName: moduleName, parentID: moduleID})

	return e.out
}

type scopeKind int

const (
	scopeModule scopeKind = iota
	scopeClass
)

// scope tracks the lexical context Extract is currently walking: whether
// nested function definitions become SymbolKindFunction or
// SymbolKindMethod, the dotted qualified-name prefix new symbols are
// appended to, and the temporary ID new symbols should record as their
// ParentID (see doc.go for what "temporary ID" means here).
type scope struct {
	kind          scopeKind
	qualifiedName string
	parentID      int64
}

// extractor holds the mutable state of a single Extract call: the source
// bytes symbols slice text out of, the fileID stamped onto every Symbol,
// the next temporary ID to hand out, and the accumulated output.
type extractor struct {
	src    []byte
	fileID int64
	nextID int64
	out    []*model.Symbol
}

func (e *extractor) allocID() int64 {
	id := e.nextID
	e.nextID++
	return id
}

// walkBlock visits every statement directly inside container, which must be
// either the module root node or a "block" node (both shapes expose their
// statements as direct children in the tree-sitter Python grammar).
func (e *extractor) walkBlock(container *sitter.Node, sc *scope) {
	for i := 0; i < int(container.ChildCount()); i++ {
		e.walkStatement(container.Child(i), sc)
	}
}

// walkStatement dispatches a single statement node. Definitions and
// variable assignments become Symbols; compound statements (if/for/while/
// with/try) are descended into so that, e.g., a class or function defined
// inside a top-level `if TYPE_CHECKING:` or `try/except ImportError:` block
// is still found, without changing sc (such statements do not introduce a
// new lexical scope in Python). Anything else — plain expressions, imports,
// return/pass/etc. — is ignored.
func (e *extractor) walkStatement(n *sitter.Node, sc *scope) {
	switch n.Type() {
	case "decorated_definition":
		def := n.ChildByFieldName("definition")
		if def == nil {
			return
		}
		switch def.Type() {
		case "function_definition":
			e.handleFunction(def, n, sc)
		case "class_definition":
			e.handleClass(def, n, sc)
		}
	case "function_definition":
		e.handleFunction(n, nil, sc)
	case "class_definition":
		e.handleClass(n, nil, sc)
	case "expression_statement":
		e.handleExpressionStatement(n, sc)
	case "if_statement", "for_statement", "while_statement", "with_statement", "try_statement", "match_statement":
		e.walkCompound(n, sc)
	}
}

// walkCompound descends into the suite(s) of a compound statement (the
// "block" children of an if/for/while/with/try, and the block(s) nested
// inside its elif/else/except/finally/case clauses) without changing scope.
func (e *extractor) walkCompound(n *sitter.Node, sc *scope) {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		switch c.Type() {
		case "block":
			e.walkBlock(c, sc)
		case "elif_clause", "else_clause", "except_clause", "except_group_clause", "finally_clause", "case_clause":
			e.walkCompound(c, sc)
		}
	}
}

// handleFunction records def (a "function_definition" node, sync or async —
// the "async" keyword, if present, is just part of its raw source text and
// needs no special handling here) as a SymbolKindFunction or
// SymbolKindMethod symbol, depending on sc. decorated is the enclosing
// "decorated_definition" node if def was decorated, else nil. Per
// model.Symbol's documented contract, a decorated definition's span and
// signature both start at the first decorator, not at "def".
//
// Function and method bodies are never walked for nested Symbols: a def or
// class written inside a function body is out of scope for this package
// (see doc.go).
func (e *extractor) handleFunction(def, decorated *sitter.Node, sc *scope) {
	nameNode := def.ChildByFieldName("name")
	body := def.ChildByFieldName("body")
	if nameNode == nil || body == nil {
		return
	}
	name := nameNode.Content(e.src)

	spanNode := def
	if decorated != nil {
		spanNode = decorated
	}

	kind := model.SymbolKindFunction
	if sc.kind == scopeClass {
		kind = model.SymbolKindMethod
	}

	parentID := sc.parentID
	sym := &model.Symbol{
		ID:            e.allocID(),
		FileID:        e.fileID,
		Kind:          kind,
		Name:          name,
		QualifiedName: joinQualifiedName(sc.qualifiedName, name),
		ParentID:      &parentID,
		StartLine:     int(spanNode.StartPoint().Row) + 1,
		EndLine:       int(spanNode.EndPoint().Row) + 1,
		Signature:     headerSignature(spanNode, body, e.src),
		Docstring:     extractDocstring(body, e.src),
	}
	e.out = append(e.out, sym)
}

// handleClass records def (a "class_definition" node) as a SymbolKindClass
// symbol and then walks its body for nested classes, methods, and
// class-level variables.
func (e *extractor) handleClass(def, decorated *sitter.Node, sc *scope) {
	nameNode := def.ChildByFieldName("name")
	body := def.ChildByFieldName("body")
	if nameNode == nil || body == nil {
		return
	}
	name := nameNode.Content(e.src)

	spanNode := def
	if decorated != nil {
		spanNode = decorated
	}

	qn := joinQualifiedName(sc.qualifiedName, name)
	id := e.allocID()
	parentID := sc.parentID

	sym := &model.Symbol{
		ID:            id,
		FileID:        e.fileID,
		Kind:          model.SymbolKindClass,
		Name:          name,
		QualifiedName: qn,
		ParentID:      &parentID,
		StartLine:     int(spanNode.StartPoint().Row) + 1,
		EndLine:       int(spanNode.EndPoint().Row) + 1,
		Signature:     headerSignature(spanNode, body, e.src),
		Docstring:     extractDocstring(body, e.src),
	}
	e.out = append(e.out, sym)

	e.walkBlock(body, &scope{kind: scopeClass, qualifiedName: qn, parentID: id})
}

// handleExpressionStatement records a module- or class-level assignment to a
// bare name (e.g. "X = 1", "X: int = 1", or "f = lambda: None") as a
// SymbolKindVariable symbol. Anything else an expression statement could be
// — a bare call, a tuple/attribute/subscript assignment target, an
// augmented assignment, ... — is not a symbol and is skipped.
func (e *extractor) handleExpressionStatement(n *sitter.Node, sc *scope) {
	if n.NamedChildCount() == 0 {
		return
	}
	assign := n.NamedChild(0)
	if assign.Type() != "assignment" {
		return
	}
	left := assign.ChildByFieldName("left")
	if left == nil || left.Type() != "identifier" {
		return
	}
	name := left.Content(e.src)
	parentID := sc.parentID

	sym := &model.Symbol{
		ID:            e.allocID(),
		FileID:        e.fileID,
		Kind:          model.SymbolKindVariable,
		Name:          name,
		QualifiedName: joinQualifiedName(sc.qualifiedName, name),
		ParentID:      &parentID,
		StartLine:     int(n.StartPoint().Row) + 1,
		EndLine:       int(n.EndPoint().Row) + 1,
	}
	e.out = append(e.out, sym)
}

// headerSignature returns the raw source text from the start of spanNode
// (the first decorator, if any, else the def/class keyword) through the ":"
// that terminates the header and precedes body. It returns "" if that ":"
// cannot be located (a malformed body field), rather than guessing.
func headerSignature(spanNode, body *sitter.Node, src []byte) string {
	colon := body.PrevSibling()
	if colon == nil || colon.Type() != ":" {
		return ""
	}
	start, end := spanNode.StartByte(), colon.EndByte()
	if start > end || int(end) > len(src) {
		return ""
	}
	return string(src[start:end])
}

// joinQualifiedName appends name to a dotted prefix, or returns name
// unchanged if prefix is empty (a top-level symbol in a file whose
// ModuleName could not be derived).
func joinQualifiedName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// extractDocstring returns the unindented, quote-stripped docstring of
// container (a module root node or a class/function "block" node), or "" if
// container's first statement is not a bare string-literal expression, or
// that string is an f-string (an f-string is never a docstring at runtime,
// since Python only treats a literal str as one).
func extractDocstring(container *sitter.Node, src []byte) string {
	if container.NamedChildCount() == 0 {
		return ""
	}
	first := container.NamedChild(0)
	if first.Type() != "expression_statement" || first.NamedChildCount() == 0 {
		return ""
	}
	str := first.NamedChild(0)
	if str.Type() != "string" {
		return ""
	}

	var start, end *sitter.Node
	for i := 0; i < int(str.ChildCount()); i++ {
		c := str.Child(i)
		switch c.Type() {
		case "string_start":
			start = c
		case "string_end":
			end = c
		case "interpolation":
			return "" // f-string: not a static docstring.
		}
	}
	if start == nil || end == nil || start.EndByte() > end.StartByte() {
		return ""
	}

	return dedent(string(src[start.EndByte():end.StartByte()]))
}

// dedent implements the same normalization Python applies to __doc__ (see
// inspect.cleandoc / PEP 257): the first line is stripped of trailing
// whitespace as-is; every subsequent line has the minimum common leading
// whitespace (ignoring blank lines) removed, then trailing whitespace
// stripped; and leading/trailing blank lines are dropped from the result.
func dedent(raw string) string {
	lines := strings.Split(raw, "\n")

	minIndent := -1
	for _, l := range lines[1:] {
		trimmed := strings.TrimLeft(l, " \t")
		if trimmed == "" {
			continue
		}
		indent := len(l) - len(trimmed)
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent == -1 {
		minIndent = 0
	}

	out := make([]string, len(lines))
	out[0] = strings.TrimRight(lines[0], " \t")
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		if len(l) >= minIndent {
			l = l[minIndent:]
		} else {
			l = strings.TrimLeft(l, " \t")
		}
		out[i] = strings.TrimRight(l, " \t")
	}

	start := 0
	for start < len(out) && out[start] == "" {
		start++
	}
	end := len(out)
	for end > start && out[end-1] == "" {
		end--
	}

	return strings.Join(out[start:end], "\n")
}
