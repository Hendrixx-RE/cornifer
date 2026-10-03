package companion

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// This test is opt-in because the normal unit suite must not require a
// database. It exercises persistence across two pools, event bounds and the
// snapshot-staleness guard against a migration-11 database.
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
	repo.ResolvedCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := b.UpdateRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	got, err := b.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stale {
		t.Fatal("old commit session was not marked stale")
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
