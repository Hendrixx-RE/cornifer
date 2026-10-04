package companion

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
)

func TestGeminiRuntimeCredentialsRolesAndFingerprint(t *testing.T) {
	t.Setenv("CORNIFER_EMBEDDING_PROVIDER", "gemini")
	t.Setenv("CORNIFER_EMBEDDING_MODEL", "")
	t.Setenv("CORNIFER_EMBEDDING_BASE_URL", "")
	t.Setenv("CORNIFER_EMBEDDING_DIM", "1024")
	t.Setenv("CORNIFER_EMBEDDING_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "mock-gemini-key")
	t.Setenv("VOYAGE_API_KEY", "wrong-provider-key")
	cfg := RuntimeConfigFromEnv()
	if !cfg.configuredEmbedding() || cfg.EmbeddingAPIKey != "mock-gemini-key" {
		t.Fatal("Gemini credentials fallback incorrect")
	}
	doc, err := cfg.embedConfig(false, "")
	if err != nil {
		t.Fatal(err)
	}
	query, err := cfg.embedConfig(true, "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Provider != embed.ProviderGemini || doc.Gemini.InputType != "document" || query.Gemini.InputType != "query" || query.Dimension != 1024 {
		t.Fatal("document/query roles or dimension not preserved")
	}
	before := cfg.ProviderFingerprint()
	cfg.EmbeddingAPIKey = "rotated-key"
	if cfg.ProviderFingerprint() != before {
		t.Fatal("key rotation changed vector space")
	}
	cfg.EmbeddingModel = "gemini-embedding-001"
	if cfg.ProviderFingerprint() == before {
		t.Fatal("model fingerprint collision")
	}
	cfg.EmbeddingModel = ""
	cfg.EmbeddingDimension = 768
	if cfg.ProviderFingerprint() == before {
		t.Fatal("dimension fingerprint collision")
	}
	cfg.EmbeddingDimension = 1024
	cfg.EmbeddingBaseURL = "https://other.example/v1beta"
	if cfg.ProviderFingerprint() == before {
		t.Fatal("endpoint fingerprint collision")
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "rotated-key") {
		t.Fatal("runtime config serializes provider key")
	}
	t.Setenv("CORNIFER_EMBEDDING_API_KEY", "explicit-key")
	if RuntimeConfigFromEnv().EmbeddingAPIKey != "explicit-key" {
		t.Fatal("explicit key did not override Gemini environment")
	}
	t.Setenv("CORNIFER_EMBEDDING_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	if RuntimeConfigFromEnv().configuredEmbedding() {
		t.Fatal("Voyage key used for Gemini")
	}
	t.Setenv("CORNIFER_EMBEDDING_PROVIDER", "")
	t.Setenv("GEMINI_API_KEY", "mock-gemini-key")
	if RuntimeConfigFromEnv().configuredEmbedding() {
		t.Fatal("Gemini key alone enabled provider calls")
	}
}
