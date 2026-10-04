package embed

import "testing"

func TestConfigFromEnvironmentFillsMissingSidecarFields(t *testing.T) {
	t.Setenv(SidecarEndpointEnvVar, "http://127.0.0.1:18080/embed")
	t.Setenv(SidecarModelEnvVar, "jinaai/jina-embeddings-v2-base-code@516f4baf")
	t.Setenv(SidecarBatchSizeEnvVar, "12")
	t.Setenv(SidecarTimeoutSecondsEnvVar, "240")

	cfg := ConfigFromEnvironment(Config{Provider: ProviderSidecar})
	if got, want := cfg.Sidecar.Endpoint, "http://127.0.0.1:18080/embed"; got != want {
		t.Errorf("Endpoint = %q, want %q", got, want)
	}
	if got, want := cfg.Sidecar.Model, "jinaai/jina-embeddings-v2-base-code@516f4baf"; got != want {
		t.Errorf("Model = %q, want %q", got, want)
	}
	if got, want := cfg.BatchSize, 12; got != want {
		t.Errorf("BatchSize = %d, want %d", got, want)
	}
	if cfg.Sidecar.HTTPClient == nil || cfg.Sidecar.HTTPClient.Timeout.Seconds() != 240 {
		t.Errorf("sidecar HTTP timeout = %v, want 240s", cfg.Sidecar.HTTPClient)
	}
}

func TestConfigFromEnvironmentDoesNotOverrideExplicitOrOtherProvider(t *testing.T) {
	t.Setenv(SidecarEndpointEnvVar, "http://environment.invalid/embed")
	t.Setenv(SidecarModelEnvVar, "environment-model")
	t.Setenv(SidecarBatchSizeEnvVar, "99")
	t.Setenv(SidecarTimeoutSecondsEnvVar, "99")

	explicit := ConfigFromEnvironment(Config{
		Provider:  ProviderSidecar,
		BatchSize: 5,
		Sidecar:   SidecarConfig{Endpoint: "http://explicit.invalid/embed", Model: "explicit-model"},
	})
	if explicit.Sidecar.Endpoint != "http://explicit.invalid/embed" || explicit.Sidecar.Model != "explicit-model" {
		t.Errorf("explicit sidecar config was changed: %+v", explicit.Sidecar)
	}
	if explicit.BatchSize != 5 {
		t.Errorf("explicit batch size was changed: %d", explicit.BatchSize)
	}

	fake := ConfigFromEnvironment(Config{Provider: ProviderFake})
	if fake.Sidecar.Endpoint != "" || fake.Sidecar.Model != "" {
		t.Errorf("non-sidecar config unexpectedly received sidecar settings: %+v", fake.Sidecar)
	}
}

func TestGeminiConfigEnvironmentExplicitFieldsWin(t *testing.T) {
	t.Setenv("CORNIFER_EMBEDDING_CACHE_DIR", "")
	t.Setenv("GEMINI_API_KEY", "gemini-env")
	t.Setenv("CORNIFER_EMBEDDING_API_KEY", "")
	t.Setenv("CORNIFER_EMBEDDING_MODEL", "gemini-embedding-001")
	t.Setenv("CORNIFER_EMBEDDING_BASE_URL", "https://environment.example/v1beta")
	t.Setenv("CORNIFER_GEMINI_CONCURRENCY", "3")
	t.Setenv("CORNIFER_GEMINI_REQUESTS_PER_MINUTE", "20")
	cfg := ConfigFromEnvironment(Config{Provider: ProviderGemini})
	if cfg.CacheDir != ".cornifer-cache/embeddings" {
		t.Fatalf("Gemini cache default = %q", cfg.CacheDir)
	}
	if cfg.Gemini.APIKey != "gemini-env" || cfg.Gemini.Model != "gemini-embedding-001" || cfg.Gemini.Concurrency != 3 || cfg.Gemini.RequestsPerMinute != 20 {
		t.Fatal("Gemini environment not resolved")
	}
	explicit := ConfigFromEnvironment(Config{Provider: ProviderGemini, Gemini: GeminiConfig{APIKey: "explicit", Model: DefaultGeminiModel, BaseURL: DefaultGeminiBaseURL, Concurrency: 1, RequestsPerMinute: 10}})
	if explicit.Gemini.APIKey != "explicit" || explicit.Gemini.Model != DefaultGeminiModel || explicit.Gemini.BaseURL != DefaultGeminiBaseURL || explicit.Gemini.Concurrency != 1 || explicit.Gemini.RequestsPerMinute != 10 {
		t.Fatal("Gemini explicit fields overwritten")
	}
	t.Setenv("CORNIFER_EMBEDDING_CACHE_DIR", "custom-cache")
	if ConfigFromEnvironment(Config{Provider: ProviderGemini}).CacheDir != "custom-cache" || ConfigFromEnvironment(Config{Provider: ProviderGemini, CacheDir: "explicit-cache"}).CacheDir != "explicit-cache" {
		t.Fatal("Gemini cache directory precedence incorrect")
	}
}
