package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	cornefval "github.com/Hendrixx-RE/cornifer/internal/eval"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
)

func newEvalCmd() *cobra.Command {
	var (
		queriesPath      string
		outputPath       string
		repoPath         string
		embedProvider    string
		indexedProvider  string
		graphBoostWeight float64
		globals          globalFlags
	)

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the hand-labeled eval set against each retrieval system and report precision@5/recall@5/MRR",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dataset, err := cornefval.Load(queriesPath)
			if err != nil {
				return err
			}

			sess, st, err := openSession(ctx, repoPath, &globals)
			if err != nil {
				return err
			}
			defer st.Close()

			sparse, err := sess.LoadBM25(globals.cacheDir)
			if err != nil {
				return err
			}
			embedCfg, err := embedderConfigForRepo(sess.Repo, embed.Provider(embedProvider))
			if err != nil {
				return err
			}
			provider := resolvedEmbedProvider(embedCfg.Provider)
			embedCfg.Provider = provider
			indexProvider := embed.Provider(indexedProvider)
			if sess.Repo.EmbeddingProvider != "" && sess.Repo.EmbeddingProvider != "unknown" {
				indexProvider = embed.Provider(sess.Repo.EmbeddingProvider)
			}
			if indexProvider != "" && indexProvider != provider {
				return fmt.Errorf("index embedding provider %q does not match query provider %q; vector retrieval requires the same embedding space", indexProvider, provider)
			}
			embedder, err := indexer.BuildEmbedder(embedCfg)
			if err != nil {
				return fmt.Errorf("build embedder: %w", err)
			}

			report, err := cornefval.Run(ctx, dataset, cornefval.Config{
				RepoRoot:              sess.Manifest.Root,
				CommitSHA:             sess.Manifest.CommitSHA,
				Manifest:              sess.Manifest,
				Sparse:                sparse,
				Vector:                sess.VectorSearcher(),
				Embedder:              embedder,
				Embedding:             embeddingMetadataForRepo(provider, indexProvider, sess.Repo.EmbeddingModel, queryModelIdentity(embedCfg)),
				GraphBoost:            sess.GraphBoost(graphBoostWeight),
				GraphBoostDescription: fmt.Sprintf("repo-scoped one-hop graph adjacency and same-file boost; weight=%.6f", graphBoostWeight),
			})
			if err != nil {
				return err
			}
			if outputPath == "" {
				outputPath = filepath.Join("eval", "results", fmt.Sprintf("fastapi-%s-%s.json", shortSHA(sess.Manifest.CommitSHA), provider))
			}
			if err := cornefval.WriteReport(outputPath, report); err != nil {
				return err
			}

			cmd.Printf("wrote raw results: %s\n", outputPath)
			cmd.Printf("target: %s @ %s; labels: source=%d ide=%d\n", report.Target.Repository, report.IndexCommit, report.LabelCounts[cornefval.VerificationSource], report.LabelCounts[cornefval.VerificationIDE])
			for _, result := range report.Systems {
				cmd.Printf("%-28s precision@5=%.3f recall@5=%.3f MRR=%.3f\n", result.System, result.Metrics.Precision5, result.Metrics.Recall5, result.Metrics.MRR)
			}
			if !report.GraphBoost.Available {
				cmd.Printf("graph boost: unavailable — %s\n", report.GraphBoost.Reason)
			}
			if !report.Embedding.SemanticallyMeaningful {
				if provider == embed.ProviderFake {
					cmd.PrintErr("warning: fake embeddings are deterministic but not semantic; vector/hybrid rows are pipeline checks, not semantic-retrieval evidence\n")
				} else {
					cmd.PrintErr("warning: the indexed embedding provider was not declared; vector/hybrid rows are not semantic-retrieval evidence until --indexed-embed-provider confirms it\n")
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&queriesPath, "queries", "eval/queries.yaml", "path to the hand-labeled eval queries file")
	cmd.Flags().StringVar(&outputPath, "output", "", "path for raw JSON results (default: eval/results/fastapi-<commit>-<provider>.json)")
	addRepoFlag(cmd, &repoPath)
	addGlobalFlags(cmd, &globals)
	embedderProviderFlag(cmd, &embedProvider)
	cmd.Flags().StringVar(&indexedProvider, "indexed-embed-provider", "", "provider used while indexing (must match --embed-provider when set; otherwise recorded as an unverified assumption)")
	cmd.Flags().Float64Var(&graphBoostWeight, "graph-boost-weight", retrieve.DefaultGraphBoostWeight, "post-fusion graph adjacency boost (0 disables it and omits the ablation)")

	return cmd
}

func resolvedEmbedProvider(provider embed.Provider) embed.Provider {
	if provider != "" {
		return provider
	}
	if selected := os.Getenv("CORNIFER_EMBEDDING_PROVIDER"); selected != "" {
		return embed.Provider(selected)
	}
	if os.Getenv(embed.VoyageAPIKeyEnvVar) != "" {
		return embed.ProviderVoyage
	}
	return embed.ProviderFake
}

func queryModelIdentity(cfg embed.Config) string {
	if cfg.Provider == embed.ProviderGemini {
		identity, _ := embed.GeminiIdentity(cfg.Gemini, cfg.Dimension)
		return identity + ";input_type=query"
	}
	return cfg.Sidecar.Model
}

func embeddingMetadata(provider, indexedProvider embed.Provider) cornefval.EmbeddingMetadata {
	return embeddingMetadataForRepo(provider, indexedProvider, "", "")
}

func embeddingMetadataForRepo(provider, indexedProvider embed.Provider, indexedModel, queryModel string) cornefval.EmbeddingMetadata {
	indexedSource := "declared with --indexed-embed-provider"
	providerDeclared := indexedProvider != ""
	if indexedProvider == "" {
		// Index manifests from the prior milestone do not persist embedding
		// provenance. Keep the convenient default, but make the assumption
		// visible in raw results rather than misrepresenting it as observed.
		indexedProvider = provider
		indexedSource = "assumed equal to query provider; index manifest does not record it"
	}
	switch provider {
	case embed.ProviderGemini:
		return cornefval.EmbeddingMetadata{QueryProvider: string(provider), QueryModel: queryModel, IndexedProvider: string(indexedProvider), IndexedModel: indexedModel, IndexProviderSource: indexedSource, SemanticallyMeaningful: providerDeclared && indexedProvider == provider && strings.TrimSuffix(queryModel, ";input_type=query") == indexedModel}
	case embed.ProviderVoyage:
		if indexedModel == "" || indexedModel == "unknown" {
			indexedModel = embed.DefaultVoyageModel + ";input_type=document"
		}
		if indexedProvider != "" && indexedSource != "assumed equal to query provider; index manifest does not record it" {
			indexedSource, providerDeclared = "persisted with indexed repo", true
		}
		return cornefval.EmbeddingMetadata{QueryProvider: string(provider), QueryModel: embed.DefaultVoyageModel + ";input_type=query", IndexedProvider: string(indexedProvider), IndexedModel: indexedModel, IndexProviderSource: indexedSource, SemanticallyMeaningful: providerDeclared}
	case embed.ProviderSidecar:
		if queryModel == "" {
			queryModel = "sidecar model not declared"
		}
		if indexedModel == "" || indexedModel == "unknown" {
			indexedModel = queryModel
		}
		if indexedProvider != "" && indexedSource != "assumed equal to query provider; index manifest does not record it" {
			indexedSource, providerDeclared = "persisted with indexed repo", true
		}
		return cornefval.EmbeddingMetadata{QueryProvider: string(provider), QueryModel: queryModel, IndexedProvider: string(indexedProvider), IndexedModel: indexedModel, IndexProviderSource: indexedSource, SemanticallyMeaningful: providerDeclared}
	default:
		return cornefval.EmbeddingMetadata{QueryProvider: string(provider), QueryModel: "deterministic hash-derived fake", IndexedProvider: string(indexedProvider), IndexedModel: "deterministic hash-derived fake", IndexProviderSource: indexedSource, SemanticallyMeaningful: false}
	}
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
