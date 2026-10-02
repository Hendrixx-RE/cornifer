// Command cornifer is the Cornifer CLI: index, reindex, query, eval, and
// structural (find-definition, callers, callees, blast-radius, cycles)
// subcommands over a Postgres-backed structural + semantic index of a
// target repo. See plan.md for the full architecture. index/reindex/query
// and the structural commands are implemented by internal/indexer; eval
// loads pinned labels and compares the available retrieval systems.
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
		newFindDefinitionCmd(),
		newCallersCmd(),
		newCalleesCmd(),
		newBlastRadiusCmd(),
		newCyclesCmd(),
	)

	return root
}
