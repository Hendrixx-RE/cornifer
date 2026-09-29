package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newFindDefinitionCmd() *cobra.Command {
	var (
		repoPath string
		globals  globalFlags
	)

	cmd := &cobra.Command{
		Use:   "find-definition <name>",
		Short: "Find the definition(s) of a symbol by qualified or bare name, disambiguating duplicates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			sess, st, err := openSession(ctx, repoPath, &globals)
			if err != nil {
				return err
			}
			defer st.Close()

			candidates := findByQualifiedOrName(sess, name)
			if len(candidates) == 0 {
				cmd.Printf("no definition found for %q\n", name)
				return nil
			}
			if len(candidates) == 1 {
				printDefinition(cmd, sess, candidates[0])
				return nil
			}

			cmd.Printf("%d candidates for %q:\n", len(candidates), name)
			for _, sym := range candidates {
				printDefinition(cmd, sess, sym)
			}
			return nil
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)

	return cmd
}

// findByQualifiedOrName finds every symbol whose QualifiedName matches
// name exactly, or, if none do, every symbol whose bare Name matches — the
// same exact + fuzzy fallback plan.md's find_definition calls for, applied
// against the cached Manifest (see internal/indexer/doc.go for why this
// doesn't go through Store.FindSymbolByQualifiedName/FindSymbolsByName
// directly: those return either one arbitrary match or every bare-name
// match, and find-definition needs "every candidate for this exact query",
// trying qualified name first).
func findByQualifiedOrName(sess *indexer.Session, name string) []*model.Symbol {
	var byQualified, byName []*model.Symbol
	for _, sym := range sess.Manifest.Symbols {
		if sym.QualifiedName == name {
			byQualified = append(byQualified, sym)
		}
		if sym.Name == name {
			byName = append(byName, sym)
		}
	}
	if len(byQualified) > 0 {
		return byQualified
	}
	return byName
}

func printDefinition(cmd *cobra.Command, sess *indexer.Session, sym *model.Symbol) {
	cmd.Printf("- %s  %s (%s)  %s:%d-%d\n", sym.QualifiedName, sym.Kind, sym.Name, fileLocation(sess, sym), sym.StartLine, sym.EndLine)
	if sym.Signature != "" {
		cmd.Printf("    %s\n", sym.Signature)
	}
}

func fileLocation(sess *indexer.Session, sym *model.Symbol) string {
	if f, ok := sess.File(sym.FileID); ok {
		return f.Path
	}
	return fmt.Sprintf("file#%d", sym.FileID)
}
