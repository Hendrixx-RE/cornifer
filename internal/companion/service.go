package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	DefaultSessionTTL    = 7 * 24 * time.Hour
	DefaultSessionEvents = 64
	DefaultSessionCount  = 100
	DefaultContextTTL    = 10 * time.Minute
	DefaultContextCache  = 256
	DefaultEvidenceLimit = 8
	DefaultContextBytes  = 18_000
)

var ErrCredentialRequired = errors.New("companion: hosted embedding credentials are required before indexing")

// Runner is deliberately small so lifecycle tests can use a deterministic
// fake. The real companion runner may clone/index, but the registry never
// executes untrusted repository code itself.
type Runner interface {
	Run(context.Context, Repository, func(Progress)) (Repository, error)
}

type ContextBuilder interface {
	Build(context.Context, Repository, string, ContextOptions) (ContextPack, error)
}

type ContextOptions struct{ EvidenceLimit, ContextBytes, GraphDepth int }

type Service struct {
	store   Store
	runner  Runner
	context ContextBuilder
	now     func() time.Time

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewService(st Store, runner Runner, contextBuilder ContextBuilder) *Service {
	return &Service{store: st, runner: runner, context: contextBuilder, now: time.Now, cancels: map[string]context.CancelFunc{}}
}

func CanonicalGitHubURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = "https://github.com/" + strings.TrimPrefix(raw, "git@github.com:")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid repository URL: %w", err)
	}
	if u.Scheme != "https" || (u.Host != "github.com" && u.Host != "www.github.com") {
		return "", errors.New("only public GitHub HTTPS URLs are supported")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("repository URL must not include credentials, query, or fragment")
	}
	p := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || path.Clean(p) != p {
		return "", errors.New("repository URL must be github.com/<owner>/<repository>")
	}
	return "https://github.com/" + p + ".git", nil
}

func (s *Service) Ingest(ctx context.Context, rawURL, ref string) (Repository, Job, bool, error) {
	canonical, err := CanonicalGitHubURL(rawURL)
	if err != nil {
		return Repository{}, Job{}, false, err
	}
	ref = strings.TrimSpace(ref)
	repo, reused, err := s.store.UpsertRepository(ctx, Repository{CanonicalURL: canonical, RequestedRef: ref, Status: StatusQueued, IndexVersion: IndexVersion, Capabilities: []string{"python_structural_graph"}})
	if err != nil {
		return Repository{}, Job{}, false, err
	}
	if reused && (repo.Status == StatusReady || repo.Status == StatusQueued || repo.Status == StatusCloning || repo.Status == StatusResolving || repo.Status == StatusIndexing || repo.Status == StatusAwaitingCredential) {
		// A deduplicated request still receives the latest job if one exists.
		job, e := s.latestJob(ctx, repo.ID)
		return repo, job, true, e
	}
	job := Job{ID: newID(), RepositoryID: repo.ID, Phase: string(StatusQueued), Cancellable: true}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return Repository{}, Job{}, false, err
	}
	if s.runner != nil {
		s.start(job, repo)
	}
	return repo, job, false, nil
}

func (s *Service) latestJob(ctx context.Context, repoID string) (Job, error) {
	return s.store.LatestJob(ctx, repoID)
}

func (s *Service) start(job Job, repo Repository) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); delete(s.cancels, job.ID); s.mu.Unlock(); cancel() }()
		update := func(p Progress) {
			job.Phase, job.FilesSeen, job.FilesIndexed, job.Chunks, job.Edges, job.Cancellable = p.Phase, p.FilesSeen, p.FilesIndexed, p.Chunks, p.Edges, p.Cancellable
			_ = s.store.UpdateJob(context.Background(), job)
		}
		result, err := s.runner.Run(ctx, repo, update)
		if errors.Is(ctx.Err(), context.Canceled) {
			result.Status = StatusCancelled
			result.ErrorCode = "cancelled"
			result.SafeMessage = "Indexing cancelled before the next safe boundary."
			job.Phase = string(StatusCancelled)
			job.Cancellable = false
		} else if errors.Is(err, ErrCredentialRequired) {
			result.Status = StatusAwaitingCredential
			result.ErrorCode = "embedding_credentials_required"
			result.SafeMessage = "Hosted embedding credentials are required before indexing."
			job.Phase = string(StatusAwaitingCredential)
			job.Cancellable = false
		} else if err != nil {
			result.Status = StatusFailed
			result.ErrorCode = "index_failed"
			result.SafeMessage = "Indexing failed without executing repository code."
			job.Phase = string(StatusFailed)
			job.ErrorCode = result.ErrorCode
			job.SafeMessage = result.SafeMessage
			job.Cancellable = false
		} else {
			result.Status = StatusReady
			result.ErrorCode = ""
			result.SafeMessage = "Indexed snapshot is ready."
			job.Phase = string(StatusReady)
			job.Cancellable = false
		}
		_ = s.store.UpdateRepository(context.Background(), result)
		_ = s.store.UpdateJob(context.Background(), job)
	}()
}

