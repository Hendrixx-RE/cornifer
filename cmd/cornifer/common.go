package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

// defaultRepoRoot matches scripts/fetch-target-repo.sh's checkout
// destination for the pinned target repo (see TARGET_REPO).
const defaultRepoRoot = "repos/fastapi"

// globalFlags holds flags shared by every subcommand that talks to Postgres
// and/or the local cache (see internal/indexer/doc.go for why a cache
// exists alongside Postgres this wave).
type globalFlags struct {
	cacheDir string
}

func addGlobalFlags(cmd *cobra.Command, f *globalFlags) {
	cmd.Flags().StringVar(&f.cacheDir, "cache-dir", "", "directory for the local BM25 cache (default: "+indexer.DefaultCacheDirName+")")
}

// addRepoFlag adds the --repo flag shared by index/reindex and every
// read-only structural/semantic query command (they must agree on which
// repo root to resolve a commit SHA against).
func addRepoFlag(cmd *cobra.Command, repoPath *string) {
	cmd.Flags().StringVar(repoPath, "repo", defaultRepoRoot, "path to the repository (defaults to TARGET_REPO's checkout under repos/)")
}

// openStore connects to Postgres using CORNIFER_DATABASE_URL (or the
// docker-compose default), per internal/store/config.go.
func openStore(ctx context.Context) (store.Store, error) {
	cfg, err := store.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	st, err := store.NewPostgres(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w (start it with `make up && make migrate`)", err)
	}
	return st, nil
}

// openSession connects to Postgres and bulk-loads the indexed catalog/graph
// for repoPath. The only local cache still used by read commands is BM25.
func openSession(ctx context.Context, repoPath string, f *globalFlags) (*indexer.Session, store.Store, error) {
	st, err := openStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	sess, err := indexer.OpenSession(ctx, st, repoPath, f.cacheDir)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return sess, st, nil
}

// logf prints a timestamp-free progress line to stderr, used as
// indexer.Config.Logf so pipeline progress doesn't get mixed into stdout
// output a script might parse.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// embedderProviderFlag adds the --embed-provider flag shared by commands
// that need an Embedder (index, query).
func embedderProviderFlag(cmd *cobra.Command, provider *string) {
	cmd.Flags().StringVar(provider, "embed-provider", "",
		fmt.Sprintf("embedding provider: %q, %q, or %q (default: %q if %s is set, else %q)",
			embed.ProviderVoyage, embed.ProviderSidecar, embed.ProviderFake,
			embed.ProviderVoyage, embed.VoyageAPIKeyEnvVar, embed.ProviderFake))
}

// embedderProviderForRepo selects the embedding space stored with a snapshot.
// A caller may explicitly repeat it, but may not silently query vectors with
// a different provider.
func embedderProviderForRepo(repo *model.Repo, requested embed.Provider) (embed.Provider, error) {
	if repo == nil || repo.EmbeddingProvider == "" || repo.EmbeddingProvider == "unknown" {
		return requested, nil
	}
	stored := embed.Provider(repo.EmbeddingProvider)
	if requested != "" && requested != stored {
		return "", fmt.Errorf("indexed repo uses embedding provider %q, requested %q; reindex before changing vector spaces", stored, requested)
	}
	return stored, nil
}

// embedderConfigForRepo resolves sidecar configuration and rejects a local
// model that differs from the indexed snapshot. Provider equality alone is
// insufficient for sidecars because one endpoint can serve many vector spaces.
func embedderConfigForRepo(repo *model.Repo, requested embed.Provider) (embed.Config, error) {
	provider, err := embedderProviderForRepo(repo, requested)
	if err != nil {
		return embed.Config{}, err
	}
	cfg := embed.Config{Provider: provider}
	if provider == embed.ProviderVoyage {
		// Voyage uses a retrieval-specific prompt for user queries. The indexer
		// retains the provider default (document) while writing chunk vectors.
		cfg.Voyage.InputType = "query"
	}
	cfg = embed.ConfigFromEnvironment(cfg)
	if provider != embed.ProviderSidecar {
		return cfg, nil
	}
	if cfg.Sidecar.Model == "" {
		return embed.Config{}, fmt.Errorf("sidecar-indexed repo requires %s to name the running model and revision", embed.SidecarModelEnvVar)
	}
	if repo != nil && repo.EmbeddingModel != "" && repo.EmbeddingModel != "unknown" && repo.EmbeddingModel != cfg.Sidecar.Model {
		return embed.Config{}, fmt.Errorf("indexed repo uses embedding model %q, configured sidecar declares %q; reindex before changing vector spaces", repo.EmbeddingModel, cfg.Sidecar.Model)
	}
	return cfg, nil
}
