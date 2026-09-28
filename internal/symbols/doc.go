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
// symbol for every class and function/method found while walking the module
// body and, recursively, every class body and function body nested inside
// it (including through if/for/while/with/try/match wrappers, so
// conditionally-defined symbols — e.g. a class guarded by `if
// TYPE_CHECKING:` or an inner def in an if/else branch — are still found).
// Bare-name assignments (SymbolKindVariable) are extracted only at module
// and class level; local variables inside function bodies are never symbols.
// Python does not allow a def or class statement inside a comprehension or
// lambda, so those need no handling.
//
// # Nested definitions and QualifiedName
//
// A def or class nested inside a function body gets a QualifiedName formed
// exactly like CPython's __qualname__ (PEP 3155): the enclosing function's
// QualifiedName, then the literal segment "<locals>", then the nested name.
// A class body does NOT add a "<locals>" segment. Examples, for module "m":
//
//	def outer():            m.outer
//	    def inner():        m.outer.<locals>.inner
//	        def deep():     m.outer.<locals>.inner.<locals>.deep
//	    class C:            m.outer.<locals>.C
//	        def meth(self): m.outer.<locals>.C.meth   (SymbolKindMethod)
//	class K:                m.K
//	    def method(self):   m.K.method
//	        def helper():   m.K.method.<locals>.helper (SymbolKindFunction)
//
// "<locals>" contains characters that cannot occur in a Python identifier, so
// a nested name can never collide with a real attribute path, and consumers
// can detect a function-local symbol by looking for the ".<locals>." segment.
// A def directly in a class body is a SymbolKindMethod; a def inside any
// function body (method or not) is always a SymbolKindFunction. ParentID is
// the enclosing function or class symbol. The qualified name is purely
// lexical: a def inside a loop, or defined in both branches of an if/else,
// produces one symbol per occurrence in source, all sharing one QualifiedName.
//
// Multiple symbols may share a QualifiedName (e.g. @typing.overload stubs
// followed by their implementation, or conditional redefinitions) — Extract
// does not deduplicate; find_definition-style lookups need to account for
// that (see Store.FindSymbolsByName, which returns a slice for this reason).
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
