package embed

import (
	"os"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestResolveDimensionOverride(t *testing.T) {
	t.Setenv(model.EmbeddingDimEnvVar, "2048")
	got, err := ResolveDimension(512)
	if err != nil {
		t.Fatalf("ResolveDimension() err = %v", err)
	}
	if got != 512 {
		t.Errorf("got %d, want 512 (explicit override wins over env)", got)
	}
}

func TestResolveDimensionFromEnv(t *testing.T) {
	t.Setenv(model.EmbeddingDimEnvVar, "2048")
	got, err := ResolveDimension(0)
	if err != nil {
		t.Fatalf("ResolveDimension() err = %v", err)
	}
	if got != 2048 {
		t.Errorf("got %d, want 2048", got)
	}
}

func TestResolveDimensionDefault(t *testing.T) {
	orig, had := os.LookupEnv(model.EmbeddingDimEnvVar)
	os.Unsetenv(model.EmbeddingDimEnvVar)
	t.Cleanup(func() {
		if had {
			os.Setenv(model.EmbeddingDimEnvVar, orig)
		}
	})
	got, err := ResolveDimension(0)
	if err != nil {
		t.Fatalf("ResolveDimension() err = %v", err)
	}
	if got != model.DefaultEmbeddingDim {
		t.Errorf("got %d, want %d", got, model.DefaultEmbeddingDim)
	}
}

func TestResolveDimensionInvalidEnv(t *testing.T) {
	t.Setenv(model.EmbeddingDimEnvVar, "not-a-number")
	if _, err := ResolveDimension(0); err == nil {
		t.Fatal("ResolveDimension() err = nil, want error for non-numeric env var")
	}
}

func TestResolveDimensionNegativeEnv(t *testing.T) {
	t.Setenv(model.EmbeddingDimEnvVar, "-1")
	if _, err := ResolveDimension(0); err == nil {
		t.Fatal("ResolveDimension() err = nil, want error for negative env var")
	}
}
