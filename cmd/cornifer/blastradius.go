package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newBlastRadiusCmd() *cobra.Command {
	var (
		repoPath      string
		depth         int
		minConfidence float64
		globals       globalFlags
	)

	cmd := &cobra.Command{
		Use:   "blast-radius <file-path|qualified-name>",
		Short: "What would be affected by a change to this file or symbol (reverse import/call closure)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBlastRadius(cmd, args[0], repoPath, &globals, depth, minConfidence)
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	addTraversalFlags(cmd, &depth, &minConfidence)

	return cmd
}

func runBlastRadius(cmd *cobra.Command, target, repoPath string, globals *globalFlags, depth int, minConfidence float64) error {
	ctx := cmd.Context()

	sess, st, err := openSession(ctx, repoPath, globals)
	if err != nil {
		return err
	}
	defer st.Close()

	starts := blastRadiusStarts(sess, target)
	if len(starts) == 0 {
		cmd.Printf("no file or symbol found for %q\n", target)
		return nil
	}

	opts := graph.TraversalOptions{MaxDepth: depth, MinConfidence: model.Confidence(minConfidence)}

	affected := map[int64]graph.Path{}
	for _, start := range starts {
		for _, p := range graph.BlastRadius(sess.Graph, start.ID, opts) {
			if existing, ok := affected[p.End()]; !ok || p.Depth() < existing.Depth() {
				affected[p.End()] = p
			}
		}
	}

	if len(affected) == 0 {
		cmd.Printf("nothing depends on %s within %d hop(s)\n", target, depth)
		return nil
	}

	// Collapse to distinct files for a file-level summary, since blast
	// radius is most often asked about a file ("what breaks if I change
	// this file?" — plan.md).
	byFile := map[int64][]string{}
	for symID, p := range affected {
		sym, ok := sess.Symbol(symID)
		if !ok {
			continue
		}
		byFile[sym.FileID] = append(byFile[sym.FileID], fmt.Sprintf("%s (depth %d, via %s)", sym.QualifiedName, p.Depth(), describePath(sess, p)))
	}

	cmd.Printf("%d affected symbol(s) across %d file(s) within %d hop(s) of %s:\n", len(affected), len(byFile), depth, target)
	for fileID, syms := range byFile {
		f, ok := sess.File(fileID)
		path := fmt.Sprintf("file#%d", fileID)
		if ok {
			path = f.Path
		}
		cmd.Printf("- %s\n", path)
		for _, s := range syms {
			cmd.Printf("    %s\n", s)
		}
	}
	return nil
}

// blastRadiusStarts resolves target to the symbols blast radius should
// start from: if target names an indexed file (repo-relative path), every
// symbol defined in that file; otherwise, the unique symbol matching
// target as a qualified or bare name.
func blastRadiusStarts(sess *indexer.Session, target string) []*model.Symbol {
	var inFile []*model.Symbol
	for _, f := range sess.Manifest.Files {
		if f.Path == target {
			for _, sym := range sess.Manifest.Symbols {
				if sym.FileID == f.ID {
					inFile = append(inFile, sym)
				}
			}
			return inFile
		}
	}
	return findByQualifiedOrName(sess, target)
}
