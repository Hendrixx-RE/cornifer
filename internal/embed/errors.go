package embed

import "errors"

var (
	// ErrMissingAPIKey is returned when a hosted provider has no explicit
	// or provider-specific environment key.
	ErrMissingAPIKey = errors.New("embed: hosted embedding API key is not set")

	// ErrDimensionMismatch is returned when a provider returns a vector
	// whose length does not match the configured embedding dimension.
	ErrDimensionMismatch = errors.New("embed: embedding dimension mismatch")
)
