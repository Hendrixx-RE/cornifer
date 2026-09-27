package parse

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Result is the parsed syntax tree for one file plus any errors encountered
// while parsing. Errors is non-fatal diagnostic information (e.g.
// tree-sitter ERROR/MISSING nodes) — a non-empty Errors slice does not mean
// Tree is unusable, only that some region of the file did not parse
// cleanly.
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

// unimplemented is the Phase 0 stub Parser. Later waves replace it with a
// tree-sitter-backed implementation for Python.
type unimplemented struct{}

// New returns the Phase 0 stub Parser, which always returns
// model.ErrNotImplemented.
func New() Parser {
	return unimplemented{}
}

func (unimplemented) Parse(ctx context.Context, path string, source []byte) (*Result, error) {
	return nil, model.ErrNotImplemented
}
