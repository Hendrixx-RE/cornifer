package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newIndexCmd() *cobra.Command {
	var repoPath string

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Parse a repository and build its structural + semantic index from scratch",
		RunE: func(cmd *cobra.Command, args []string) error {
			return model.ErrNotImplemented
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "path to the repository to index (defaults to TARGET_REPO's checkout under repos/)")

	return cmd
}
