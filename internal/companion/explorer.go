package companion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

// Explorer reads structural catalog data from the same indexed Postgres
// snapshot used by retrieval. The file endpoint only serves paths recorded in
// that catalog and resolves symlinks before opening them.
type Explorer struct{ Engine store.Store }

type GraphNode struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Signature     string `json:"signature,omitempty"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
}
type GraphEdge struct {
	ID         int64   `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}
type GraphData struct {
	RepositoryID       string      `json:"repository_id"`
	CommitSHA          string      `json:"commit_sha"`
	StructuralLanguage string      `json:"structural_language"`
	Nodes              []GraphNode `json:"nodes"`
	Edges              []GraphEdge `json:"edges"`
	Files              []string    `json:"files"`
}
type SourceView struct {
	Path      string `json:"path"`
	Language  string `json:"language"`
	Content   string `json:"content"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (e Explorer) session(ctx context.Context, repo Repository) (*indexer.Session, error) {
	if e.Engine == nil {
		return nil, fmt.Errorf("structural explorer is not configured")
	}
	return indexer.OpenSession(ctx, e.Engine, repo.CheckoutPath, repo.CacheDir)
}

func (e Explorer) Graph(ctx context.Context, repo Repository, kind string) (GraphData, error) {
	s, err := e.session(ctx, repo)
	if err != nil {
		return GraphData{}, err
	}
	return graphData(repo, s, kind), nil
}

func graphData(repo Repository, s *indexer.Session, kind string) GraphData {
	out := GraphData{RepositoryID: repo.ID, CommitSHA: repo.ResolvedCommitSHA, StructuralLanguage: "python", Nodes: []GraphNode{}, Edges: []GraphEdge{}, Files: []string{}}
	filesByID := map[int64]*model.File{}
	for _, f := range s.Manifest.Files {
		filesByID[f.ID] = f
		out.Files = append(out.Files, f.Path)
	}
	symbolsByID := map[int64]*model.Symbol{}
	for _, sym := range s.Manifest.Symbols {
		if len(out.Nodes) >= 2500 {
			break
		}
		if kind != "" && string(sym.Kind) != kind {
			continue
		}
		f, ok := filesByID[sym.FileID]
		if !ok {
			continue
		}
		symbolsByID[sym.ID] = sym
		out.Nodes = append(out.Nodes, GraphNode{ID: fmt.Sprintf("s:%d", sym.ID), Name: sym.Name, QualifiedName: sym.QualifiedName, Kind: string(sym.Kind), Path: f.Path, Signature: sym.Signature, StartLine: sym.StartLine, EndLine: sym.EndLine})
	}
	for _, edge := range s.Manifest.Edges {
		from, ok1 := symbolsByID[edge.SrcSymbolID]
		to, ok2 := symbolsByID[edge.DstSymbolID]
		if ok1 && ok2 && len(out.Edges) < 10000 {
			out.Edges = append(out.Edges, GraphEdge{ID: edge.ID, Source: fmt.Sprintf("s:%d", from.ID), Target: fmt.Sprintf("s:%d", to.ID), Kind: string(edge.Kind), Confidence: float64(edge.Confidence)})
		}
	}
	return out
}

func (e Explorer) Source(ctx context.Context, repo Repository, requested string, start, end int) (SourceView, error) {
	s, err := e.session(ctx, repo)
	if err != nil {
		return SourceView{}, err
	}
	var file *model.File
	for i := range s.Manifest.Files {
		if s.Manifest.Files[i].Path == requested {
			file = s.Manifest.Files[i]
			break
		}
	}
	if file == nil {
		return SourceView{}, fmt.Errorf("source path is not part of this indexed snapshot")
	}
	root, err := filepath.EvalSymlinks(repo.CheckoutPath)
	if err != nil {
		return SourceView{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return SourceView{}, err
	}
	candidate := filepath.Join(root, filepath.FromSlash(file.Path))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return SourceView{}, fmt.Errorf("indexed source is unavailable")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return SourceView{}, fmt.Errorf("source path leaves repository")
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return SourceView{}, fmt.Errorf("indexed source is unavailable")
	}
	lines := strings.Split(string(b), "\n")
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	if end-start > 500 {
		end = start + 500
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		start = len(lines)
	}
	return SourceView{Path: file.Path, Language: file.Language, Content: strings.Join(lines[start-1:end], "\n"), StartLine: start, EndLine: end}, nil
}
