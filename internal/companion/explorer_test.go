package companion

import (
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestGraphDataUsesResolvedSymbolsAndEdges(t *testing.T) {
	repo := Repository{ID: "repo-1", ResolvedCommitSHA: "abc123"}
	module, function, cls := model.SymbolKindModule, model.SymbolKindFunction, model.SymbolKindClass
	session := &indexer.Session{Manifest: &indexer.Manifest{
		Files: []*model.File{{ID: 1, Path: "pkg/handler.py", Language: "python"}, {ID: 2, Path: "README.md", Language: "markdown"}},
		Symbols: []*model.Symbol{
			{ID: 11, FileID: 1, Kind: module, Name: "pkg.handler", QualifiedName: "pkg.handler", StartLine: 1, EndLine: 40},
			{ID: 12, FileID: 1, Kind: function, Name: "handle", QualifiedName: "pkg.handler.handle", Signature: "def handle(request):", StartLine: 4, EndLine: 20},
			{ID: 13, FileID: 1, Kind: cls, Name: "Router", QualifiedName: "pkg.handler.Router", StartLine: 22, EndLine: 40},
		},
		Edges: []*model.Edge{{ID: 21, SrcSymbolID: 12, DstSymbolID: 13, Kind: model.EdgeKindCalls, Confidence: model.ConfidenceHigh}, {ID: 22, SrcSymbolID: 12, DstSymbolID: 999, Kind: model.EdgeKindImports, Confidence: model.ConfidenceLow}},
	}}

	got := graphData(repo, session, "")
	if got.RepositoryID != repo.ID || got.CommitSHA != repo.ResolvedCommitSHA {
		t.Fatalf("snapshot identity = %q @ %q", got.RepositoryID, got.CommitSHA)
	}
	if got.StructuralLanguage != "python" || len(got.Files) != 2 {
		t.Fatalf("catalog metadata = language %q files %v", got.StructuralLanguage, got.Files)
	}
	if len(got.Nodes) != 3 || got.Nodes[1].Signature != "def handle(request):" {
		t.Fatalf("nodes = %#v", got.Nodes)
	}
	if len(got.Edges) != 1 || got.Edges[0].Source != "s:12" || got.Edges[0].Target != "s:13" || got.Edges[0].Confidence != float64(model.ConfidenceHigh) {
		t.Fatalf("edges = %#v", got.Edges)
	}

	functions := graphData(repo, session, "function")
	if len(functions.Nodes) != 1 || functions.Nodes[0].ID != "s:12" || len(functions.Edges) != 0 {
		t.Fatalf("filtered graph = %#v", functions)
	}
}
