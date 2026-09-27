package parse

import (
	"context"
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// Result is the parsed syntax tree for one file plus any errors encountered
// while parsing. Errors is non-fatal diagnostic information (e.g.
// tree-sitter ERROR/MISSING nodes) — a non-empty Errors slice does not mean
// Tree is unusable, only that some region of the file did not parse
// cleanly.
//
// Tree wraps a C-allocated tree-sitter tree. Callers must call Tree.Close()
// once they are done with a Result (a finalizer also releases it on GC, but
// relying on that for a batch indexing run lets C memory pile up well
// beyond what the Go heap reports, so close explicitly).
type Result struct {
	Tree   *sitter.Tree
	Source []byte
	Errors []error
}

// Parser turns a file's raw source bytes into a syntax Result. Implementations
// are expected to be safe for concurrent use across different Parse calls.
type Parser interface {
	// Parse parses source, which is the exact byte content of the file at
	// path. path is used only for error messages, not read from disk.
	Parse(ctx context.Context, path string, source []byte) (*Result, error)
}

// pythonParser implements Parser against the tree-sitter Python grammar.
//
// Concurrency contract: a *sitter.Parser is not safe for concurrent use (it
// carries C-side mutable parse state), but a compiled *sitter.Language is
// immutable and shared freely. pythonParser therefore pools one
// *sitter.Parser per concurrent caller via sync.Pool rather than sharing a
// single one: Parse acquires a parser from the pool, uses it for exactly one
// call, and returns it before returning, so callers may call Parse
// concurrently from any number of goroutines. The *sitter.Tree returned in
// Result is a separate, independent value per call — it does not need the
// parser that produced it and is safe for the caller to read from any
// goroutine (tree-sitter trees are immutable once parsing completes), but a
// single Result should not be mutated (e.g. via Tree.Edit) from multiple
// goroutines at once.
type pythonParser struct {
	pool sync.Pool
}

// New returns a Parser backed by the tree-sitter Python grammar.
func New() Parser {
	p := &pythonParser{}
	p.pool.New = func() any {
		sp := sitter.NewParser()
		sp.SetLanguage(python.GetLanguage())
		return sp
	}
	return p
}

func (p *pythonParser) Parse(ctx context.Context, path string, source []byte) (*Result, error) {
	sp := p.pool.Get().(*sitter.Parser)
	defer p.pool.Put(sp)

	tree, err := sp.ParseCtx(ctx, nil, source)
	if err != nil {
		return nil, fmt.Errorf("parse: %s: %w", path, err)
	}

	res := &Result{Tree: tree, Source: source}
	collectSyntaxErrors(tree.RootNode(), path, source, &res.Errors)
	return res, nil
}

// collectSyntaxErrors walks n's subtree looking for tree-sitter ERROR and
// MISSING nodes, recording one diagnostic error per node found. It never
// returns early on finding one: a single unparseable region should not hide
// others, and the caller (internal/walker's indexing loop, eventually) must
// not treat a non-empty result as fatal.
func collectSyntaxErrors(n *sitter.Node, path string, source []byte, errs *[]error) {
	if n == nil {
		return
	}
	if n.IsMissing() {
		*errs = append(*errs, fmt.Errorf("%s:%d:%d: missing %s", path, n.StartPoint().Row+1, n.StartPoint().Column+1, n.Type()))
	} else if n.IsError() {
		*errs = append(*errs, fmt.Errorf("%s:%d:%d: syntax error near %q", path, n.StartPoint().Row+1, n.StartPoint().Column+1, errSnippet(source, n)))
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		collectSyntaxErrors(n.Child(i), path, source, errs)
	}
}

// errSnippet returns a short, single-line preview of n's source text for use
// in a diagnostic message.
func errSnippet(source []byte, n *sitter.Node) string {
	start, end := int(n.StartByte()), int(n.EndByte())
	if end > len(source) {
		end = len(source)
	}
	if start < 0 || start > end {
		return ""
	}
	s := string(source[start:end])
	const maxLen = 40
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}
