package companion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ChatConfig is intentionally independent from RuntimeConfig: embeddings and
// answer generation may use distinct hosted providers/models and credentials.
type ChatConfig struct {
	Provider, BaseURL, Model, APIKey string
	HTTPClient                       *http.Client
}

func ChatConfigFromEnv() ChatConfig {
	return ChatConfig{Provider: strings.TrimSpace(os.Getenv("CORNIFER_CHAT_PROVIDER")), BaseURL: strings.TrimSpace(os.Getenv("CORNIFER_CHAT_BASE_URL")), Model: strings.TrimSpace(os.Getenv("CORNIFER_CHAT_MODEL")), APIKey: os.Getenv("CORNIFER_CHAT_API_KEY")}
}
func (c ChatConfig) Configured() bool {
	return c.Provider != "" && c.BaseURL != "" && c.Model != "" && c.APIKey != ""
}

type Answer struct {
	Status      string      `json:"status"`
	Text        string      `json:"text"`
	CitationIDs []string    `json:"citation_ids"`
	Context     ContextPack `json:"context"`
}

// GenerateAnswer is a narrow OpenAI-compatible adapter. It carries only a
// bounded evidence pack and rejects citations not supplied by retrieval.
func GenerateAnswer(ctx context.Context, cfg ChatConfig, pack ContextPack) (Answer, error) {
	if !cfg.Configured() {
		return Answer{Status: "credentials_required", Context: pack}, errors.New("hosted chat configuration is required to generate an answer")
	}
	if cfg.Provider != "openai_compatible" && cfg.Provider != "openai" {
		return Answer{}, fmt.Errorf("companion: chat provider %q is not supported; configure openai_compatible", cfg.Provider)
	}
	evidence, err := json.Marshal(pack)
	if err != nil {
		return Answer{}, err
	}
	prompt := "Answer only from the supplied Cornifer evidence pack. State insufficient evidence when necessary. Cite every factual claim with [eN], and return JSON exactly: {\\\"answer\\\":string,\\\"citations\\\":[string]}.\n\n" + string(evidence)
	body, _ := json.Marshal(map[string]any{"model": cfg.Model, "temperature": 0, "response_format": map[string]string{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": "You are a source-grounded repository assistant."}, {"role": "user", "content": prompt}}})
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		return Answer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return Answer{}, fmt.Errorf("hosted chat request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Answer{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Answer{}, fmt.Errorf("hosted chat returned %d", resp.StatusCode)
	}
	var wire struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Choices) == 0 {
		return Answer{}, errors.New("hosted chat response did not contain a choice")
	}
	var generated struct {
		Answer    string   `json:"answer"`
		Citations []string `json:"citations"`
	}
	if err := json.Unmarshal([]byte(wire.Choices[0].Message.Content), &generated); err != nil {
		return Answer{}, errors.New("hosted chat response was not the requested JSON object")
	}
	allowed := map[string]bool{}
	for _, c := range pack.Evidence {
		allowed[c.ID] = true
	}
	for _, id := range generated.Citations {
		if !allowed[id] {
			return Answer{}, fmt.Errorf("hosted chat cited unknown evidence %q", id)
		}
	}
	for _, id := range citationTokens.FindAllString(generated.Answer, -1) {
		if !allowed[strings.Trim(id, "[]")] {
			return Answer{}, fmt.Errorf("hosted chat text cited unknown evidence %q", id)
		}
	}
	if strings.TrimSpace(generated.Answer) == "" {
		return Answer{}, errors.New("hosted chat returned an empty answer")
	}
	return Answer{Status: "ok", Text: generated.Answer, CitationIDs: generated.Citations, Context: pack}, nil
}

var citationTokens = regexp.MustCompile(`\[e[0-9]+\]`)
