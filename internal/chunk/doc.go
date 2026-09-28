// Package chunk splits a parsed, symbol-extracted file into AST-aware
// retrieval units (model.Chunk) and runs before internal/embed.
//
// # Shape of the output
//
//   - One chunk per function/method/class. A class whose text exceeds
//     Options.ClassWholeTokens becomes a header chunk (signature, docstring,
//     attributes: every class-body line outside a method/nested class) plus
//     the chunks of its members, recursively. SymbolID of a header chunk is
//     the class.
//   - Module-level code outside any def/class is its own chunk(s) with a nil
//     SymbolID. Functions and classes are leaves for carving purposes: a def
//     or class nested in a function body (whether or not internal/symbols
//     extracts it) stays inside its enclosing chunk.
//   - A region above Options.MaxTokens is split only at lines the
//     tree-sitter AST reports as the start of a statement (never mid-
//     statement, never inside another symbol); a single statement larger than
//     the budget stays whole.
//   - Adjacent, contiguous sibling symbols are merged while their combined
//     text stays within Options.MergeTokens; the merged chunk's SymbolID is
//     the first symbol.
//   - Blank-only gaps are absorbed into a neighbour, so every source line is
//     covered by exactly one chunk.
//
// # Invariants (property-tested)
//
// No chunk partially overlaps any symbol (each symbol is contained in, or
// disjoint from, each chunk), and every source line is covered.
//
// # Header and tokens
//
// Chunk.Text is raw source. Chunk.ContextHeader holds file path, symbol,
// enclosing classes, imports actually used by the chunk, and signature; the
// embedding input is EmbeddingText (header + "\n" + text). TokenCount is
// CountTokens of that string. CountTokens is a dependency-free BPE
// approximation (see tokens.go) used consistently for all budgets.
package chunk
