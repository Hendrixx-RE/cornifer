package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
)

// RuntimeConfig intentionally contains configuration identity but never
// serializes API keys. The companion process, rather than the browser, reads
// these values from its environment.
type RuntimeConfig struct {
	DataDir            string
	EmbeddingProvider  string
	EmbeddingModel     string
	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingDimension int
}

func RuntimeConfigFromEnv() RuntimeConfig {
	dataDir := os.Getenv("CORNIFER_COMPANION_DIR")
	if dataDir == "" {
		dataDir = ".cornifer-companion"
	}
	key := os.Getenv("CORNIFER_EMBEDDING_API_KEY")
	if key == "" {
		key = os.Getenv(embed.VoyageAPIKeyEnvVar)
	}
	dim, _ := strconv.Atoi(os.Getenv("CORNIFER_EMBEDDING_DIM"))
	return RuntimeConfig{
		DataDir:            dataDir,
		EmbeddingProvider:  strings.ToLower(strings.TrimSpace(os.Getenv("CORNIFER_EMBEDDING_PROVIDER"))),
		EmbeddingModel:     strings.TrimSpace(os.Getenv("CORNIFER_EMBEDDING_MODEL")),
		EmbeddingBaseURL:   strings.TrimSpace(os.Getenv("CORNIFER_EMBEDDING_BASE_URL")),
		EmbeddingAPIKey:    key,
		EmbeddingDimension: dim,
	}
}

func (c RuntimeConfig) configuredEmbedding() bool {
	return c.EmbeddingProvider != "" && c.EmbeddingAPIKey != ""
}

func (c RuntimeConfig) embedConfig(query bool, cacheDir string) (embed.Config, error) {
	if !c.configuredEmbedding() {
		return embed.Config{}, ErrCredentialRequired
	}
	switch embed.Provider(c.EmbeddingProvider) {
	case embed.ProviderVoyage:
		inputType := "document"
		if query {
			inputType = "query"
		}
		return embed.Config{Provider: embed.ProviderVoyage, Dimension: c.EmbeddingDimension, CacheDir: cacheDir,
			Voyage: embed.VoyageConfig{APIKey: c.EmbeddingAPIKey, Model: c.EmbeddingModel, BaseURL: c.EmbeddingBaseURL, InputType: inputType}}, nil
	default:
		return embed.Config{}, fmt.Errorf("companion: embedding provider %q is not supported; configure voyage", c.EmbeddingProvider)
	}
}

func (c RuntimeConfig) ProviderFingerprint() string {
	model := c.EmbeddingModel
	if model == "" && c.EmbeddingProvider == string(embed.ProviderVoyage) {
		model = embed.DefaultVoyageModel
	}
	v := strings.Join([]string{c.EmbeddingProvider, model, c.EmbeddingBaseURL, fmt.Sprint(c.EmbeddingDimension), IndexVersion}, "\x00")
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// LocalRunner clones a public GitHub repository without checking out or
// invoking anything from it. It then indexes the detached pinned commit.
// Git hooks are disabled and neither shell expansion nor repository config is
// used in command construction.
type LocalRunner struct {
	Engine corestore.Store
	Config RuntimeConfig
}

var safeRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@-]*$`)

func (r LocalRunner) Run(ctx context.Context, repo Repository, progress func(Progress)) (Repository, error) {
	if r.Engine == nil {
		return repo, errors.New("companion: engine store is not configured")
	}
	if r.Config.DataDir == "" {
		r.Config.DataDir = ".cornifer-companion"
	}
	if err := os.MkdirAll(filepath.Join(r.Config.DataDir, "repos"), 0o700); err != nil {
		return repo, fmt.Errorf("create companion data directory: %w", err)
	}
	checkout := filepath.Join(r.Config.DataDir, "repos", repo.ID)
	cacheDir := filepath.Join(r.Config.DataDir, "cache", repo.ID)
	repo.CheckoutPath, repo.CacheDir = checkout, cacheDir
	repo.ProviderFingerprint = r.Config.ProviderFingerprint()

	progress(Progress{Phase: string(StatusCloning), Cancellable: true})
	if err := os.RemoveAll(checkout); err != nil {
		return repo, fmt.Errorf("reset incomplete checkout: %w", err)
	}
	if err := r.git(ctx, "clone", "--no-checkout", "--depth", "1", "--no-tags", repo.CanonicalURL, checkout); err != nil {
		return repo, fmt.Errorf("clone public repository: %w", err)
	}
	progress(Progress{Phase: string(StatusResolving), Cancellable: true})
	ref := repo.RequestedRef
	// Credential retry keeps the commit already resolved by the original job.
	if repo.ResolvedCommitSHA != "" {
		ref = repo.ResolvedCommitSHA
	}
	if ref == "" {
		ref = "HEAD"
	}
	if !safeRef.MatchString(ref) {
		return repo, errors.New("repository ref contains unsupported characters")
	}
	if err := r.gitIn(ctx, checkout, "fetch", "--depth", "1", "origin", ref); err != nil {
		return repo, fmt.Errorf("resolve requested ref: %w", err)
	}
	sha, err := r.gitOutput(ctx, checkout, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return repo, fmt.Errorf("resolve pinned commit: %w", err)
	}
	repo.ResolvedCommitSHA = strings.TrimSpace(sha)
	if err := r.gitIn(ctx, checkout, "checkout", "--detach", "--no-recurse-submodules", repo.ResolvedCommitSHA); err != nil {
		return repo, fmt.Errorf("checkout pinned commit: %w", err)
	}
	if !r.Config.configuredEmbedding() {
		return repo, ErrCredentialRequired
	}
	embedCfg, err := r.Config.embedConfig(false, filepath.Join(r.Config.DataDir, "embeddings"))
	if err != nil {
		return repo, err
	}
	progress(Progress{Phase: string(StatusIndexing), Cancellable: true})
	stats, err := indexer.Index(ctx, r.Engine, indexer.Config{RepoRoot: checkout, CacheDir: cacheDir, Embedder: embedCfg, IncludeText: true,
		Progress: func(p indexer.Progress) {
			progress(Progress{Phase: p.Phase, FilesSeen: p.Files, Chunks: p.Chunks, Edges: p.Edges, Cancellable: true})
		},
	})
	if err != nil {
		return repo, err
	}
	repo.EngineRepoID = stats.RepoID
	progress(Progress{Phase: string(StatusIndexing), FilesSeen: stats.Files, FilesIndexed: stats.Files, Chunks: stats.Chunks, Edges: stats.Edges, Cancellable: false})
	return repo, nil
}

func (r LocalRunner) git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=never"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}
func (r LocalRunner) gitIn(ctx context.Context, dir string, args ...string) error {
	return r.git(ctx, append([]string{"-C", dir}, args...)...)
}
func (r LocalRunner) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=never", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	b, err := cmd.Output()
	return string(b), err
}
