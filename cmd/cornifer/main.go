// Command cornifer is the Cornifer CLI: index, reindex, query, and eval
// subcommands over a Postgres-backed structural + semantic index of a
// target repo. See plan.md for the full architecture; Phase 0 wires the
// command surface with every subcommand returning model.ErrNotImplemented
// — later waves fill in the RunE bodies.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cornifer",
		Short: "Repository intelligence engine: structural + semantic code search",
		Long: "Cornifer parses a repository into a structural index (symbols, imports, call graph)\n" +
			"and a semantic index (AST-aware chunks, embeddings, BM25), and answers queries by\n" +
			"combining exact structural lookups with hybrid retrieval.",
	}

	root.AddCommand(
		newIndexCmd(),
		newReindexCmd(),
		newQueryCmd(),
		newEvalCmd(),
	)

	return root
}
