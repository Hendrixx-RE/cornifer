package embed

import (
	"fmt"
	"os"
	"strconv"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// ResolveDimension returns override if positive; otherwise it consults the
// model.EmbeddingDimEnvVar environment variable, falling back to
// model.DefaultEmbeddingDim. This mirrors the resolution
// migrations/00007_add_chunks_embedding.go performs for the chunks.embedding
// column, so an Embedder and the store it feeds agree on vector shape
// without either package importing the other's config.
func ResolveDimension(override int) (int, error) {
	if override > 0 {
		return override, nil
	}
	if v, ok := os.LookupEnv(model.EmbeddingDimEnvVar); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("embed: invalid %s=%q: must be a positive integer", model.EmbeddingDimEnvVar, v)
		}
		return n, nil
	}
	return model.DefaultEmbeddingDim, nil
}
