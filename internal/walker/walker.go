package walker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// File pairs a discovered model.File (without an ID or RepoID — those are
// assigned by internal/store on insert) with its raw byte content, so
// callers can hand the bytes straight to internal/parse without a second
// disk read.
type File struct {
	model.File
	Content []byte
}

// dirSkipList holds directory names that are never indexable source and are
// pruned unconditionally, regardless of .gitignore contents: VCS metadata,
// virtualenvs, caches, packaging/build output, and editor state.
var dirSkipList = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".tox":          true,
	".nox":          true,
	".venv":         true,
	"venv":          true,
	"env":           true,
	".eggs":         true,
	"node_modules":  true,
	"dist":          true,
	"build":         true,
	".idea":         true,
	".vscode":       true,
	"testdata":      true,
	"fixtures":      true,
	"test-fixtures": true,
	"__snapshots__": true,
}

func isSkippedDir(name string) bool {
	if dirSkipList[name] {
		return true
	}
	return strings.HasSuffix(name, ".egg-info")
}

// Walk discovers every Python source file under root, in repo-relative,
// forward-slash path order. It respects .gitignore files found anywhere
// under root (nested .gitignore files apply to their own subtree, matching
// git's own scoping rules; a match against any applicable .gitignore is
// enough to exclude a path — negation patterns in a deeper .gitignore that
// re-include something a shallower one ignored are not honoured), and it
// unconditionally skips the directories in dirSkipList (vendored,
// generated, cache, and test-fixture directories) before ever consulting
// .gitignore. Only files named *.py are returned; every other file is
// skipped.
//
// ctx is checked for cancellation between directories so a very large
// repository can be walked cooperatively; it is not passed further (file
// I/O here is local disk access, not something worth threading contexts
// through).
func Walk(ctx context.Context, root string) ([]File, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("walker: resolve root %q: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("walker: stat root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("walker: root %q is not a directory", absRoot)
	}

	gi := newGitignoreCache(absRoot)
	var out []File

	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == absRoot {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		name := d.Name()
		if d.IsDir() {
			if isSkippedDir(name) {
				return filepath.SkipDir
			}
			if gi.ignored(path, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(name, ".py") {
			return nil
		}
		if gi.ignored(path, false) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("walker: read %s: %w", path, err)
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return fmt.Errorf("walker: relativize %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)

		sum := sha256.Sum256(content)

		out = append(out, File{
			File: model.File{
				Path:        rel,
				Language:    "python",
				ContentHash: hex.EncodeToString(sum[:]),
				ModuleName:  moduleName(absRoot, path),
			},
			Content: content,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// moduleName derives the dotted Python module path for the file at path
// (an absolute path under root), relative to the nearest package root: the
// outer boundary of the unbroken chain of ancestor directories — starting
// at the file's own directory and walking upward — that each contain an
// __init__.py. A file named __init__.py contributes its containing
// directory's name to the path, not "__init__" itself. Returns "" if
// path is not under root.
func moduleName(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".py")

	var parts []string
	if name != "__init__" {
		parts = append(parts, name)
	}

	for dir != root {
		if _, err := os.Stat(filepath.Join(dir, "__init__.py")); err != nil {
			break
		}
		parts = append([]string{filepath.Base(dir)}, parts...)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return strings.Join(parts, ".")
}

// gitignoreCache loads and caches compiled .gitignore matchers by the
// directory they live in, so a repository with many directories doesn't
// re-read and re-compile the same .gitignore file for every path checked
// beneath it.
type gitignoreCache struct {
	root  string
	cache map[string]*gitignore.GitIgnore
}

func newGitignoreCache(root string) *gitignoreCache {
	return &gitignoreCache{root: root, cache: map[string]*gitignore.GitIgnore{}}
}

func (c *gitignoreCache) matcherFor(dir string) *gitignore.GitIgnore {
	if m, ok := c.cache[dir]; ok {
		return m
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	var m *gitignore.GitIgnore
	if err == nil {
		m = gitignore.CompileIgnoreLines(strings.Split(string(data), "\n")...)
	}
	c.cache[dir] = m
	return m
}

// ignored reports whether path (absolute, under c.root) is excluded by any
// .gitignore found in path's own directory or an ancestor of it, checked
// from c.root down to path's immediate parent.
func (c *gitignoreCache) ignored(path string, isDir bool) bool {
	dir := filepath.Dir(path)

	var ancestors []string
	for d := dir; ; {
		ancestors = append(ancestors, d)
		if d == c.root {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}

	for _, a := range ancestors {
		m := c.matcherFor(a)
		if m == nil {
			continue
		}
		rel, err := filepath.Rel(a, path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if isDir {
			rel += "/"
		}
		if m.MatchesPath(rel) {
			return true
		}
	}
	return false
}
