package chunk

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// importInfo is one module-/class-level import statement: its whitespace-
// normalized source and the local names it binds.
type importInfo struct {
	text  string
	names []string
}

// analysis is the AST-derived data the chunker needs beyond symbols: legal
// statement-boundary cut lines and the file's imports.
type analysis struct {
	// cuts maps a 1-indexed line L to the nesting depth of the statement
	// beginning there, for every L where cutting "before line L" falls
	// between two statements of the same block.
	cuts    map[int]int
	imports []importInfo
}

func analyze(root *sitter.Node, src []byte) *analysis {
	a := &analysis{cuts: map[int]int{}}
	a.walkBlock(root, src, 0)
	a.walkImports(root, src)
	return a
}

// walkBlock records a cut point for every statement directly inside a
// module/block node, then recurses into each statement so nested blocks
// contribute (deeper) cut points too. ERROR subtrees contribute nothing:
// their structure is unreliable.
func (a *analysis) walkBlock(n *sitter.Node, src []byte, depth int) {
	prevEnd := 0 // last row (1-indexed) of the previous non-comment statement
	if n.Type() == "block" {
		// The first statement of a suite must stay with its header line.
		prevEnd = int(n.StartPoint().Row) + 1
	}
	commentStart, commentEnd := 0, 0
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.Type() == "ERROR" || c.IsMissing() {
			prevEnd = int(c.EndPoint().Row) + 1
			commentStart = 0
			continue
		}
		start, end := int(c.StartPoint().Row)+1, int(c.EndPoint().Row)+1
		if c.Type() == "comment" {
			if commentStart == 0 || commentEnd != start-1 {
				commentStart = start
			}
			commentEnd = end
			continue
		}
		cut := start
		if commentStart != 0 && commentEnd == start-1 {
			cut = commentStart
		}
		commentStart = 0
		if cut > prevEnd {
			if _, dup := a.cuts[cut]; !dup && cut > 1 {
				a.cuts[cut] = depth
			}
		}
		prevEnd = end
		a.walkStatement(c, src, depth)
	}
}

func (a *analysis) walkStatement(n *sitter.Node, src []byte, depth int) {
	if n.Type() == "block" {
		a.walkBlock(n, src, depth+1)
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		a.walkStatement(n.Child(i), src, depth)
	}
}

func (a *analysis) walkImports(n *sitter.Node, src []byte) {
	switch n.Type() {
	case "function_definition", "ERROR":
		return
	case "import_statement", "import_from_statement":
		a.imports = append(a.imports, parseImport(n, src))
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		a.walkImports(n.Child(i), src)
	}
}

func parseImport(n *sitter.Node, src []byte) importInfo {
	info := importInfo{text: strings.Join(strings.Fields(n.Content(src)), " ")}
	bind := func(c *sitter.Node) {
		switch c.Type() {
		case "dotted_name":
			first := strings.Split(c.Content(src), ".")[0]
			if n.Type() == "import_from_statement" {
				first = c.Content(src)
			}
			info.names = append(info.names, first)
		case "aliased_import":
			if alias := c.ChildByFieldName("alias"); alias != nil {
				info.names = append(info.names, alias.Content(src))
			}
		}
	}
	if n.Type() == "import_statement" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			bind(n.NamedChild(i))
		}
		return info
	}
	mod := n.ChildByFieldName("module_name")
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if mod != nil && c.StartByte() == mod.StartByte() && c.EndByte() == mod.EndByte() {
			continue
		}
		bind(c)
	}
	return info
}
