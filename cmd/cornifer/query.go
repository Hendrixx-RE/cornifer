package main

import (
	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func newQueryCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Run a hybrid (BM25 + vector + graph) search against the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return model.ErrNotImplemented
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of results to return")

	return cmd
}
