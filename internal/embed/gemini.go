package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DefaultGeminiModel             = "gemini-embedding-2"
	DefaultGeminiBaseURL           = "https://generativelanguage.googleapis.com/v1beta"
	GeminiAPIKeyEnvVar             = "GEMINI_API_KEY"
	DefaultGeminiConcurrency       = 2
	DefaultGeminiRequestsPerMinute = 30
	// Conservative UTF-8 byte ceilings leave room under documented token
	// limits (8192 for model 2, 2048 for 001). They are not token estimates.
	Gemini2MaxInputBytes   = 7680
	Gemini001MaxInputBytes = 1536
)

// GeminiConfig uses AI Studio API keys, not Vertex/ADC. Model defaults to
// Embedding 2; 001 requires explicit selection. No model or local fallback.
// BaseURL is the API root, not a full model endpoint. InputType is document
// or query (natural-language code retrieval). Title defaults to "none".
// Concurrency is 1..8; RequestsPerMinute is 1..600, per embedder instance.
// Retry timings are optional library/test overrides; waits are cancellable.
type GeminiConfig struct {
	APIKey                           string `json:"-"`
	Model, BaseURL, InputType, Title string
	Concurrency, RequestsPerMinute   int
	HTTPClient                       *http.Client
	RetryBase, RetryMax              time.Duration
}

func resolveGemini(cfg GeminiConfig, dim int) (GeminiConfig, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultGeminiModel
	}
	if cfg.Model != DefaultGeminiModel && cfg.Model != "gemini-embedding-001" {
		return cfg, errors.New("embed: Gemini model must be gemini-embedding-2 or explicitly gemini-embedding-001")
	}
	if dim < 128 || dim > 3072 {
		return cfg, errors.New("embed: Gemini output dimension must be between 128 and 3072")
	}
	if cfg.InputType == "" {
		cfg.InputType = "document"
	}
	if cfg.InputType != "document" && cfg.InputType != "query" {
		return cfg, errors.New("embed: Gemini input type must be document or query")
	}
	if cfg.Title == "" {
		cfg.Title = "none"
	}
	if !utf8.ValidString(cfg.Title) {
		return cfg, errors.New("embed: Gemini title must be valid UTF-8")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultGeminiBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return cfg, errors.New("embed: Gemini base URL must be an HTTPS API root (HTTP allowed only for loopback tests), without credentials/query/fragment")
	}
	if strings.Contains(u.Path, ":embedContent") || strings.Contains(u.Path, "/files") {
		return cfg, errors.New("embed: Gemini base URL is an API root, not a model or Files endpoint")
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = DefaultGeminiConcurrency
	}
	if cfg.RequestsPerMinute == 0 {
		cfg.RequestsPerMinute = DefaultGeminiRequestsPerMinute
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 8 || cfg.RequestsPerMinute < 1 || cfg.RequestsPerMinute > 600 {
		return cfg, errors.New("embed: Gemini concurrency must be 1..8 and requests per minute 1..600")
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = DefaultRetryBase
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = DefaultRetryMax
	}
	return cfg, nil
}

// GeminiIdentity records a document/query-compatible vector space, including
// task/prompt schema, normalization, output shape and endpoint. No keys enter
// provenance/cache paths. Document/query cache entries add their own role.
func GeminiIdentity(cfg GeminiConfig, dim int) (string, error) {
	var err error
	dim, err = ResolveDimension(dim)
	if err != nil {
		return "", err
	}
	cfg, err = resolveGemini(cfg, dim)
	if err != nil {
		return "", err
	}
	roles := "document=title-text-v1;query=code-retrieval-prefix-v1;normalization=server-v1"
	if cfg.Model == "gemini-embedding-001" {
		roles = "document=RETRIEVAL_DOCUMENT;query=CODE_RETRIEVAL_QUERY;normalization=l2-v1"
	}
	endpoint := sha256.Sum256([]byte(cfg.BaseURL))
	title := sha256.Sum256([]byte(cfg.Title))
	return fmt.Sprintf("%s;dimensions=%d;%s;endpoint=%s;title=%s", cfg.Model, dim, roles, hex.EncodeToString(endpoint[:]), hex.EncodeToString(title[:])), nil
}

