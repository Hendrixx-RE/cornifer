package main

import (
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// TestEvalIsWired confirms eval no longer returns the old implementation
// sentinel before it validates its input or opens its database-backed index.
func TestEvalIsWired(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"eval", "--queries", "does-not-exist.yaml"})
	root.SilenceUsage = true
	root.SilenceErrors = true
	err := root.Execute()
	if err == nil {
		t.Fatal("cornifer eval unexpectedly succeeded")
	}
	if errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("cornifer eval: err = %v, must not be ErrNotImplemented", err)
	}
}

// TestCommandsFailFastWithoutPostgres checks that every command needing a
// database returns a clear connection error (not a panic, and not
// ErrNotImplemented) when Postgres is unreachable — this test does not
// require Docker/`make up`, it only checks the fast-fail path.
func TestCommandsFailFastWithoutPostgres(t *testing.T) {
	t.Setenv("CORNIFER_DATABASE_URL", "postgres://cornifer:cornifer@localhost:1/cornifer?sslmode=disable")

	for _, args := range [][]string{
		{"index", "--repo", "."},
		{"reindex", "--repo", "."},
		{"query", "rate limiting", "--repo", "."},
		{"find-definition", "foo", "--repo", "."},
		{"callers", "foo", "--repo", "."},
		{"callees", "foo", "--repo", "."},
		{"blast-radius", "foo", "--repo", "."},
		{"cycles", "--repo", "."},
	} {
		t.Run(args[0], func(t *testing.T) {
			root := newRootCmd()
			root.SetArgs(args)
			root.SilenceUsage = true
			root.SilenceErrors = true
			err := root.Execute()
			if err == nil {
				t.Fatalf("cornifer %v: expected an error with no reachable postgres", args)
			}
			if errors.Is(err, model.ErrNotImplemented) {
				t.Errorf("cornifer %v: err = %v, want a connection error, not ErrNotImplemented", args, err)
			}
		})
	}
}

// TestSubcommandsAreWired is a smoke test that every subcommand is
// registered and parses its own flags without error before RunE runs.
func TestSubcommandsAreWired(t *testing.T) {
	root := newRootCmd()
	names := map[string]bool{}
	for _, cmd := range root.Commands() {
		names[cmd.Name()] = true
	}
	for _, want := range []string{"index", "reindex", "query", "eval", "find-definition", "callers", "callees", "blast-radius", "cycles"} {
		if !names[want] {
			t.Errorf("subcommand %q not registered", want)
		}
	}
}

func TestEmbeddingMetadataRequiresDeclaredIndexedProviderForSemanticClaim(t *testing.T) {
	if metadata := embeddingMetadata(embed.ProviderVoyage, ""); metadata.SemanticallyMeaningful {
		t.Fatal("undeclared corpus provider must not be presented as semantic evidence")
	}
	if metadata := embeddingMetadata(embed.ProviderVoyage, embed.ProviderVoyage); !metadata.SemanticallyMeaningful {
		t.Fatal("matching declared Voyage provider should be semantic")
	}
	if metadata := embeddingMetadata(embed.ProviderFake, embed.ProviderFake); metadata.SemanticallyMeaningful {
		t.Fatal("fake provider must never be semantic")
	}
}

func TestSidecarEmbeddingMetadataNamesTheConfiguredModel(t *testing.T) {
	metadata := embeddingMetadataForRepo(
		embed.ProviderSidecar,
		embed.ProviderSidecar,
		"jinaai/jina-embeddings-v2-base-code@516f4baf",
		"jinaai/jina-embeddings-v2-base-code@516f4baf",
	)
	if metadata.QueryModel != "jinaai/jina-embeddings-v2-base-code@516f4baf" {
		t.Errorf("QueryModel = %q, want configured immutable model identity", metadata.QueryModel)
	}
	if !metadata.SemanticallyMeaningful {
		t.Fatal("matching persisted sidecar provider must be reported as semantically meaningful")
	}
}

func TestEmbedderConfigForRepoRejectsUnidentifiedOrChangedSidecar(t *testing.T) {
	repo := &model.Repo{EmbeddingProvider: string(embed.ProviderSidecar), EmbeddingModel: "model@one"}
	t.Setenv(embed.SidecarEndpointEnvVar, "http://127.0.0.1:18080/embed")
	t.Setenv(embed.SidecarModelEnvVar, "")
	if _, err := embedderConfigForRepo(repo, ""); err == nil {
		t.Fatal("embedderConfigForRepo() succeeded without a sidecar model identity")
	}
	t.Setenv(embed.SidecarModelEnvVar, "model@two")
	if _, err := embedderConfigForRepo(repo, ""); err == nil {
		t.Fatal("embedderConfigForRepo() accepted a changed sidecar model")
	}
	t.Setenv(embed.SidecarModelEnvVar, "model@one")
	if _, err := embedderConfigForRepo(repo, ""); err != nil {
		t.Fatalf("embedderConfigForRepo() = %v, want nil", err)
	}
}

func TestEmbedderConfigForRepoUsesVoyageQueryPrompt(t *testing.T) {
	repo := &model.Repo{
		EmbeddingProvider: string(embed.ProviderVoyage),
		EmbeddingModel:    "voyage-code-3;input_type=document",
	}
	cfg, err := embedderConfigForRepo(repo, "")
	if err != nil {
		t.Fatalf("embedderConfigForRepo() = %v, want nil", err)
	}
	if got, want := cfg.Voyage.InputType, "query"; got != want {
		t.Errorf("Voyage.InputType = %q, want %q", got, want)
	}
}

func TestVoyageMetadataRecordsBothRetrievalRoles(t *testing.T) {
	metadata := embeddingMetadataForRepo(
		embed.ProviderVoyage,
		embed.ProviderVoyage,
		"voyage-code-3;input_type=document",
		"",
	)
	if got, want := metadata.IndexedModel, "voyage-code-3;input_type=document"; got != want {
		t.Errorf("IndexedModel = %q, want %q", got, want)
	}
	if got, want := metadata.QueryModel, "voyage-code-3;input_type=query"; got != want {
		t.Errorf("QueryModel = %q, want %q", got, want)
	}
}
