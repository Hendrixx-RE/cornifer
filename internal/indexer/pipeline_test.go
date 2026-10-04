package indexer

import (
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
)

func TestResolvedEmbedConfigUsesDeclaredSidecarEnvironment(t *testing.T) {
	t.Setenv(embed.SidecarEndpointEnvVar, "http://127.0.0.1:18080/embed")
	t.Setenv(embed.SidecarModelEnvVar, "jinaai/jina-embeddings-v2-base-code@516f4baf")

	cfg := resolvedEmbedConfig(embed.Config{Provider: embed.ProviderSidecar})
	if got, want := cfg.Sidecar.Endpoint, "http://127.0.0.1:18080/embed"; got != want {
		t.Errorf("sidecar endpoint = %q, want %q", got, want)
	}
	if got, want := cfg.Sidecar.Model, "jinaai/jina-embeddings-v2-base-code@516f4baf"; got != want {
		t.Errorf("sidecar model = %q, want %q", got, want)
	}
	provider, model := embeddingProvenance(cfg)
	if provider != string(embed.ProviderSidecar) || model != cfg.Sidecar.Model {
		t.Errorf("embeddingProvenance() = (%q, %q), want (%q, %q)", provider, model, embed.ProviderSidecar, cfg.Sidecar.Model)
	}
}

func TestRequireSidecarModel(t *testing.T) {
	if err := requireSidecarModel(embed.Config{Provider: embed.ProviderSidecar}); err == nil {
		t.Fatal("requireSidecarModel() succeeded without a model identity")
	}
	if err := requireSidecarModel(embed.Config{Provider: embed.ProviderSidecar, Sidecar: embed.SidecarConfig{Model: "model@revision"}}); err != nil {
		t.Fatalf("requireSidecarModel() = %v, want nil", err)
	}
}

func TestVoyageProvenanceRecordsDocumentPrompt(t *testing.T) {
	provider, model := embeddingProvenance(embed.Config{Provider: embed.ProviderVoyage})
	if got, want := provider, string(embed.ProviderVoyage); got != want {
		t.Errorf("provider = %q, want %q", got, want)
	}
	if got, want := model, "voyage-code-3;input_type=document"; got != want {
		t.Errorf("model = %q, want %q", got, want)
	}
}

func TestGeminiDocumentProvenanceAndExplicitProviderEnvironment(t *testing.T) {
	t.Setenv("CORNIFER_EMBEDDING_PROVIDER", "gemini")
	t.Setenv("CORNIFER_EMBEDDING_MODEL", "gemini-embedding-2")
	t.Setenv("CORNIFER_EMBEDDING_BASE_URL", "")
	t.Setenv("CORNIFER_EMBEDDING_DIM", "1024")
	cfg := resolvedEmbedConfig(embed.Config{})
	provider, identity := embeddingProvenance(cfg)
	want, err := embed.GeminiIdentity(cfg.Gemini, 1024)
	if err != nil || provider != "gemini" || identity != want {
		t.Fatalf("Gemini provenance mismatch %s: %v", identity, err)
	}
	query := cfg
	query.Gemini.InputType = "query"
	if err := embed.ValidateQuerySpace(provider, identity, query); err != nil {
		t.Fatal(err)
	}
	cfg.Gemini.Model = "unknown-model"
	if err := requireEmbeddingConfig(cfg); err == nil {
		t.Fatal("invalid Gemini model admitted to indexing provenance")
	}
}
