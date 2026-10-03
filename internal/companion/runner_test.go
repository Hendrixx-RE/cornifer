package companion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
)

type unusedEngine struct{ corestore.Store }

func TestCredentialRetryFetchesPinnedCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test Git stub uses a POSIX shell")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "git-args")
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CORNIFER_TEST_GIT_LOG\"\ncase \"$*\" in *rev-parse*) printf '%s\\n' '" + sha + "';; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CORNIFER_TEST_GIT_LOG", logPath)
	runner := LocalRunner{Engine: unusedEngine{}, Config: RuntimeConfig{DataDir: dir}}
	repo := Repository{ID: "credential-retry-test", CanonicalURL: "https://github.com/cornifer-test/example.git", RequestedRef: "main", ResolvedCommitSHA: sha}
	got, err := runner.Run(context.Background(), repo, func(Progress) {})
	if !errors.Is(err, ErrCredentialRequired) || got.ResolvedCommitSHA != sha {
		t.Fatalf("Run=%+v, %v", got, err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "fetch --depth 1 origin "+sha) || strings.Contains(string(args), "fetch --depth 1 origin main") {
		t.Fatalf("retry did not fetch pinned commit: %s", args)
	}
}
