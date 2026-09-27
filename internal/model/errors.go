package model

import "errors"

// ErrNotImplemented is returned by every Phase 0 interface stub (Embedder,
// Store, SparseIndex, Parser, Chunker, ...). It is defined once here, in the
// shared contract package, so callers can check for it uniformly with
// errors.Is regardless of which package's stub they called.
var ErrNotImplemented = errors.New("cornifer: not implemented")
