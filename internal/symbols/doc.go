// Package symbols walks a parse.Result's syntax tree into model.Symbol
// values: functions, classes, methods, and module/class-level variables,
// with line ranges, signatures, decorators, docstrings, and qualified names
// derived from lexical nesting. It depends on internal/parse for the tree
// and internal/model for the output shape; it does not resolve imports or
// call sites (internal/resolve) and does not decide chunk boundaries
// (internal/chunk), though internal/chunk consumes this package's output.
package symbols
