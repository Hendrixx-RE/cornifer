package bm25

import (
	"reflect"
	"testing"
)

func TestTokenizeSnakeCase(t *testing.T) {
	got := Tokenize("parse_chunk_header")
	want := []string{"parse_chunk_header", "parse", "chunk", "header"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeCamelCase(t *testing.T) {
	got := Tokenize("parseChunkHeader")
	want := []string{"parsechunkheader", "parse", "chunk", "header"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizePascalCase(t *testing.T) {
	got := Tokenize("ParseChunkHeader")
	want := []string{"parsechunkheader", "parse", "chunk", "header"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeScreamingSnakeCase(t *testing.T) {
	got := Tokenize("MAX_RETRY_COUNT")
	want := []string{"max_retry_count", "max", "retry", "count"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeAcronymBoundary(t *testing.T) {
	got := Tokenize("HTTPServer")
	want := []string{"httpserver", "http", "server"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeDottedPath(t *testing.T) {
	got := Tokenize("pkg.mod.ParseFile")
	want := []string{"pkg", "mod", "parsefile", "parse", "file"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeDunder(t *testing.T) {
	got := Tokenize("__init__")
	want := []string{"init"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeLeadingUnderscore(t *testing.T) {
	got := Tokenize("_privateHelper")
	want := []string{"privatehelper", "private", "helper"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeDigitsInIdentifier(t *testing.T) {
	got := Tokenize("parseJSON2")
	want := []string{"parsejson2", "parse", "json2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeVersionLikeToken(t *testing.T) {
	got := Tokenize("v2")
	want := []string{"v2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizePlainWordUnaffected(t *testing.T) {
	got := Tokenize("chunk")
	want := []string{"chunk"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeShortIdentifiersKept(t *testing.T) {
	for _, tok := range []string{"id", "db", "ok", "i", "x"} {
		got := Tokenize(tok)
		want := []string{tok}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %v, want %v", tok, got, want)
		}
	}
}

func TestTokenizeStopwordsDropped(t *testing.T) {
	got := Tokenize("the quick fox")
	want := []string{"quick", "fox"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeStopwordNeverDropsIdentifierPart(t *testing.T) {
	// "as" is a stopword, but as a compound-identifier part it still
	// matters less than the whole; verify the whole identifier survives
	// even when one split part would be filtered.
	got := Tokenize("parseAs")
	want := []string{"parseas", "parse"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() = %v, want %v", got, want)
	}
}

func TestTokenizeEmptyAndWhitespace(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n", "___", "..."} {
		if got := Tokenize(in); len(got) != 0 {
			t.Errorf("Tokenize(%q) = %v, want empty", in, got)
		}
	}
}

func TestTokenizeMixedProseAndCode(t *testing.T) {
	got := Tokenize("The rate_limiter.CheckLimit() function throttles requests.")
	for _, want := range []string{"rate_limiter", "rate", "limiter", "checklimit", "check", "limit", "throttles", "requests"} {
		found := false
		for _, tok := range got {
			if tok == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Tokenize() missing expected token %q in %v", want, got)
		}
	}
	for _, unwanted := range []string{"the"} {
		for _, tok := range got {
			if tok == unwanted {
				t.Errorf("Tokenize() should have dropped stopword %q, got %v", unwanted, got)
			}
		}
	}
}
