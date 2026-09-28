// Package resolve turns raw imports and call sites found by internal/symbols
// into model.Edge rows: module-level import resolution (mapping file paths
// to dotted module names), name+scope+import-binding call resolution, and
// class-hierarchy-aware method resolution (so a call to a base method also
// links to overriding subclasses). Resolution here is heuristic, not type
// inference — every edge it produces carries a model.Confidence, and every
// reference it cannot bind becomes a model.UnresolvedRef rather than being
// dropped. It depends on internal/model, internal/parse and the symbols the
// caller supplies (see FileInput); internal/graph is the consumer that turns
// its edges into a traversable graph.
//
// # API
//
// Resolve takes every file's parse.Result and symbols (with repo-unique
// IDs) and returns an Output: deduplicated Edges, UnresolvedRefs,
// per-import ImportRecords, and Stats. It is pure and needs no database.
//
// # What is resolved
//
//   - Imports: `import x`, `import x.y as z`, `from x import y as z`,
//     relative imports with any number of leading dots, star imports, and
//     __init__.py re-exports (followed transitively). Imports become
//     EdgeKindImports edges between module symbols. Imports leaving the
//     repo are labelled ExternalStdlib or ExternalThirdParty in
//     Output.Imports and recorded as unresolved refs flagged external
//     (Output.IsExternal); they are never pretended to resolve.
//   - Calls (EdgeKindCalls) via, in order: function locals, lexical scope,
//     import bindings, star imports, builtins; `self.`/`cls.`/`super()`
//     lookups on the enclosing class; attribute chains through imported
//     modules and classes; locals bound exactly once to a constructor call
//     (`x = Foo()`), typed as Foo; and, as a last resort for receivers of
//     unknown type, name-only matching against repo methods.
//   - Inheritance (EdgeKindInherits) from each class's base list, and
//     overrides (EdgeKindImplements) from a method to the base method it
//     overrides, found by a C3-style MRO walk per direct base so diamonds
//     and deep chains link correctly. graph.Callers follows Implements edges
//     in reverse, which is what makes "callers through interfaces" work.
//
// # Confidence
//
// ConfidenceExact: unambiguous binding, or a member found on the class
// itself. ConfidenceHigh: found via a hierarchy walk, a star import, or a
// last-definition-wins pick among redefinitions. ConfidenceMedium: a unique
// repo method matched by name alone (or subclass overrides of a
// self-called hook). ConfidenceLow: several name-only candidates. When the
// same (src, dst, kind) is derived twice, the higher confidence is kept.
//
// # Known limits
//
// This is not type inference. Flow is ignored (an import anywhere in a file
// binds file-wide; conditional redefinitions resolve to the last one).
// Values returned by calls, attribute types, decorators that rewrap
// callables, dynamic dispatch, and getattr are not modelled. Defs nested in
// function bodies are only seen if internal/symbols extracts them; until
// then their calls are attributed to the enclosing symbol and their names
// shadow module-level ones as locals. References that cannot be bound are
// counted in Output.Stats — in-repo recall is Stats.ResolvedRatio, which
// excludes external references — so the gap is measurable.
package resolve
