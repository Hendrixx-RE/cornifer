package indexer

import (
	"github.com/Hendrixx-RE/cornifer/internal/chunk"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
)

// Defaults used when the corresponding Config field is zero.
const (
	// DefaultMaxEmbedTokens caps how many tokens (per chunk.CountTokens) of
	// a chunk's EmbeddingText are sent to the Embedder. A handful of chunks
	// in a real repo blow past any AST-aware chunker's target budget (a
	// single oversized statement that cannot be split mid-statement, e.g.
	// plan.md's "one FastAPI chunk is ~50K tokens" example); rather than
	// fail the whole batch, Index truncates just that chunk's embedding
	// input to this many tokens (logging a warning) and still stores its
	// full Text/TokenCount untruncated.
	DefaultMaxEmbedTokens = 8000

	// DefaultCacheDirName is the directory name (relative to the current
	// working directory unless Config.CacheDir is set) Index writes its
	// Manifest and BM25 index files under.
	DefaultCacheDirName = ".cornifer-cache"
)

// Config configures a full indexing run.
type Config struct {
	// RepoRoot is the filesystem path to the repository to index.
	RepoRoot string

	// Embedder builds the Embedder used to vectorize chunks. The zero value
	// (embed.Config{}) selects ProviderFake unless overridden by the
	// caller, so indexing works with no network access and no API key.
	Embedder embed.Config

	// ChunkOptions controls internal/chunk's token budgets. The zero value
	// selects chunk.DefaultOptions().
	ChunkOptions chunk.Options

	// MaxEmbedTokens is the per-chunk embedding-input token ceiling
	// described on DefaultMaxEmbedTokens. Zero selects the default.
	MaxEmbedTokens int

	// CacheDir is where the Manifest and BM25 index are written/read.
	// Empty selects DefaultCacheDirName in the current working directory.
	CacheDir string

	// Logf receives progress and warning lines (resolution stats, oversized
	// chunk warnings, per-phase timings). Nil discards them.
	Logf func(format string, args ...any)
}

func (c Config) log(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c Config) maxEmbedTokens() int {
	if c.MaxEmbedTokens > 0 {
		return c.MaxEmbedTokens
	}
	return DefaultMaxEmbedTokens
}
