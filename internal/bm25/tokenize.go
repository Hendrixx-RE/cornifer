package bm25

import (
	"strings"
	"unicode"
)

// stopwords is a deliberately small English stopword list. plan.md calls
// for dropping stopwords "sparingly": code text is dense with meaningful
// short tokens (e.g. "id", "if", "or" as identifiers/operators-in-text), so
// only the highest-frequency, lowest-signal function words are excluded.
// Nothing here is a common identifier fragment or a keyword in a mainstream
// language.
var stopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"of": {}, "to": {}, "in": {}, "on": {}, "and": {}, "or": {}, "it": {},
	"this": {}, "that": {}, "with": {}, "as": {}, "be": {}, "by": {}, "at": {},
}

// Tokenize splits code-and-prose text into lowercase index tokens.
//
// It first splits on non-alphanumeric boundaries (whitespace, punctuation,
// operators) to get raw "words" (which, for code, are typically whole
// identifiers, dotted paths broken at the dots, etc). Each raw word is
// emitted as-is (lowercased) AND, if it looks like a compound identifier
// (snake_case, camelCase, SCREAMING_SNAKE, or dotted path), decomposed into
// its parts, which are also emitted. This dual emission is deliberate per
// plan.md: keeping the unsplit identifier as a token means an exact query
// like "parseChunkHeader" still matches the single token that carries the
// most specific signal, while the split parts let fuzzier queries like
// "parse chunk header" or "chunk" alone find the same document.
func Tokenize(text string) []string {
	var tokens []string
	for _, word := range splitWords(text) {
		// Strip leading/trailing underscores before anything else, so a
		// dunder name like "__init__" or a stray "___" separator doesn't
		// leave underscore noise in the emitted tokens.
		trimmed := strings.Trim(word, "_")
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)

		parts := splitIdentifier(trimmed)
		if len(parts) <= 1 {
			if tok := filterToken(lower); tok != "" {
				tokens = append(tokens, tok)
			}
			continue
		}

		// Compound identifier: keep the whole thing as one token (never
		// stopword-filtered — an identifier is never an English stopword)
		// plus each meaningful part.
		tokens = append(tokens, lower)
		for _, p := range parts {
			if tok := filterToken(strings.ToLower(p)); tok != "" {
				tokens = append(tokens, tok)
			}
		}
	}
	return tokens
}

// filterToken drops stopwords and empty tokens but keeps short tokens: code
// is full of meaningful 1-2 character identifiers ("id", "ok", "db", "i",
// "x") that a generic-text stopword/min-length filter would wrongly
// discard.
func filterToken(tok string) string {
	if tok == "" {
		return ""
	}
	if _, stop := stopwords[tok]; stop {
		return ""
	}
	return tok
}

// splitWords breaks text on runs of characters that are not letters,
// digits, or underscore. Underscore is kept attached so snake_case and
// dunder names (__init__) survive as single words for splitIdentifier to
// handle; a dotted path like "pkg.mod.Func" splits into "pkg", "mod",
// "Func" here since '.' is a boundary.
func splitWords(text string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

// splitIdentifier decomposes a single word into its meaningful sub-parts if
// it looks like a compound code identifier. It handles:
//   - snake_case / SCREAMING_SNAKE_CASE -> split on '_'
//   - camelCase / PascalCase -> split at lower->upper and letter->digit
//     boundaries, and at acronym boundaries (HTTPServer -> HTTP, Server)
//   - leading/trailing underscores (including dunder names like __init__)
//     are stripped from the produced parts, and pure-underscore or
//     structural markers don't produce empty parts
//
// A word with a single part after splitting (e.g. "chunk", "12345") is
// reported as len<=1 so the caller treats it as a plain token, not a
// compound identifier that also needs to be re-emitted whole.
func splitIdentifier(word string) []string {
	trimmed := strings.Trim(word, "_")
	if trimmed == "" {
		return nil
	}

	var segments []string
	if strings.Contains(trimmed, "_") {
		for _, seg := range strings.Split(trimmed, "_") {
			if seg != "" {
				segments = append(segments, seg)
			}
		}
	} else {
		segments = []string{trimmed}
	}

	var parts []string
	for _, seg := range segments {
		parts = append(parts, camelSplit(seg)...)
	}
	return parts
}

// camelSplit splits a single underscore-free segment on camelCase /
// PascalCase / acronym / letter-digit boundaries. Examples:
//
//	"camelCase"   -> ["camel", "Case"]
//	"HTTPServer"  -> ["HTTP", "Server"]
//	"parseJSON2"  -> ["parse", "JSON2"]
//	"v2"          -> ["v2"]
func camelSplit(seg string) []string {
	runes := []rune(seg)
	if len(runes) == 0 {
		return nil
	}

	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false

		switch {
		case unicode.IsLower(prev) && unicode.IsUpper(cur):
			// camelCase: fooBar -> foo | Bar
			boundary = true
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			// acronym followed by a new word: HTTPServer -> HTTP | Server
			boundary = true
		}

		if boundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))

	if len(parts) <= 1 {
		return []string{seg}
	}
	return parts
}
