package embed

import (
	"net/http"
	"time"
)

// Provider selects which Embedder implementation New builds.
type Provider string

const (
	// ProviderVoyage calls the hosted Voyage voyage-code-3 API — plan.md's
	// primary embedding bridge choice.
	ProviderVoyage Provider = "voyage"
	// ProviderSidecar calls a local HTTP sidecar (sentence-transformers or
	// text-embeddings-inference) serving jina-embeddings-v2-base-code.
	ProviderSidecar Provider = "sidecar"
	// ProviderFake returns deterministic, hash-derived vectors with no
	// network access or API key, for tests and CI.
	ProviderFake Provider = "fake"
)

// Defaults used when the corresponding Config field is zero.
const (
	DefaultBatchSize   = 128
	DefaultMaxRetries  = 5
	DefaultRetryBase   = 500 * time.Millisecond
	DefaultRetryMax    = 30 * time.Second
	DefaultHTTPTimeout = 60 * time.Second

	DefaultVoyageModel   = "voyage-code-3"
	DefaultVoyageBaseURL = "https://api.voyageai.com/v1/embeddings"

	// VoyageAPIKeyEnvVar is the environment variable the Voyage client reads
	// its API key from when Config.Voyage.APIKey is empty.
	VoyageAPIKeyEnvVar = "VOYAGE_API_KEY"
)

// Config selects and configures the Embedder built by New.
type Config struct {
	Provider Provider

	// Dimension overrides the embedding dimension used for validation and
	// cache keys. Zero means "resolve via ResolveDimension" (which in turn
	// consults model.EmbeddingDimEnvVar, falling back to
	// model.DefaultEmbeddingDim).
	Dimension int

	// BatchSize caps how many texts are sent per provider request. Zero
	// means DefaultBatchSize.
	BatchSize int

	// MaxRetries caps retry attempts on 429/5xx/network errors. Zero means
	// DefaultMaxRetries.
	MaxRetries int

	// CacheDir, if non-empty, enables an on-disk cache of embeddings keyed
	// by a hash of (model identifier, dimension, text). Empty disables
	// caching.
	CacheDir string

	Voyage  VoyageConfig
	Sidecar SidecarConfig
}

// VoyageConfig configures the Voyage voyage-code-3 HTTP client.
type VoyageConfig struct {
	// APIKey is the Voyage API key. If empty, it is read from the
	// VoyageAPIKeyEnvVar environment variable; New returns ErrMissingAPIKey
	// if neither is set, so a missing key fails fast with an actionable
	// message rather than as an opaque 401 mid-run.
	APIKey string

	// Model is the Voyage model name sent in each request. Empty means
	// DefaultVoyageModel.
	Model string

	// BaseURL is the embeddings endpoint. Empty means DefaultVoyageBaseURL.
	BaseURL string

	// HTTPClient, if set, overrides the default HTTP client (used by tests
	// to point at an httptest server, and by callers who want custom
	// timeouts/transport).
	HTTPClient *http.Client
}

// SidecarConfig configures a local sidecar HTTP client — plan.md's "Local
// model via a small sidecar" embedding bridge option.
type SidecarConfig struct {
	// Endpoint is the sidecar's /embed URL. Required.
	Endpoint string

	// Model identifies the sidecar's model for cache namespacing only (the
	// sidecar itself is not told which model to run; that's a property of
	// how it was started). Defaults to Endpoint.
	Model string

	HTTPClient *http.Client
}
