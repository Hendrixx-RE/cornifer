package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
)

func newQueryCmd() *cobra.Command {
	var (
		limit            int
		graphBoostWeight float64
		repoPath         string
		embedProvider    string
		globals          globalFlags
	)

	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Run a hybrid (BM25 + vector) search against the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			query := args[0]

			sess, st, err := openSession(ctx, repoPath, &globals)
			if err != nil {
				return err
			}
			defer st.Close()

			provider, err := embedderProviderForRepo(sess.Repo, embed.Provider(embedProvider))
			if err != nil {
				return err
			}
			embedder, err := indexer.BuildEmbedder(embed.Config{Provider: provider})
			if err != nil {
				return fmt.Errorf("build embedder: %w", err)
			}

			searcher, err := sess.NewHybridSearcher(globals.cacheDir, embedder, retrieve.Config{Boost: sess.GraphBoost(graphBoostWeight)})
			if err != nil {
				return err
			}

			result, err := searcher.Search(ctx, query, limit)
			if err != nil {
				return err
			}
			for _, failure := range result.Failed {
				cmd.PrintErrf("warning: %v\n", failure)
			}

			if len(result.Hits) == 0 {
				cmd.Println("no results")
				return nil
			}

			for i, hit := range result.Hits {
				cm, ok := sess.Chunk(hit.ChunkID)
				loc := fmt.Sprintf("chunk#%d", hit.ChunkID)
				preview := ""
				if ok {
					if f, ok := sess.File(cm.FileID); ok {
						loc = fmt.Sprintf("%s:%d-%d", f.Path, cm.StartLine, cm.EndLine)
					}
					preview = firstLines(cm.Text, 3)
				}

				sources := make([]string, len(hit.Sources))
				for j, src := range hit.Sources {
					sources[j] = fmt.Sprintf("%s(rank=%d,score=%.4f)", src.Retriever, src.Rank, src.Score)
				}

				cmd.Printf("%d. %s  score=%.4f  [%s]\n", i+1, loc, hit.Score, strings.Join(sources, ", "))
				if preview != "" {
					cmd.Printf("   %s\n", strings.ReplaceAll(preview, "\n", "\n   "))
				}
			}
			return nil
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	embedderProviderFlag(cmd, &embedProvider)
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of results to return")
	cmd.Flags().Float64Var(&graphBoostWeight, "graph-boost-weight", retrieve.DefaultGraphBoostWeight, "post-fusion graph adjacency boost (0 disables it)")

	return cmd
}

// firstLines returns up to n lines of s, trimmed, for a short preview.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
