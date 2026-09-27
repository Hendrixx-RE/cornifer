package chunk

import (
	"context"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Chunker turns one file's parsed symbols into retrieval-ready Chunks. file
// and symbols must belong to the same File (symbols is every model.Symbol
// internal/symbols extracted from it, in any order); source is the file's
// raw bytes, needed to slice out each chunk's text by line range.
type Chunker interface {
	Chunk(ctx context.Context, file *model.File, source []byte, symbols []*model.Symbol) ([]*model.Chunk, error)
}

// unimplemented is the Phase 0 stub Chunker. Later waves replace it with
// the AST-aware chunker described in plan.md ("AST-aware chunking").
type unimplemented struct{}

// New returns the Phase 0 stub Chunker, which always returns
// model.ErrNotImplemented.
func New() Chunker {
	return unimplemented{}
}

func (unimplemented) Chunk(ctx context.Context, file *model.File, source []byte, symbols []*model.Symbol) ([]*model.Chunk, error) {
	return nil, model.ErrNotImplemented
}
