package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newReindexCmd() *cobra.Command {
	var repoPath string

	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Incrementally reindex a repository, re-parsing only files whose content hash changed",
		RunE: func(cmd *cobra.Command, args []string) error {
			return model.ErrNotImplemented
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "path to the repository to reindex (defaults to TARGET_REPO's checkout under repos/)")

	return cmd
}
