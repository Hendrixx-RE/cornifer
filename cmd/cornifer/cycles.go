package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newCyclesCmd() *cobra.Command {
	var (
		repoPath      string
		minConfidence float64
		imports       bool
		calls         bool
		globals       globalFlags
	)

	cmd := &cobra.Command{
		Use:   "cycles",
		Short: "Find circular dependencies (import cycles by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			sess, st, err := openSession(ctx, repoPath, &globals)
			if err != nil {
				return err
			}
			defer st.Close()

			opts := graph.TraversalOptions{MinConfidence: model.Confidence(minConfidence)}
			if imports {
				opts.Kinds = append(opts.Kinds, model.EdgeKindImports)
			}
			if calls {
				opts.Kinds = append(opts.Kinds, model.EdgeKindCalls)
			}

			sccs := graph.FindCycles(sess.Graph, opts)
			if len(sccs) == 0 {
				cmd.Println("no cycles found")
				return nil
			}

			cmd.Printf("%d cycle(s) found:\n", len(sccs))
			for _, scc := range sccs {
				cmd.Printf("- %d symbol(s): %s\n", len(scc.Nodes), formatCycle(sess, scc.ShortestCycle))
			}
			return nil
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	cmd.Flags().Float64Var(&minConfidence, "min-confidence", 0, "exclude edges with confidence below this threshold (0-1)")
	cmd.Flags().BoolVar(&imports, "imports", false, "include import edges (default when neither --imports nor --calls is set)")
	cmd.Flags().BoolVar(&calls, "calls", false, "include call edges")

	return cmd
}

func formatCycle(sess *indexer.Session, nodes []int64) string {
	names := make([]string, len(nodes))
	for i, id := range nodes {
		if sym, ok := sess.Symbol(id); ok {
			names[i] = fmt.Sprintf("%s (%s)", sym.QualifiedName, fileLocation(sess, sym))
		} else {
			names[i] = fmt.Sprintf("#%d", id)
		}
	}
	return strings.Join(names, " -> ")
}
