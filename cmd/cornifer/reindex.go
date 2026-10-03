package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/chunk"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
)

func newReindexCmd() *cobra.Command {
	var (
		repoPath       string
		embedProvider  string
		maxEmbedTokens int
		force          bool
		globals        globalFlags
	)

	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Re-index changed files by content hash, or build a new commit snapshot",
		Long: "Re-index a repository.\n\n" +
			"For an already-indexed root+commit, reindex compares file content hashes,\n" +
			"updates added/changed/deleted files, rebuilds cross-file resolution, and\n" +
			"rebuilds BM25 from authoritative chunks. A new git commit is indexed as a\n" +
			"new immutable snapshot. --force requests the clean full rebuild path.",
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

			var stats *indexer.Stats
			if force {
				stats, err = indexer.Index(ctx, st, cfg)
			} else {
				stats, err = indexer.IncrementalIndex(ctx, st, cfg)
			}
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
	cmd.Flags().BoolVar(&force, "force", false, "re-run the full pipeline even if this commit was already indexed")

	return cmd
}
