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
