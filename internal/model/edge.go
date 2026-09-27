package model

// EdgeKind classifies an Edge (and, reused, the kind of reference an
// UnresolvedRef represents). The set is closed and mirrors the `edges.kind`
// / `unresolved_refs.kind` CHECK constraints in migrations — adding a value
// requires a migration change alongside this one.
type EdgeKind string

const (
	// EdgeKindImports connects a File's module Symbol to the module Symbol
	// it imports (module-level `import`/`from ... import ...`).
	EdgeKindImports EdgeKind = "imports"
	// EdgeKindCalls connects a calling Symbol to the Symbol it invokes at a
	// call site.
	EdgeKindCalls EdgeKind = "calls"
	// EdgeKindInherits connects a class Symbol to a base class Symbol
	// listed in its class definition.
	EdgeKindInherits EdgeKind = "inherits"
	// EdgeKindImplements connects an overriding method Symbol to the method
	// Symbol it overrides on a base class (or interface-like base). This is
	// what lets "find callers of Base.method" also surface callers that
	// only ever call the subclass override.
	EdgeKindImplements EdgeKind = "implements"
)

// Valid reports whether k is one of the defined EdgeKind values.
func (k EdgeKind) Valid() bool {
	switch k {
	case EdgeKindImports, EdgeKindCalls, EdgeKindInherits, EdgeKindImplements:
		return true
	default:
		return false
	}
}

// Confidence scores how certain an Edge's resolution is, in the closed
// range [0, 1]. Resolution in a dynamic language like Python is heuristic,
// never full type inference (see plan.md "Risks and mitigations"), so this
// is a graded score rather than a boolean: a name+import-binding match on
// an unambiguous target should record ConfidenceExact, while a resolution
// that had to guess between multiple same-named candidates should record
// something lower. Consumers that only care about exact-vs-heuristic can
// compare against ConfidenceExact directly.
type Confidence float64

const (
	// ConfidenceExact marks a resolution with no ambiguity: e.g. a call
	// resolved through an unambiguous import binding or an unambiguous
	// `self.method()` lookup on a single known class.
	ConfidenceExact Confidence = 1.0
	// ConfidenceHigh marks a resolution that is very likely correct but
	// involved a heuristic step, e.g. resolving through a shallow class
	// hierarchy walk.
	ConfidenceHigh Confidence = 0.75
	// ConfidenceMedium marks a resolution chosen among a small number of
	// plausible candidates (e.g. same method name on unrelated classes)
	// without enough information to prefer one confidently.
	ConfidenceMedium Confidence = 0.5
	// ConfidenceLow marks a resolution that is little better than a guess,
	// kept because some answer is more useful than none but callers should
	// treat it skeptically.
	ConfidenceLow Confidence = 0.25
)

// Edge is a directed, resolved relationship between two Symbols.
type Edge struct {
	ID          int64
	SrcSymbolID int64
	DstSymbolID int64
	Kind        EdgeKind
	Confidence  Confidence
}

// UnresolvedRef records a reference (import or call, per Kind) that
// resolution could not bind to a destination Symbol within the indexed
// Repo — either because it targets code outside the repo (e.g. a
// third-party import) or because no resolution heuristic matched. These
// rows are kept, not dropped, so recall gaps in structural queries are
// explainable rather than silent (see plan.md "Risks and mitigations").
type UnresolvedRef struct {
	ID          int64
	SrcSymbolID int64

	// Name is the referenced name exactly as written at the call/import
	// site, e.g. "requests.get" or ".utils.helper". It is not resolved to
	// a qualified name because that resolution is precisely what failed.
	Name string

	// Kind is the kind of reference that failed to resolve. In practice
	// this is EdgeKindImports, EdgeKindCalls, EdgeKindInherits, or
	// EdgeKindImplements — whichever resolution step produced this miss.
	Kind EdgeKind
}
