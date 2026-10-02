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
