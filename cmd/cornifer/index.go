package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/chunk"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
)

func newIndexCmd() *cobra.Command {
	var (
		repoPath       string
		embedProvider  string
		maxEmbedTokens int
		globals        globalFlags
	)

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Parse a repository and build its structural + semantic index from scratch",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()

			cfg := indexer.Config{
				RepoRoot:       repoPath,
				Embedder:       embed.Config{Provider: embed.Provider(embedProvider)},
				ChunkOptions:   chunk.DefaultOptions(),
				MaxEmbedTokens: maxEmbedTokens,
				CacheDir:       globals.cacheDir,
				Logf:           logf,
				IncludeText:    true,
			}

			stats, err := indexer.Index(ctx, st, cfg)
			if err != nil {
				return err
			}

			cmd.Println(stats.String())
			return nil
		},
	}

	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	embedderProviderFlag(cmd, &embedProvider)
	cmd.Flags().IntVar(&maxEmbedTokens, "max-embed-tokens", indexer.DefaultMaxEmbedTokens,
		"chunks whose embedding input exceeds this token count are truncated for embedding (with a warning), not skipped")

	return cmd
}
