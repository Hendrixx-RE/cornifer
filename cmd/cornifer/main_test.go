package main

import (
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestSubcommandsAreWiredButNotImplemented(t *testing.T) {
	for _, args := range [][]string{
		{"index"},
		{"reindex"},
		{"query", "rate limiting"},
		{"eval"},
	} {
		t.Run(args[0], func(t *testing.T) {
			root := newRootCmd()
			root.SetArgs(args)
			root.SilenceUsage = true
			root.SilenceErrors = true
			err := root.Execute()
			if !errors.Is(err, model.ErrNotImplemented) {
				t.Errorf("cornifer %v: err = %v, want ErrNotImplemented", args, err)
			}
		})
	}
}
