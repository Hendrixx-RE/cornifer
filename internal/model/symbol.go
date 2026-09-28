package model

// SymbolKind classifies a Symbol. The set is closed and mirrors the
// `symbols.kind` CHECK constraint in migrations/00003_create_symbols.sql —
// adding a value requires a migration change alongside this one.
type SymbolKind string

const (
	// SymbolKindModule is the single implicit symbol representing a whole
	// file (one per File). Its QualifiedName equals the file's ModuleName.
	SymbolKindModule SymbolKind = "module"
	// SymbolKindClass is a class definition.
	SymbolKindClass SymbolKind = "class"
	// SymbolKindFunction is a module-level (or nested, non-method) function.
	SymbolKindFunction SymbolKind = "function"
	// SymbolKindMethod is a function defined directly inside a class body,
	// including staticmethod/classmethod/async variants.
	SymbolKindMethod SymbolKind = "method"
	// SymbolKindVariable is a module- or class-level assignment target
	// (e.g. constants, class attributes). Local variables inside function
	// bodies are not symbols.
	SymbolKindVariable SymbolKind = "variable"
)

// Valid reports whether k is one of the defined SymbolKind values.
func (k SymbolKind) Valid() bool {
	switch k {
	case SymbolKindModule, SymbolKindClass, SymbolKindFunction, SymbolKindMethod, SymbolKindVariable:
		return true
	default:
		return false
	}
}

// Symbol is a named, indexable code entity extracted from a File: a module,
// class, function, method, or module/class-level variable.
type Symbol struct {
	ID     int64
	FileID int64
	Kind   SymbolKind

	// Name is the unqualified identifier as written in source, e.g. "get".
	// For SymbolKindModule this equals the File's ModuleName (there is no
	// shorter unqualified form for a module).
	Name string

	// QualifiedName is the dotted path from the module root to this symbol,
	// e.g. "fastapi.routing.APIRoute.get". A def or class nested in a
	// function body follows CPython's __qualname__ form, with a "<locals>"
	// segment after the enclosing function, e.g. "m.outer.<locals>.inner"
	// (see internal/symbols doc.go).
	//
	// QualifiedName is NOT unique, within a Repo or even within a File:
	// @typing.overload stubs and their implementation, conditional
	// redefinitions (if/else, try/except ImportError), and same-named
	// nested defs all produce several Symbols with one QualifiedName. The
	// database agrees: symbols(qualified_name) has a plain btree index, not a
	// UNIQUE constraint. It is the primary lookup key for find_definition-
	// style queries, but consumers MUST handle multiple symbols sharing a
	// qualified name (disambiguate, or return all candidates) and must not
	// treat it as an identity key; use ID for that.
	QualifiedName string

	// ParentID is the enclosing Symbol: the class Symbol for a method, the
	// module Symbol for a top-level class/function/variable, the class
	// Symbol for a nested class, or the enclosing function/method Symbol for
	// a def or class nested inside a function body. Nil for module symbols,
	// which have no enclosing scope.
	ParentID *int64

	// StartLine and EndLine are 1-indexed and inclusive, spanning from the
	// first line of the symbol's own definition (including any decorators)
	// through its last body line. tree-sitter reports 0-indexed rows; the
	// parse package is responsible for the +1 conversion before a Symbol is
	// constructed. A single-line symbol has StartLine == EndLine.
	StartLine int
	EndLine   int

	// Signature is the source text of the def/class header: for a function
	// or method, from the first decorator line through the trailing ":" of
	// the parameter list (spanning multiple lines for multi-line
	// signatures); for a class, the "class Name(Bases):" line(s). Empty for
	// modules and variables.
	Signature string

	// Docstring is the extracted, unindented docstring body (no surrounding
	// quotes), or empty if the symbol has none.
	Docstring string
}
