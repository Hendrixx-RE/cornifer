package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// ChunkMeta is the subset of model.Chunk the Manifest keeps: everything
// query commands need to display a hit (file, line range, header, text,
// owning symbol) without the embedding, which stays in Postgres and is
// searched via Store.VectorSearch.
type ChunkMeta struct {
	ID            int64
	SymbolID      *int64
	FileID        int64
	StartLine     int
	EndLine       int
	Text          string
	ContextHeader string
	TokenCount    int
}

// Manifest is everything a query-side command needs to answer structural
// and semantic queries against one indexed repo, without a bulk-read Store
// method (see doc.go). It is written once per successful Index run and
// read by every read-only CLI command.
type Manifest struct {
	RepoID    int64
	Root      string
	CommitSHA string

	Files   []*model.File
	Symbols []*model.Symbol
	Edges   []*model.Edge
	Chunks  []ChunkMeta
}

// resolveCacheDir returns dir if non-empty, else DefaultCacheDirName in the
// current working directory.
func resolveCacheDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	return DefaultCacheDirName, nil
}

// manifestPath is the on-disk location of repoID's Manifest under dir.
func manifestPath(dir string, repoID int64) string {
	return filepath.Join(dir, fmt.Sprintf("manifest-%d.json", repoID))
}

// bm25Path is the on-disk location of repoID's persisted BM25 index under
// dir.
func bm25Path(dir string, repoID int64) string {
	return filepath.Join(dir, fmt.Sprintf("bm25-%d.json", repoID))
}

// SaveManifest writes m to dir, creating dir if needed.
func SaveManifest(dir string, m *Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("indexer: create cache dir %s: %w", dir, err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("indexer: marshal manifest: %w", err)
	}
	path := manifestPath(dir, m.RepoID)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("indexer: write manifest %s: %w", path, err)
	}
	return nil
}

// LoadManifest reads a Manifest previously written by SaveManifest for
// repoID under dir.
func LoadManifest(dir string, repoID int64) (*Manifest, error) {
	path := manifestPath(dir, repoID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("indexer: read manifest %s (run `cornifer index` first?): %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("indexer: parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// BySymbolID indexes m.Symbols by ID for O(1) lookup.
func (m *Manifest) BySymbolID() map[int64]*model.Symbol {
	out := make(map[int64]*model.Symbol, len(m.Symbols))
	for _, s := range m.Symbols {
		out[s.ID] = s
	}
	return out
}

// ByFileID indexes m.Files by ID for O(1) lookup.
func (m *Manifest) ByFileID() map[int64]*model.File {
	out := make(map[int64]*model.File, len(m.Files))
	for _, f := range m.Files {
		out[f.ID] = f
	}
	return out
}

// ByChunkID indexes m.Chunks by ID for O(1) lookup.
func (m *Manifest) ByChunkID() map[int64]ChunkMeta {
	out := make(map[int64]ChunkMeta, len(m.Chunks))
	for _, c := range m.Chunks {
		out[c.ID] = c
	}
	return out
}
