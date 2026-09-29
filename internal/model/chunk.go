package model

// DefaultEmbeddingDim is the pgvector embedding dimension used when the
// CORNIFER_EMBEDDING_DIM environment variable is unset. It matches Voyage's
// voyage-code-3 default output dimension — plan.md's primary embedding
// model choice ("Embedding model" decision table).
//
// The dimension is baked into the `chunks.embedding` column type
// (`vector(N)`) at migration time by migrations/00007_add_chunks_embedding.go,
// which reads CORNIFER_EMBEDDING_DIM and falls back to this constant.
// Because pgvector fixes a column's dimension at creation, switching
// embedding models to one with a different output dimension after data
// exists requires a new migration (drop + recreate the column and its HNSW
// index), not just an env var change. Any Embedder or Store implementation
// should validate vector lengths against this same configured dimension so
// a mismatch fails fast instead of corrupting the index.
const DefaultEmbeddingDim = 1024

// EmbeddingDimEnvVar is the environment variable that overrides
// DefaultEmbeddingDim, consulted by the migration that creates the
// `chunks.embedding` column. Later waves (embed, store) should read the
// same variable so the configured dimension stays consistent end to end.
const EmbeddingDimEnvVar = "CORNIFER_EMBEDDING_DIM"

// Chunk is a retrieval unit: a contiguous span of source text prepared for
// hybrid (BM25 + vector) search.
type Chunk struct {
	ID int64

	// SymbolID is the Symbol this chunk was carved from. Nil for
	// module-level code that sits outside any def/class (see plan.md
	// "AST-aware chunking": "Module-level code not inside any def gets its
	// own chunk").
	SymbolID *int64
	FileID   int64

	// StartLine and EndLine are the 1-indexed, inclusive source lines Text
	// was sliced from. Persisted by internal/store as the chunks.start_line
	// and chunks.end_line columns (migrations/00009_add_chunk_lines.sql);
	// rows inserted before that migration read back as 0/0 ("unknown span").
	StartLine int
	EndLine   int

	// Text is the raw chunk source text, exactly as it appears in the file
	// (no context header prepended).
	Text string

	// ContextHeader is prepended to Text to form the string actually sent
	// to the Embedder: file path, enclosing class, imports used, and
	// signature, per plan.md "AST-aware chunking". Stored separately from
	// Text so callers can render just the source or just the header.
	ContextHeader string

	// TokenCount is the token count of ContextHeader+Text under whatever
	// tokenizer the chunk/embed packages standardize on, used to enforce
	// chunking token budgets.
	TokenCount int

	// Embedding is the dense vector for ContextHeader+Text, produced by an
	// Embedder. Its length must equal the configured embedding dimension
	// (DefaultEmbeddingDim unless overridden by EmbeddingDimEnvVar); it is
	// nil until embedding has run for this chunk.
	Embedding []float32
}
