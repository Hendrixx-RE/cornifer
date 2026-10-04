package model

import "time"

// Repo is one indexed snapshot of a git repository: a specific checkout
// pinned to a commit SHA. Re-indexing the same root at a different commit
// creates a new Repo row rather than mutating the old one, so eval results
// and query answers stay attributable to the commit they were computed
// against.
type Repo struct {
	ID int64

	// Root is the absolute filesystem path to the checked-out repository
	// that was walked, e.g. "/home/user/cornifer/repos/fastapi".
	Root string

	// CommitSHA is the full 40-character git commit SHA that was indexed.
	// Callers must resolve short SHAs/branches/tags to a full SHA before
	// constructing a Repo; this field is what pins eval labels to a
	// specific, reproducible state of the target repo.
	CommitSHA string

	// EmbeddingProvider and EmbeddingModel identify the exact vector space
	// used for chunks in this snapshot. They are persisted with the repo so
	// query/eval never has to guess from a process environment or a cache.
	// "unknown" is retained for rows indexed before provenance was added.
	EmbeddingProvider string
	EmbeddingModel    string

	// IndexedAt is when this Repo's indexing run completed.
	IndexedAt time.Time
}
