package chunk

import "unicode"

// CountTokens is the single tokenizer the chunker's budgets and
// model.Chunk.TokenCount use. It is a deterministic, dependency-free
// approximation of a BPE tokenizer (no vocab files, no network): every
// maximal run of letters/digits/underscore costs ceil(runeLen/4) tokens, every
// other non-space rune costs 1, and whitespace is free. It tracks real code
// tokenizations within roughly 20-30%, which is all a budget needs; it is
// not the provider's billing tokenizer. internal/embed should treat
// TokenCount as an estimate against its own input limits.
func CountTokens(s string) int {
	n, run := 0, 0
	flush := func() {
		if run > 0 {
			n += (run + 3) / 4
			run = 0
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			run++
		case unicode.IsSpace(r):
			flush()
		default:
			flush()
			n++
		}
	}
	flush()
	return n
}
