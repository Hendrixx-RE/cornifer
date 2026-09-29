package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/chunk"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/store"
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
		Short: "Re-index a repository, skipping the run if its current commit is already indexed",
		Long: "Re-index a repository.\n\n" +
			"This wave does not yet implement the file-level incremental diff plan.md\n" +
			"describes for Week 3 (\"diff content_hash, re-parse changed files ...\"):\n" +
			"internal/store has no per-file content_hash comparison query yet. Instead,\n" +
			"reindex checks whether the repo's current commit SHA was already indexed\n" +
			"(store.GetRepoByCommit) and, if so, does nothing unless --force is given;\n" +
			"otherwise it runs the same full pipeline as `cornifer index`, creating a new\n" +
			"Repo row for the new commit (old rows are left in place, per plan.md: " +
			"\"Re-indexing the same root at a different commit creates a new Repo row\").",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()

			if !force {
				absRoot, err := indexer.AbsRepoRoot(repoPath)
				if err != nil {
					return err
				}
				commitSHA, err := indexer.ResolveCommitSHA(absRoot)
				if err != nil {
					return err
				}
				if repo, err := st.GetRepoByCommit(ctx, absRoot, commitSHA); err == nil {
					cmd.Printf("repo %d already indexed at commit %s (indexed_at=%s); nothing to do (use --force to re-run)\n",
						repo.ID, repo.CommitSHA, repo.IndexedAt)
					return nil
				} else if !errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("check existing index: %w", err)
				}
			}

			cfg := indexer.Config{
				RepoRoot:       repoPath,
				Embedder:       embed.Config{Provider: embed.Provider(embedProvider)},
				ChunkOptions:   chunk.DefaultOptions(),
				MaxEmbedTokens: maxEmbedTokens,
				CacheDir:       globals.cacheDir,
				Logf:           logf,
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
	cmd.Flags().BoolVar(&force, "force", false, "re-run the full pipeline even if this commit was already indexed")

	return cmd
}
