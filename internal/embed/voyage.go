package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/sethvargo/go-retry"
)

// voyageClient calls the Voyage embeddings API
// (https://api.voyageai.com/v1/embeddings) for the voyage-code-3 model —
// plan.md's primary embedding bridge choice.
type voyageClient struct {
	apiKey     string
	model      string
	baseURL    string
	dim        int
	httpClient *http.Client
}

func newVoyageClient(cfg VoyageConfig, dim int) (*voyageClient, error) {
	key := cfg.APIKey
	if key == "" {
		key = os.Getenv(VoyageAPIKeyEnvVar)
	}
	if key == "" {
		return nil, fmt.Errorf("%w: set %s or Config.Voyage.APIKey", ErrMissingAPIKey, VoyageAPIKeyEnvVar)
	}

	model := cfg.Model
	if model == "" {
		model = DefaultVoyageModel
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultVoyageBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}

	return &voyageClient{apiKey: key, model: model, baseURL: baseURL, dim: dim, httpClient: httpClient}, nil
}

type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type,omitempty"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

type voyageEmbeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type voyageResponse struct {
	Data []voyageEmbeddingData `json:"data"`
}

func (c *voyageClient) doEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody, err := json.Marshal(voyageRequest{
		Input:           texts,
		Model:           c.model,
		InputType:       "document",
		OutputDimension: c.dim,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal voyage request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("embed: build voyage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network-level failures (timeouts, connection resets) are
		// transient: retry them.
		return nil, retry.RetryableError(fmt.Errorf("embed: voyage request failed: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, retry.RetryableError(fmt.Errorf("embed: read voyage response: %w", err))
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, retry.RetryableError(fmt.Errorf("embed: voyage returned %d: %s", resp.StatusCode, bytes.TrimSpace(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: voyage returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var parsed voyageResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("embed: decode voyage response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embed: voyage returned %d embeddings for %d inputs", len(parsed.Data), len(texts))
	}

	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embed: voyage returned out-of-range index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
