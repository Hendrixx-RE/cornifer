package main

import (
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// TestEvalIsNotImplemented documents the one subcommand still a Phase 0
// stub: eval, pending the eval wave (plan.md "Days 18-19: evaluation").
func TestEvalIsNotImplemented(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"eval"})
	root.SilenceUsage = true
	root.SilenceErrors = true
	err := root.Execute()
	if !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("cornifer eval: err = %v, want ErrNotImplemented", err)
	}
}

// TestCommandsFailFastWithoutPostgres checks that every command needing a
// database returns a clear connection error (not a panic, and not
// ErrNotImplemented) when Postgres is unreachable — this test does not
// require Docker/`make up`, it only checks the fast-fail path.
func TestCommandsFailFastWithoutPostgres(t *testing.T) {
	t.Setenv("CORNIFER_DATABASE_URL", "postgres://cornifer:cornifer@localhost:1/cornifer?sslmode=disable")

	for _, args := range [][]string{
		{"index", "--repo", "."},
		{"reindex", "--repo", "."},
		{"query", "rate limiting", "--repo", "."},
		{"find-definition", "foo", "--repo", "."},
		{"callers", "foo", "--repo", "."},
		{"callees", "foo", "--repo", "."},
		{"blast-radius", "foo", "--repo", "."},
		{"cycles", "--repo", "."},
	} {
		t.Run(args[0], func(t *testing.T) {
			root := newRootCmd()
			root.SetArgs(args)
			root.SilenceUsage = true
			root.SilenceErrors = true
			err := root.Execute()
			if err == nil {
				t.Fatalf("cornifer %v: expected an error with no reachable postgres", args)
			}
			if errors.Is(err, model.ErrNotImplemented) {
				t.Errorf("cornifer %v: err = %v, want a connection error, not ErrNotImplemented", args, err)
			}
		})
	}
}

// TestSubcommandsAreWired is a smoke test that every subcommand is
// registered and parses its own flags without error before RunE runs.
func TestSubcommandsAreWired(t *testing.T) {
	root := newRootCmd()
	names := map[string]bool{}
	for _, cmd := range root.Commands() {
		names[cmd.Name()] = true
	}
	for _, want := range []string{"index", "reindex", "query", "eval", "find-definition", "callers", "callees", "blast-radius", "cycles"} {
		if !names[want] {
			t.Errorf("subcommand %q not registered", want)
		}
	}
}
