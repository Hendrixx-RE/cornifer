package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newEvalCmd() *cobra.Command {
	var queriesPath string

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the hand-labeled eval set against each retrieval system and report precision@5/recall@5/MRR",
		RunE: func(cmd *cobra.Command, args []string) error {
			return model.ErrNotImplemented
		},
	}

	cmd.Flags().StringVar(&queriesPath, "queries", "eval/queries.yaml", "path to the hand-labeled eval queries file")

	return cmd
}
