package embed

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-retry"
)

// providerClient makes exactly one HTTP request for a batch of texts that is
// already within the provider's per-request limits. Transport-level and
// 429/5xx failures are marked with retry.RetryableError so batchingEmbedder
// knows to retry them.
type providerClient interface {
	doEmbed(ctx context.Context, texts []string) ([][]float32, error)
}

// batchingEmbedder splits arbitrarily large inputs into provider-sized
// batches, retries transient failures with exponential backoff and jitter,
// and validates every returned vector's dimension.
type batchingEmbedder struct {
	client     providerClient
	batchSize  int
	maxRetries int
	dim        int
}

func (b *batchingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += b.batchSize {
		end := start + b.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		vecs, err := b.embedBatchWithRetry(ctx, batch)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(batch) {
			return nil, fmt.Errorf("embed: provider returned %d embeddings for %d inputs", len(vecs), len(batch))
		}
		for _, v := range vecs {
			if len(v) != b.dim {
				return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(v), b.dim)
			}
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (b *batchingEmbedder) embedBatchWithRetry(ctx context.Context, batch []string) ([][]float32, error) {
	backoff := retry.NewExponential(DefaultRetryBase)
	backoff = retry.WithJitterPercent(20, backoff)
	backoff = retry.WithCappedDuration(DefaultRetryMax, backoff)
	backoff = retry.WithMaxRetries(uint64(b.maxRetries), backoff)

	return retry.DoValue(ctx, backoff, func(ctx context.Context) ([][]float32, error) {
		return b.client.doEmbed(ctx, batch)
	})
}
