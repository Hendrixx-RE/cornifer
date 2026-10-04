package chunk

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestGenericTextBoundsAndCoverage(t *testing.T) {
	source := strings.Repeat("A normal documentation line.\n", 300) + strings.Repeat("界!", 5000) + "\n\nlast line\r\n"
	file := &model.File{ID: 12, Path: "docs/manual.md", Language: "text"}
	chunks, err := Text(context.Background(), file, []byte(source), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	rebuilt := make([]string, len(lines))
	for _, c := range chunks {
		if len(c.Text) > GenericMaxBytes || c.EndLine-c.StartLine+1 > GenericMaxLines || !utf8.ValidString(c.Text) || c.TokenCount > DefaultOptions().MaxTokens {
			t.Fatalf("unbounded/invalid chunk: lines=%d-%d bytes=%d tokens=%d", c.StartLine, c.EndLine, len(c.Text), c.TokenCount)
		}
		if c.SymbolID != nil || c.Embedding != nil || c.FileID != file.ID {
			t.Fatal("generic text fabricated structural/vector metadata")
		}
		parts := strings.Split(c.Text, "\n")
		if len(parts) != c.EndLine-c.StartLine+1 {
			t.Fatal("chunk line range does not match text")
		}
		for index, text := range parts {
			rebuilt[c.StartLine-1+index] += text
		}
	}
	if strings.Join(rebuilt, "\n")+"\n" != source {
		t.Fatal("generic chunks lost or changed source characters")
	}
}

func TestGenericTextEmptyInvalidAndCancelled(t *testing.T) {
	file := &model.File{ID: 1, Path: "README.md"}
	if out, err := Text(context.Background(), file, nil, Options{}); err != nil || len(out) != 0 {
		t.Fatalf("empty=%v, %v", out, err)
	}
	if _, err := Text(context.Background(), file, []byte{0xff}, Options{}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Text(ctx, file, []byte("source"), Options{}); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
}
