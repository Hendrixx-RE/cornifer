package companion

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedRetryRunner struct {
	started chan Repository
	release chan struct{}
	runs    atomic.Int32
}

func (r *blockedRetryRunner) Run(ctx context.Context, repo Repository, _ func(Progress)) (Repository, error) {
	r.runs.Add(1)
	r.started <- repo
	select {
	case <-r.release:
		return repo, ErrCredentialRequired
	case <-ctx.Done():
		return repo, ctx.Err()
	}
}

// Simulate a credential-blocked job persisted by an earlier server, then an
// explicit resubmission through a new pool/service. No Git or provider calls.
func TestPostgresCredentialRetryAfterRestart(t *testing.T) {
	dsn := os.Getenv("CORNIFER_COMPANION_TEST_DSN")
	if dsn == "" {
		t.Skip("set CORNIFER_COMPANION_TEST_DSN to run companion database integration")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	repo, _, err := store.UpsertRepository(ctx, Repository{CanonicalURL: "https://github.com/cornifer-test/" + newID() + ".git", RequestedRef: "main", Status: StatusAwaitingCredential})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.pool.Exec(ctx, "DELETE FROM companion_repositories WHERE id=$1", repo.ID) })
	repo.ResolvedCommitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repo.ErrorCode, repo.SafeMessage = "embedding_credentials_required", "Hosted embedding credentials are required before indexing."
	if err := store.UpdateRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	oldJob := Job{ID: newID(), RepositoryID: repo.ID, Phase: string(StatusAwaitingCredential)}
	if err := store.CreateJob(ctx, oldJob); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	runner := &blockedRetryRunner{started: make(chan Repository, 1), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	defer release()
	service := NewService(restarted, runner, nil)
	queued, job, reused, err := service.Ingest(ctx, repo.CanonicalURL, repo.RequestedRef)
	if err != nil || reused || queued.ID != repo.ID || queued.Status != StatusQueued || job.ID == oldJob.ID || job.Phase != string(StatusQueued) {
		t.Fatalf("retry = repo=%+v job=%+v reused=%v err=%v", queued, job, reused, err)
	}
	select {
	case got := <-runner.started:
		if got.ResolvedCommitSHA != repo.ResolvedCommitSHA || got.ErrorCode != "" || got.SafeMessage != "" {
			t.Fatalf("retry lost pin or retained error: %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("explicit credential retry did not start runner")
	}
	_, duplicate, reused, err := service.Ingest(ctx, repo.CanonicalURL, repo.RequestedRef)
	if err != nil || !reused || duplicate.ID != job.ID || runner.runs.Load() != 1 {
		t.Fatalf("active retry duplicate=%+v reused=%v runs=%d err=%v", duplicate, reused, runner.runs.Load(), err)
	}
	release()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.GetRepository(ctx, repo.ID)
		if err == nil && got.Status == StatusAwaitingCredential {
			terminal, err := store.GetJob(ctx, job.ID)
			if err == nil && terminal.Phase == string(StatusAwaitingCredential) && !terminal.Cancellable {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retry without credentials did not return to awaiting_credentials")
}

// This test is opt-in because the normal unit suite must not require a
// database. It exercises persistence across two pools, event bounds and the
// immutable snapshot isolation against a migration-12 database.
func TestPostgresSessionSnapshotIsolation(t *testing.T) {
	dsn := os.Getenv("CORNIFER_COMPANION_TEST_DSN")
	if dsn == "" {
		t.Skip("set CORNIFER_COMPANION_TEST_DSN to run companion database integration")
	}
	ctx := context.Background()
	a, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	repo, reused, err := a.UpsertRepository(ctx, Repository{CanonicalURL: "https://github.com/cornifer-test/" + newID() + ".git", Status: StatusReady, IndexVersion: IndexVersion})
	if err != nil || reused {
		t.Fatalf("UpsertRepository = %#v reused=%v err=%v", repo, reused, err)
	}
	repo.ResolvedCommitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := a.UpdateRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	session := Session{ID: newID(), RepositoryID: repo.ID, CommitSHA: repo.ResolvedCommitSHA, ExpiresAt: time.Now().Add(time.Hour)}
	if err := a.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := a.PutSessionEvent(ctx, SessionEvent{SessionID: session.ID, Kind: "note", Payload: map[string]int{"n": i}}, 2); err != nil {
			t.Fatal(err)
		}
	}
	if events, err := a.ListSessionEvents(ctx, session.ID, 10); err != nil || len(events) != 2 {
		t.Fatalf("event bound = %d, %v; want 2", len(events), err)
	}
	// A second pool simulates a process restart rather than only in-memory state.
	b, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// A second snapshot for the same URL/ref must not mutate the first row.
	second, reused, err := b.UpsertRepository(ctx, Repository{CanonicalURL: repo.CanonicalURL, RequestedRef: repo.RequestedRef, Status: StatusQueued, IndexVersion: IndexVersion})
	if err != nil || reused || second.ID == repo.ID {
		t.Fatalf("second snapshot = %+v reused=%v err=%v", second, reused, err)
	}
	second.ResolvedCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second.Status = StatusReady
	if err := b.UpdateRepository(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := b.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stale || got.RepositoryID != repo.ID || got.CommitSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("old snapshot session was corrupted: %+v", got)
	}
	if err := b.ClearSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetSession(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleared session error=%v, want ErrNotFound", err)
	}
}

func TestCanonicalGitHubURL(t *testing.T) {
	got, err := CanonicalGitHubURL("git@github.com:owner/repo.git")
	if err != nil || got != "https://github.com/owner/repo.git" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, raw := range []string{"http://github.com/a/b", "https://github.com/a/b?token=x", "https://example.com/a/b"} {
		if _, err := CanonicalGitHubURL(raw); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
}
