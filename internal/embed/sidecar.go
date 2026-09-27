package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sethvargo/go-retry"
)

// sidecarClient calls a local HTTP sidecar's /embed-style endpoint — plan.md's
// "Local model via a small sidecar" embedding bridge option, for a
// sentence-transformers or text-embeddings-inference container serving
// jina-embeddings-v2-base-code.
type sidecarClient struct {
	endpoint   string
	model      string
	httpClient *http.Client
}

func newSidecarClient(cfg SidecarConfig) (*sidecarClient, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("embed: SidecarConfig.Endpoint is required")
	}
	model := cfg.Model
	if model == "" {
		model = cfg.Endpoint
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	return &sidecarClient{endpoint: cfg.Endpoint, model: model, httpClient: httpClient}, nil
}

type sidecarRequest struct {
	Inputs []string `json:"inputs"`
}

// sidecarResponseObject is the shape used when the sidecar wraps its output
// in an object rather than returning a bare array (text-embeddings-inference
// returns a bare `[][]float32`; some sentence-transformers wrappers return
// `{"embeddings": [][]float32}`). doEmbed accepts either.
type sidecarResponseObject struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *sidecarClient) doEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody, err := json.Marshal(sidecarRequest{Inputs: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal sidecar request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("embed: build sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, retry.RetryableError(fmt.Errorf("embed: sidecar request failed: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, retry.RetryableError(fmt.Errorf("embed: read sidecar response: %w", err))
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, retry.RetryableError(fmt.Errorf("embed: sidecar returned %d: %s", resp.StatusCode, bytes.TrimSpace(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: sidecar returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var bare [][]float32
	if err := json.Unmarshal(body, &bare); err == nil {
		if len(bare) != len(texts) {
			return nil, fmt.Errorf("embed: sidecar returned %d embeddings for %d inputs", len(bare), len(texts))
		}
		return bare, nil
	}

	var obj sidecarResponseObject
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("embed: decode sidecar response: %w", err)
	}
	if len(obj.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: sidecar returned %d embeddings for %d inputs", len(obj.Embeddings), len(texts))
	}
	return obj.Embeddings, nil
}
