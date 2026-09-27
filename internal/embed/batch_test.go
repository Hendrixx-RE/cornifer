package embed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sethvargo/go-retry"
)

// recordingClient wraps a fn and records the batches it was called with.
type recordingClient struct {
	fn      func(ctx context.Context, texts []string) ([][]float32, error)
	batches [][]string
}

func (c *recordingClient) doEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	c.batches = append(c.batches, append([]string(nil), texts...))
	return c.fn(ctx, texts)
}

func vecsOf(n, dim int) [][]float32 {
	out := make([][]float32, n)
	for i := range out {
		out[i] = make([]float32, dim)
	}
	return out
}

func TestBatchingEmbedderSplitsIntoBatches(t *testing.T) {
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return vecsOf(len(texts), 4), nil
	}}
	b := &batchingEmbedder{client: client, batchSize: 2, maxRetries: 3, dim: 4}

	texts := []string{"a", "b", "c", "d", "e"}
	out, err := b.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if len(out) != len(texts) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(texts))
	}
	wantBatches := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if len(client.batches) != len(wantBatches) {
		t.Fatalf("got %d batches, want %d: %v", len(client.batches), len(wantBatches), client.batches)
	}
	for i, wb := range wantBatches {
		if !equalStrings(client.batches[i], wb) {
			t.Errorf("batch[%d] = %v, want %v", i, client.batches[i], wb)
		}
	}
}

func TestBatchingEmbedderEmptyInput(t *testing.T) {
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		t.Fatal("doEmbed should not be called for empty input")
		return nil, nil
	}}
	b := &batchingEmbedder{client: client, batchSize: 2, maxRetries: 3, dim: 4}
	out, err := b.Embed(context.Background(), nil)
	if err != nil || out != nil {
		t.Fatalf("Embed(nil) = %v, %v, want nil, nil", out, err)
	}
}

func TestBatchingEmbedderDimensionMismatch(t *testing.T) {
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return vecsOf(len(texts), 3), nil // wrong dim
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 3, dim: 4}

	_, err := b.Embed(context.Background(), []string{"a"})
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("Embed() err = %v, want ErrDimensionMismatch", err)
	}
}

func TestBatchingEmbedderWrongCount(t *testing.T) {
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return vecsOf(len(texts)-1, 4), nil // one short
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 3, dim: 4}

	_, err := b.Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error for mismatched vector count")
	}
}

func TestBatchingEmbedderRetriesRetryableError(t *testing.T) {
	var attempts int32
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return nil, retry.RetryableError(errors.New("429 too many requests"))
		}
		return vecsOf(len(texts), 4), nil
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 5, dim: 4}
	// Shrink backoff timing for the test via a custom retry loop by
	// exercising embedBatchWithRetry directly is not needed: production
	// backoff base is 500ms, so drive this through a short deadline instead.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := b.Embed(ctx, []string{"a"})
	if err != nil {
		t.Fatalf("Embed() err = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestBatchingEmbedderNonRetryableFailsFast(t *testing.T) {
	var attempts int32
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, errors.New("400 bad request")
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 5, dim: 4}

	_, err := b.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed() err = nil, want error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (non-retryable errors must not retry)", got)
	}
}

func TestBatchingEmbedderExhaustsRetriesAndReturnsUnderlyingError(t *testing.T) {
	var attempts int32
	wantErr := errors.New("still failing")
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, retry.RetryableError(wantErr)
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 2, dim: 4}

	_, err := b.Embed(context.Background(), []string{"a"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Embed() err = %v, want wrapping %v", err, wantErr)
	}
	// maxRetries=2 means at most 3 attempts (1 initial + 2 retries).
	if got := atomic.LoadInt32(&attempts); got < 1 || got > 3 {
		t.Errorf("attempts = %d, want between 1 and 3", got)
	}
}

func TestBatchingEmbedderHonoursContextCancellation(t *testing.T) {
	client := &recordingClient{fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, retry.RetryableError(errors.New("always fails"))
	}}
	b := &batchingEmbedder{client: client, batchSize: 8, maxRetries: 100, dim: 4}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		_, err := b.Embed(ctx, []string{"a"})
		if err == nil {
			t.Error("Embed() err = nil, want context error")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Embed() did not return promptly after context cancellation")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
