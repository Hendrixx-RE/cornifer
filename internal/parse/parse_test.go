package parse

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestParseValidSource(t *testing.T) {
	src := []byte("def f(x):\n    return x + 1\n")
	res, err := New().Parse(context.Background(), "f.py", src)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	defer res.Tree.Close()

	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want none for valid source", res.Errors)
	}
	if res.Tree.RootNode().Type() != "module" {
		t.Errorf("root node type = %q, want module", res.Tree.RootNode().Type())
	}
	if string(res.Source) != string(src) {
		t.Errorf("Source mismatch")
	}
}

func TestParseCollectsErrorsWithoutFailing(t *testing.T) {
	// Deliberately broken syntax: unbalanced parens and a dangling colon.
	src := []byte("def f(x:\n    return x +\n\nclass :\n    pass\n")
	res, err := New().Parse(context.Background(), "broken.py", src)
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil even for broken source", err)
	}
	defer res.Tree.Close()

	if len(res.Errors) == 0 {
		t.Fatal("Errors = empty, want at least one diagnostic for broken source")
	}
	if res.Tree.RootNode() == nil {
		t.Fatal("RootNode() = nil, want a usable (partial) tree")
	}
	for _, e := range res.Errors {
		if !strings.Contains(e.Error(), "broken.py") {
			t.Errorf("error %q does not mention the file path", e)
		}
	}
}

func TestParseConcurrentUse(t *testing.T) {
	p := New()
	src := []byte("def f():\n    pass\n")

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := p.Parse(context.Background(), "f.py", src)
			if err != nil {
				errs <- err
				return
			}
			defer res.Tree.Close()
			if res.Tree.RootNode().Type() != "module" {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Parse() error = %v", err)
		}
	}
}
