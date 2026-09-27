package store

import (
	"fmt"
	"os"
	"strconv"
)

// DefaultDatabaseURL matches the docker-compose Postgres started by `make
// up` (see docker-compose.yml, host port 5433) and the default used by
// cmd/migrate.
const DefaultDatabaseURL = "postgres://cornifer:cornifer@localhost:5433/cornifer?sslmode=disable"

// DatabaseURLEnvVar is the environment variable read for the Postgres
// connection string, shared with cmd/migrate's CORNIFER_DATABASE_URL.
const DatabaseURLEnvVar = "CORNIFER_DATABASE_URL"

// MaxConnsEnvVar overrides the pgxpool's maximum connection count.
const MaxConnsEnvVar = "CORNIFER_DB_MAX_CONNS"

// DistanceOperatorEnvVar overrides VectorSearch's distance operator. Must
// name one of the DistanceXxx constants below.
const DistanceOperatorEnvVar = "CORNIFER_VECTOR_DISTANCE_OP"

// Distance operators supported by VectorSearch, matching pgvector's
// operator classes. DistanceCosine is the default and is the only one that
// matches the HNSW index built by migrations/00007_add_chunks_embedding.go
// (`vector_cosine_ops`); picking a different operator here without also
// changing that index's operator class means VectorSearch will still
// return correct results but will fall back to a sequential scan instead
// of using the HNSW index.
const (
	DistanceCosine             = "<=>"
	DistanceL2                 = "<->"
	DistanceInnerProduct       = "<#>"
	DefaultMaxConns      int32 = 10
)

// Config configures NewPostgres's connection pool.
type Config struct {
	// DSN is a libpq/pgx connection string, e.g.
	// "postgres://user:pass@host:port/db?sslmode=disable".
	DSN string

	// MaxConns is the pgxpool maximum connection count. Zero means "use the
	// package default" (DefaultMaxConns).
	MaxConns int32

	// DistanceOperator is the pgvector operator VectorSearch orders by. Must
	// be one of DistanceCosine, DistanceL2, or DistanceInnerProduct. Empty
	// means "use DistanceCosine", matching the HNSW index's operator class.
	DistanceOperator string
}

// ConfigFromEnv builds a Config from CORNIFER_DATABASE_URL,
// CORNIFER_DB_MAX_CONNS, and CORNIFER_VECTOR_DISTANCE_OP, falling back to
// defaults that match docker-compose.yml when unset.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		DSN:              os.Getenv(DatabaseURLEnvVar),
		DistanceOperator: os.Getenv(DistanceOperatorEnvVar),
	}
	if cfg.DSN == "" {
		cfg.DSN = DefaultDatabaseURL
	}
	if cfg.DistanceOperator == "" {
		cfg.DistanceOperator = DistanceCosine
	}
	if !validDistanceOperator(cfg.DistanceOperator) {
		return Config{}, fmt.Errorf("store: %s=%q is not a supported distance operator (want %q, %q, or %q)",
			DistanceOperatorEnvVar, cfg.DistanceOperator, DistanceCosine, DistanceL2, DistanceInnerProduct)
	}

	if v := os.Getenv(MaxConnsEnvVar); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("store: %s=%q is not an integer: %w", MaxConnsEnvVar, v, err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("store: %s=%d must be positive", MaxConnsEnvVar, n)
		}
		cfg.MaxConns = int32(n)
	}

	return cfg, nil
}

func validDistanceOperator(op string) bool {
	switch op {
	case DistanceCosine, DistanceL2, DistanceInnerProduct:
		return true
	default:
		return false
	}
}
