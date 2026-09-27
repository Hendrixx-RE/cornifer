package model

// File is one source file inside an indexed Repo.
type File struct {
	ID     int64
	RepoID int64

	// Path is repo-relative and uses forward slashes regardless of host OS,
	// e.g. "fastapi/routing.py". It is unique within a Repo.
	Path string

	// Language is the lowercase language identifier the walker detected for
	// this file, e.g. "python". Phase 0 defines the field; only Python is
	// in scope for parsing per plan.md.
	Language string

	// ContentHash is the lowercase hex-encoded SHA-256 of the file's raw
	// bytes at index time. Incremental reindex compares this against the
	// previously stored value to decide whether a file needs re-parsing.
	ContentHash string

	// ModuleName is the dotted Python module path derived from Path relative
	// to the nearest package root, e.g. "fastapi.routing". Empty when a
	// module name cannot be derived (e.g. a file outside any package).
	ModuleName string
}