// ValidateQuerySpace prevents equal-length vectors from different models or
// prompt spaces being compared. Unknown provenance is rejected for Gemini.
func ValidateQuerySpace(provider, identity string, cfg Config) error {
	if provider != string(cfg.Provider) {
		return errors.New("embed: configured provider differs from indexed snapshot; use matching embeddings or disable them for BM25 evidence")
	}
	if cfg.Provider != ProviderGemini {
		return nil
	}
	want, err := GeminiIdentity(cfg.Gemini, cfg.Dimension)
	if err != nil {
		return err
	}
	if identity != want {
		return errors.New("embed: Gemini model/dimension/task format/endpoint differs from indexed snapshot; index a new snapshot or use its original embedding configuration")
	}
	return nil
}

type geminiEmbedder struct {
	cfg                       GeminiConfig
	dim, maxRetries           int
	inputType                 string
	client                    *http.Client
	mu                        sync.Mutex
	nextRequest, blockedUntil time.Time
	// Shared across concurrent Embed calls on this instance.
	slots chan struct{}
}

func newGeminiEmbedder(cfg GeminiConfig, dim, maxRetries int) (*geminiEmbedder, error) {
	if maxRetries < 0 || maxRetries > 10 {
		return nil, errors.New("embed: Gemini retry count must be between 0 and 10")
	}
	var err error
	cfg, err = resolveGemini(cfg, dim)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("CORNIFER_EMBEDDING_API_KEY")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv(GeminiAPIKeyEnvVar)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: set %s or CORNIFER_EMBEDDING_API_KEY", ErrMissingAPIKey, GeminiAPIKeyEnvVar)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	copyClient := *client
	// Redirects must never forward the API-key header to another endpoint.
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &geminiEmbedder{cfg: cfg, dim: dim, maxRetries: maxRetries, inputType: cfg.InputType, client: &copyClient, slots: make(chan struct{}, cfg.Concurrency)}, nil
}

type geminiRequest struct {
	Model   string `json:"model"`
	Content struct {
		Parts []geminiPart `json:"parts"`
	} `json:"content"`
	Config geminiContentConfig `json:"embedContentConfig"`
}
type geminiPart struct {
	Text string `json:"text"`
}
type geminiContentConfig struct {
	OutputDimensionality int    `json:"outputDimensionality"`
	AutoTruncate         bool   `json:"autoTruncate"`
	TaskType             string `json:"taskType,omitempty"`
	Title                string `json:"title,omitempty"`
}

func (g *geminiEmbedder) request(text string) (geminiRequest, error) {
	var request geminiRequest
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return request, errors.New("embed: Gemini input must be nonempty valid UTF-8 text")
	}
	request.Model = "models/" + g.cfg.Model
	request.Config.OutputDimensionality = g.dim
	limit := Gemini2MaxInputBytes
	if g.cfg.Model == DefaultGeminiModel {
		if g.inputType == "query" {
			text = "task: code retrieval | query: " + text
		} else {
			text = "title: " + g.cfg.Title + " | text: " + text
		}
	} else {
		limit = Gemini001MaxInputBytes
		request.Config.TaskType = "RETRIEVAL_DOCUMENT"
		request.Config.Title = g.cfg.Title
		if g.inputType == "query" {
			request.Config.TaskType = "CODE_RETRIEVAL_QUERY"
			request.Config.Title = ""
		}
	}
	if len(text)+len(request.Config.Title) > limit {
		return request, fmt.Errorf("embed: Gemini input exceeds conservative %d-byte guard; reduce chunk/query size (no silent truncation)", limit)
	}
	request.Content.Parts = []geminiPart{{Text: text}}
	return request, nil
}

