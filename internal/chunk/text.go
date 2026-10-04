package chunk

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

const (
	GenericMaxBytes = 4096
	GenericMaxLines = 128
)

// Text produces lexical-only chunks without parsing a structural language.
// Text is an exact source span, with inclusive line numbers and no SymbolID.
// Ordinary chunks contain complete lines. Oversized single lines are split on
// UTF-8 boundaries; those fragments share their original line number. No source
// lines or within-line whitespace are discarded. A trailing newline is a line
// delimiter, not a separate source line, matching the AST chunker's convention.
func Text(ctx context.Context, file *model.File, source []byte, options Options) ([]*model.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("generic source is not UTF-8")
	}
	if len(source) == 0 {
		return nil, nil
	}
	maxTokens := options.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultOptions().MaxTokens
	}
	// Keep the header bounded even for unusually long paths. The authoritative
	// full path remains in the file record and citation metadata.
	path := []rune(file.Path)
	if len(path) > 128 {
		path = path[:128]
	}
	header := "file: " + string(path) + "\nscope: generic text; structural graph unsupported"
	budget := maxTokens - CountTokens(header)
	if budget < 1 {
		budget = 1
	}
	lines := strings.Split(string(source), "\n")
	if source[len(source)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	var out []*model.Chunk
	var pending []string
	start := 1
	emit := func(text string, first, last int) {
		c := &model.Chunk{FileID: file.ID, StartLine: first, EndLine: last, Text: text, ContextHeader: header}
		c.TokenCount = CountTokens(EmbeddingText(c))
		out = append(out, c)
	}
	flush := func() {
		if len(pending) > 0 {
			emit(strings.Join(pending, "\n"), start, start+len(pending)-1)
			pending = nil
		}
	}
	fits := func(text string) bool { return len(text) <= GenericMaxBytes && CountTokens(text) <= budget }
	for index, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNumber := index + 1
		if !fits(line) {
			flush()
			// Binary search a bounded prefix; token count is monotonic for a
			// prefix. Start with a byte-limited UTF-8 slice to avoid converting
			// an entire huge line into runes for every fragment.
			for rest := line; rest != ""; {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				end := min(len(rest), GenericMaxBytes)
				for end < len(rest) && !utf8.RuneStart(rest[end]) {
					end--
				}
				runes := []rune(rest[:end])
				low, high := 1, len(runes)
				for low < high {
					mid := (low + high + 1) / 2
					if fits(string(runes[:mid])) {
						low = mid
					} else {
						high = mid - 1
					}
				}
				part := string(runes[:low])
				emit(part, lineNumber, lineNumber)
				rest = rest[len(part):]
			}
			continue
		}
		candidate := strings.Join(append(pending, line), "\n")
		if len(pending) == GenericMaxLines || !fits(candidate) {
			flush()
		}
		if len(pending) == 0 {
			start = lineNumber
		}
		pending = append(pending, line)
	}
	flush()
	return out, nil
}
