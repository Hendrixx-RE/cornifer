package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newCallersCmd() *cobra.Command {
	var (
		repoPath      string
		depth         int
		minConfidence float64
		globals       globalFlags
	)

	cmd := &cobra.Command{
		Use:   "callers <qualified-name>",
		Short: "Find every (transitive) caller of a symbol, including callers through interface overrides",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTraversal(cmd, args[0], repoPath, &globals, depth, minConfidence, graph.Callers)
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	addTraversalFlags(cmd, &depth, &minConfidence)

	return cmd
}

func newCalleesCmd() *cobra.Command {
	var (
		repoPath      string
		depth         int
		minConfidence float64
		globals       globalFlags
	)

	cmd := &cobra.Command{
		Use:   "callees <qualified-name>",
		Short: "Find every (transitive) symbol a symbol calls, imports, inherits from, or overrides",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTraversal(cmd, args[0], repoPath, &globals, depth, minConfidence, graph.Callees)
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	addTraversalFlags(cmd, &depth, &minConfidence)

	return cmd
}

func addTraversalFlags(cmd *cobra.Command, depth *int, minConfidence *float64) {
	cmd.Flags().IntVar(depth, "depth", 3, "maximum number of edge hops to traverse")
	cmd.Flags().Float64Var(minConfidence, "min-confidence", 0, "exclude edges with confidence below this threshold (0-1)")
}

// runTraversal resolves name to a starting symbol (disambiguating like
// find-definition), runs traverse over the session's Graph, and prints one
// line per reached symbol with the path that reached it.
func runTraversal(cmd *cobra.Command, name, repoPath string, globals *globalFlags, depth int, minConfidence float64,
	traverse func(g *graph.Graph, symbolID int64, opts graph.TraversalOptions) []graph.Path) error {
	ctx := cmd.Context()

	sess, st, err := openSession(ctx, repoPath, globals)
	if err != nil {
		return err
	}
	defer st.Close()

	start, err := resolveOneSymbol(cmd, sess, name)
	if err != nil {
		return err
	}
	if start == nil {
		return nil
	}

	// Restrict to call edges by default: "callers"/"callees" of a symbol
	// means who calls/is called, not the whole reverse-reachable set
	// including import edges (that broader question is blast-radius).
	// graph.Callers always additionally follows Implements edges regardless
	// of Kinds, which is what makes "callers through an interface" work.
	opts := graph.TraversalOptions{
		Kinds:         []model.EdgeKind{model.EdgeKindCalls},
		MaxDepth:      depth,
		MinConfidence: model.Confidence(minConfidence),
	}
	paths := traverse(sess.Graph, start.ID, opts)

	if len(paths) == 0 {
		cmd.Printf("no results within %d hop(s) of %s\n", depth, start.QualifiedName)
		return nil
	}

	cmd.Printf("%d result(s) within %d hop(s) of %s:\n", len(paths), depth, start.QualifiedName)
	for _, p := range paths {
		end, ok := sess.Symbol(p.End())
		if !ok {
			continue
		}
		cmd.Printf("- %s  %s  (depth %d, via %s)\n", end.QualifiedName, sess.Location(end), p.Depth(), describePath(sess, p))
	}
	return nil
}

// resolveOneSymbol finds the unique symbol for name (preferring an exact
// qualified-name match), printing a disambiguation list and returning nil
// (no error) if more than one candidate exists.
func resolveOneSymbol(cmd *cobra.Command, sess *indexer.Session, name string) (*model.Symbol, error) {
	candidates := findByQualifiedOrName(sess, name)
	switch len(candidates) {
	case 0:
		cmd.Printf("no definition found for %q\n", name)
		return nil, nil
	case 1:
		return candidates[0], nil
	default:
		cmd.Printf("%q is ambiguous; candidates:\n", name)
		for _, sym := range candidates {
			cmd.Printf("- %s  %s:%d\n", sym.QualifiedName, fileLocation(sess, sym), sym.StartLine)
		}
		return nil, nil
	}
}

func describePath(sess *indexer.Session, p graph.Path) string {
	parts := make([]string, len(p.Edges))
	for i, e := range p.Edges {
		src, srcOK := sess.Symbol(e.SrcSymbolID)
		dst, dstOK := sess.Symbol(e.DstSymbolID)
		srcName, dstName := fmt.Sprintf("#%d", e.SrcSymbolID), fmt.Sprintf("#%d", e.DstSymbolID)
		if srcOK {
			srcName = src.QualifiedName
		}
		if dstOK {
			dstName = dst.QualifiedName
		}
		parts[i] = fmt.Sprintf("%s(%s->%s,conf=%.2f)", e.Kind, srcName, dstName, e.Confidence)
	}
	return strings.Join(parts, " -> ")
}
