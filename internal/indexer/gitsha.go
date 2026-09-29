package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
)

// resolveCommitSHA returns root's checked-out git commit SHA via `git
// rev-parse HEAD`. If root is not a git checkout (e.g. a synthetic fixture
// directory in a test), it falls back to a deterministic, clearly-labeled
// pseudo-SHA so indexing still works outside a git repo; model.Repo's
// CommitSHA doc comment calls for a real 40-character commit SHA, and this
// fallback is exactly that shape but is not a git SHA, so eval and PR
// output should treat non-git roots as informational, not reproducible
// against a public commit history.
// ResolveCommitSHA is the exported form of resolveCommitSHA, for callers
// (cmd/cornifer's reindex command) that need to compute a repo's commit SHA
// the same way Index does before deciding whether to run the pipeline.
func ResolveCommitSHA(root string) (string, error) {
	return resolveCommitSHA(root)
}

func resolveCommitSHA(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err == nil {
		sha := strings.TrimSpace(string(out))
		if sha != "" {
			return sha, nil
		}
	}

	// Not a git repo (or git is unavailable): derive a stable 40-hex-char
	// placeholder from the root path so repeated runs against the same,
	// unchanged root produce the same "commit", matching how a real SHA
	// would behave for `make up`/reindex idempotency in tests.
	sum := sha256.Sum256([]byte(root))
	pseudo := hex.EncodeToString(sum[:])[:40]
	return pseudo, nil
}
