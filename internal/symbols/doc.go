// Package symbols walks a parse.Result's syntax tree into model.Symbol
// values: functions, classes, methods, and module/class-level variables,
// with line ranges, signatures, decorators, docstrings, and qualified names
// derived from lexical nesting. It depends on internal/parse for the tree
// and internal/model for the output shape; it does not resolve imports or
// call sites (internal/resolve) and does not decide chunk boundaries
// (internal/chunk), though internal/chunk consumes this package's output.
//
// # Scope
//
// Extract produces exactly one SymbolKindModule symbol per file, plus one
// symbol for every class, function/method, and bare-name assignment found
// while walking the module body and, recursively, every class body nested
// inside it (including through if/for/while/with/try wrappers, so
// conditionally-defined top-level symbols — e.g. a class guarded by `if
// TYPE_CHECKING:` — are still found). Function and method bodies are never
// walked: a def or class written inside another function's body is not
// extracted, matching the plan's "module/class-level" scope. Multiple
// symbols may share a QualifiedName (e.g. @typing.overload stubs followed by
// their implementation, or any other redefinition of the same name) —
// Extract does not deduplicate; find_definition-style lookups need to
// account for that (see Store.FindSymbolsByName, which already returns a
// slice for this reason).
//
// # Parent links before persistence
//
// model.Symbol.ID and model.Symbol.ParentID are meant to hold
// database-assigned IDs, but Extract runs before any database interaction.
// Within a single Extract call, it instead assigns each Symbol a
// process-local, non-zero, monotonically increasing temporary ID (module
// symbol first, so it is always ID 1 within that call), and wires
// ParentID to the parent symbol's temporary ID the same way. These IDs are
// only unique and meaningful within the returned slice — they say nothing
// about any other file or Extract call, and must never be persisted as-is.
//
// The store package is responsible for remapping them: when inserting a
// file's symbols, walk the slice in order (Extract always emits a symbol
// after its parent, since a class/function's own record is appended before
// its body is walked), assign each one the real BIGSERIAL id Postgres
// returns, and rewrite every later ParentID that pointed at its temporary ID
// to that real id before the row referencing it is inserted — e.g. via a
// map[int64]int64 from temporary ID to real ID built up as rows are
// inserted.
package symbols
