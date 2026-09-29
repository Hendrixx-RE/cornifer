package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hendrixx-RE/cornifer/internal/store"
)

// e2eStoreOrSkip connects to Postgres the same way tests in internal/store
// do, skipping (not failing) when no database is reachable, so this test
// stays green in CI/no-Docker environments while still exercising the real
// pipeline end to end when Postgres is up (`make up && make migrate`).
func e2eStoreOrSkip(t *testing.T) {
	t.Helper()

	dsn := os.Getenv(store.DatabaseURLEnvVar)
	if dsn == "" {
		dsn = store.DefaultDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := store.NewPostgres(ctx, store.Config{DSN: dsn})
	if err != nil {
		t.Skipf("skipping: no reachable postgres at %s (start one with `make up && make migrate`): %v", dsn, err)
	}
	st.Close()
}

// writeFixtureRepo writes a tiny, self-contained Python "repo" exercising
// imports, a call, and a class/method — enough to produce at least one
// symbol of each kind, one resolved call edge, and one BM25-searchable
// chunk — and returns its root directory.
func writeFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "foo.py"), `"""Foo module: a greeter."""


class Greeter:
    """Greets people by name."""

    def greet(self, name):
        return f"hello {name}"


def make_greeter():
    return Greeter()
`)

	mustWrite(t, filepath.Join(root, "bar.py"), `"""Bar module: uses foo."""

from foo import make_greeter


def run():
    g = make_greeter()
    return g.greet("world")
`)

	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runCLI executes cornifer's root command with args and returns its
// combined stdout/stderr and any error, isolated from the process's real
// stdout/stderr.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	root.SilenceUsage = true
	root.SilenceErrors = true
	err := root.Execute()
	return out.String(), err
}

// TestEndToEndFixtureRepo runs the whole index -> query/structural-command
// pipeline against a small fixture repo. It skips (does not fail) when no
// Postgres is reachable.
func TestEndToEndFixtureRepo(t *testing.T) {
	e2eStoreOrSkip(t)

	repoRoot := writeFixtureRepo(t)
	cacheDir := t.TempDir()

	out, err := runCLI(t, "index", "--repo", repoRoot, "--cache-dir", cacheDir, "--embed-provider", "fake")
	if err != nil {
		t.Fatalf("index: %v\noutput:\n%s", err, out)
	}
	t.Logf("index output:\n%s", out)
	for _, want := range []string{"symbols=", "edges=", "chunks="} {
		if !strings.Contains(out, want) {
			t.Errorf("index output missing %q:\n%s", want, out)
		}
	}

	// find-definition: exact qualified name.
	out, err = runCLI(t, "find-definition", "foo.Greeter.greet", "--repo", repoRoot, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("find-definition: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "foo.py") {
		t.Errorf("find-definition output missing file location:\n%s", out)
	}

	// callers: bar.run calls foo.Greeter.greet.
	out, err = runCLI(t, "callers", "foo.Greeter.greet", "--repo", repoRoot, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("callers: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "bar.run") {
		t.Errorf("callers output missing bar.run:\n%s", out)
	}

	// callees: bar.run calls make_greeter and greet.
	out, err = runCLI(t, "callees", "bar.run", "--repo", repoRoot, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("callees: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "make_greeter") {
		t.Errorf("callees output missing make_greeter:\n%s", out)
	}

	// blast-radius of foo.py should include bar.py (bar imports foo).
	out, err = runCLI(t, "blast-radius", "foo.py", "--repo", repoRoot, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("blast-radius: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "bar.py") {
		t.Errorf("blast-radius output missing bar.py:\n%s", out)
	}

	// cycles: none expected in this fixture.
	out, err = runCLI(t, "cycles", "--repo", repoRoot, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("cycles: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "no cycles found") {
		t.Errorf("cycles output = %q, want no cycles", out)
	}

	// query: lexical match on "greet".
	out, err = runCLI(t, "query", "greeter", "--repo", repoRoot, "--cache-dir", cacheDir, "--embed-provider", "fake")
	if err != nil {
		t.Fatalf("query: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(out, "no results") {
		t.Errorf("query output = %q, want at least one result", out)
	}
}