func (g *geminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(texts) == 0 {
		return nil, nil
	}
	// Validate every input before making any request, so an oversized later
	// chunk cannot trigger earlier billable requests in this call.
	for i, text := range texts {
		if _, err := g.request(text); err != nil {
			return nil, fmt.Errorf("input %d: %w", i, err)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([][]float32, len(texts))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var once sync.Once
	var firstErr error
	for i := 0; i < g.cfg.Concurrency; i++ {
		workers.Go(func() {
			for index := range jobs {
				select {
				case g.slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				vec, err := g.embedOne(ctx, texts[index])
				<-g.slots
				if err != nil {
					once.Do(func() { firstErr = fmt.Errorf("input %d: %w", index, err); cancel() })
					return
				}
				out[index] = vec
			}
		})
	}
sendLoop:
	for index := range texts {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func waitGemini(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (g *geminiEmbedder) waitSlot(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		now := time.Now()
		next := g.nextRequest
		if g.blockedUntil.After(next) {
			next = g.blockedUntil
		}
		delay := next.Sub(now)
		if delay <= 0 {
			g.nextRequest = now.Add(time.Minute / time.Duration(g.cfg.RequestsPerMinute))
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		if err := waitGemini(ctx, delay); err != nil {
			return err
		}
	}
}

type geminiRequestError struct {
	status     int
	transient  bool
	retryAfter time.Duration
}

func (e *geminiRequestError) Error() string {
	switch e.status {
	case 0:
		return "embed: Gemini transport/read failure"
	case 401, 403:
		return fmt.Sprintf("embed: Gemini HTTP %d; check API key and project/model access", e.status)
	case 429:
		return "embed: Gemini HTTP 429; rate/quota limited, retry later or lower request rate"
	default:
		return fmt.Sprintf("embed: Gemini HTTP %d; check provider/model/input/dimension configuration", e.status)
	}
}
func geminiRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
		if seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
func (g *geminiEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	for attempt := 0; ; attempt++ {
		if err := g.waitSlot(ctx); err != nil {
			return nil, err
		}
		vec, err := g.send(ctx, text)
		if err == nil {
			return vec, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var problem *geminiRequestError
		if !errors.As(err, &problem) || !problem.transient || attempt >= g.maxRetries {
			return nil, err
		}
		delay := time.Duration(float64(g.cfg.RetryBase) * math.Pow(2, float64(attempt)) * (.8 + .4*rand.Float64()))
		if delay > g.cfg.RetryMax || delay < 0 {
			delay = g.cfg.RetryMax
		}
		if problem.retryAfter > g.cfg.RetryMax {
			return nil, fmt.Errorf("%w; Retry-After exceeds bounded retry window", err)
		}
		if problem.retryAfter > delay {
			delay = problem.retryAfter
		}
		// Cooldown applies to every worker, including already waiting ones.
		g.mu.Lock()
		until := time.Now().Add(delay)
		if until.After(g.blockedUntil) {
			g.blockedUntil = until
		}
		g.mu.Unlock()
		if err := waitGemini(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func (g *geminiEmbedder) send(ctx context.Context, text string) ([]float32, error) {
	payload, err := g.request(text)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("embed: invalid Gemini request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.BaseURL+"/models/"+g.cfg.Model+":embedContent", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("embed: invalid Gemini endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.cfg.APIKey)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &geminiRequestError{transient: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
		return nil, &geminiRequestError{status: resp.StatusCode, transient: resp.StatusCode == 429 || resp.StatusCode >= 500, retryAfter: geminiRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &geminiRequestError{transient: true}
	}
	var response struct {
		Embedding *struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
		Embeddings []json.RawMessage `json:"embeddings"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, errors.New("embed: malformed Gemini embedding response")
	}
	if response.Embedding == nil || response.Embeddings != nil {
		return nil, errors.New("embed: Gemini must return exactly one embedding per input")
	}
	vec := response.Embedding.Values
	if len(vec) != g.dim {
		return nil, fmt.Errorf("%w: Gemini got %d, want %d", ErrDimensionMismatch, len(vec), g.dim)
	}
	var squared float64
	for _, value := range vec {
		n := float64(value)
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, errors.New("embed: Gemini returned nonfinite vector")
		}
		squared += n * n
	}
	if squared == 0 || math.IsInf(squared, 0) {
		return nil, errors.New("embed: Gemini returned invalid zero/nonfinite vector norm")
	}
	// Embedding 2 normalizes reduced outputs itself. 001 requires us to do so.
	if g.cfg.Model == "gemini-embedding-001" {
		norm := math.Sqrt(squared)
		for i, value := range vec {
			vec[i] = float32(float64(value) / norm)
		}
	}
	return vec, nil
}
