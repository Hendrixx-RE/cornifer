package embed

import "errors"

var (
	// ErrMissingAPIKey is returned by New when Provider is ProviderVoyage
	// and no API key is available from Config.Voyage.APIKey or the
	// VoyageAPIKeyEnvVar environment variable.
	ErrMissingAPIKey = errors.New("embed: voyage API key is not set")

	// ErrDimensionMismatch is returned when a provider returns a vector
	// whose length does not match the configured embedding dimension.
	ErrDimensionMismatch = errors.New("embed: embedding dimension mismatch")
)