func (s *Service) GetRepository(ctx context.Context, id string) (Repository, error) {
	return s.store.GetRepository(ctx, id)
}
func (s *Service) ListRepositories(ctx context.Context) ([]Repository, error) {
	return s.store.ListRepositories(ctx)
}
func (s *Service) GetJob(ctx context.Context, id string) (Job, error) { return s.store.GetJob(ctx, id) }
func (s *Service) Cancel(ctx context.Context, id string) error {
	if err := s.store.RequestCancel(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Service) BuildContext(ctx context.Context, repoID, question, sessionID string, opts ContextOptions) (ContextPack, Session, error) {
	repo, err := s.store.GetRepository(ctx, repoID)
	if err != nil {
		return ContextPack{}, Session{}, err
	}
	if !repo.Status.Ready() {
		return ContextPack{}, Session{}, fmt.Errorf("repository is %s: %s", repo.Status, repo.SafeMessage)
	}
	if opts.EvidenceLimit <= 0 {
		opts.EvidenceLimit = DefaultEvidenceLimit
	}
	if opts.ContextBytes <= 0 {
		opts.ContextBytes = DefaultContextBytes
	}
	key := contextKey(repo, question, opts)
	if pack, ok, err := s.store.GetContextCache(ctx, key, s.now()); err != nil {
		return ContextPack{}, Session{}, err
	} else if ok {
		return s.recordContext(ctx, repo, sessionID, question, pack)
	}
	if s.context == nil {
		return ContextPack{}, Session{}, errors.New("context retrieval is not configured")
	}
	pack, err := s.context.Build(ctx, repo, question, opts)
	if err != nil {
		return ContextPack{}, Session{}, err
	}
	pack.Retrieval.CacheKey = key
	if err := s.store.PutContextCache(ctx, key, repo, pack, s.now().Add(DefaultContextTTL), DefaultContextCache); err != nil {
		return ContextPack{}, Session{}, err
	}
	return s.recordContext(ctx, repo, sessionID, question, pack)
}

func (s *Service) recordContext(ctx context.Context, repo Repository, sessionID, question string, pack ContextPack) (ContextPack, Session, error) {
	sess, err := s.ensureSession(ctx, repo, sessionID)
	if err != nil {
		return ContextPack{}, Session{}, err
	}
	if err := s.store.PutSessionEvent(ctx, SessionEvent{SessionID: sess.ID, Kind: "context", Payload: map[string]any{"question": question, "context": pack}}, DefaultSessionEvents); err != nil {
		return ContextPack{}, Session{}, err
	}
	return pack, sess, nil
}
func (s *Service) ensureSession(ctx context.Context, repo Repository, id string) (Session, error) {
	if id != "" {
		v, err := s.store.GetSession(ctx, id)
		if err != nil {
			return v, err
		}
		if v.RepositoryID != repo.ID || v.CommitSHA != repo.ResolvedCommitSHA {
			return v, errors.New("session belongs to a different repository snapshot")
		}
		if v.Stale {
			return v, errors.New("session is stale and cannot be applied to the current repository snapshot")
		}
		if v.ExpiresAt.Before(s.now()) {
			return v, errors.New("session expired")
		}
		return v, nil
	}
	v := Session{ID: newID(), RepositoryID: repo.ID, CommitSHA: repo.ResolvedCommitSHA, ExpiresAt: s.now().Add(DefaultSessionTTL)}
	if err := s.store.CreateSession(ctx, v); err != nil {
		return v, err
	}
	return v, nil
}
func (s *Service) Remember(ctx context.Context, id, note string) (Session, error) {
	v, err := s.store.GetSession(ctx, id)
	if err != nil {
		return v, err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return v, errors.New("note is required")
	}
	if len(note) > 2000 {
		note = note[:2000]
	}
	if err := s.store.PutSessionEvent(ctx, SessionEvent{SessionID: id, Kind: "note", Payload: map[string]string{"note": note}}, DefaultSessionEvents); err != nil {
		return v, err
	}
	v.UpdatedAt = s.now()
	return v, s.store.UpdateSession(ctx, v)
}
func (s *Service) GetSession(ctx context.Context, id string, limit int) (Session, []SessionEvent, error) {
	v, err := s.store.GetSession(ctx, id)
	if err != nil {
		return v, nil, err
	}
	if limit <= 0 || limit > DefaultSessionEvents {
		limit = DefaultSessionEvents
	}
	events, err := s.store.ListSessionEvents(ctx, id, limit)
	return v, events, err
}
func (s *Service) ClearSession(ctx context.Context, id string) error {
	return s.store.ClearSession(ctx, id)
}
func contextKey(r Repository, q string, o ContextOptions) string {
	h := sha256.Sum256([]byte(strings.Join([]string{r.ID, r.ResolvedCommitSHA, r.IndexVersion, r.ProviderFingerprint, strings.TrimSpace(q), fmt.Sprint(o.EvidenceLimit), fmt.Sprint(o.ContextBytes), fmt.Sprint(o.GraphDepth)}, "\x00")))
	return hex.EncodeToString(h[:])
}
