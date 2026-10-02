package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestLoadPinnedDatasetAndVerifySourceLabels(t *testing.T) {
	root := filepath.Join("..", "..")
	dataset, err := Load(filepath.Join(root, "eval", "queries.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(dataset.Queries); got < 20 {
		t.Fatalf("query count = %d, want at least 20", got)
	}
	for _, query := range dataset.Queries {
		if query.Verification != VerificationSource {
			t.Errorf("query %s verification = %q, want source", query.ID, query.Verification)
		}
	}
	// The checkout is deliberately optional in ordinary unit-test runs. The
	// committed labels are still parsed and validated above; a source checkout
	// adds the stronger line-by-line verification used by cornifer eval.
	if _, err := os.Stat(filepath.Join(root, "repos", "fastapi")); err == nil {
		if err := dataset.VerifySourceLabels(filepath.Join(root, "repos", "fastapi")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetricsUseFixedCutoffAndUniqueLabels(t *testing.T) {
	labels := []Label{{Path: "a.py", StartLine: 2, EndLine: 2}, {Path: "a.py", StartLine: 4, EndLine: 4}}
	items := []Item{
		{Path: "a.py", StartLine: 2, EndLine: 2},
		{Path: "a.py", StartLine: 2, EndLine: 2},
		{Path: "a.py", StartLine: 4, EndLine: 4},
	}
	m := metricsFor(items, labels)
	if m.Precision5 != 0.6 || m.Recall5 != 1 || m.MRR != 1 {
		t.Fatalf("metrics = %#v, want P@5=.6 R@5=1 MRR=1", m)
	}
}

type staticVector struct{ chunks []*model.Chunk }

func (s staticVector) VectorSearch(_ context.Context, _ []float32, limit int) ([]*model.Chunk, error) {
	if len(s.chunks) > limit {
		return s.chunks[:limit], nil
	}
	return s.chunks, nil
}

func TestRunWritesAllAvailableSystemsAndMarksNoGraphBoost(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "needle.py"), []byte("needle = True\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dataset := &Dataset{Version: 1, Target: Target{Repository: "fixture", Commit: "abc"}}
	for i := 0; i < 20; i++ {
		dataset.Queries = append(dataset.Queries, Query{
			ID: "q" + string(rune('a'+i)), Type: "identifier", Text: "needle", Verification: VerificationSource,
			Evidence: "pkg/needle.py:1", Relevant: []Label{{Path: "pkg/needle.py", StartLine: 1, EndLine: 1}},
		})
	}
	chunk := &model.Chunk{ID: 1, FileID: 1, StartLine: 1, EndLine: 1, Text: "needle = True"}
	sparse := bm25.New()
	if err := sparse.Index(context.Background(), []*model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	embedder, err := embed.New(embed.Config{Provider: embed.ProviderFake})
	if err != nil {
		t.Fatal(err)
	}
	manifest := &indexer.Manifest{Files: []*model.File{{ID: 1, Path: "pkg/needle.py"}}, Chunks: []indexer.ChunkMeta{{ID: 1, FileID: 1, StartLine: 1, EndLine: 1, Text: chunk.Text}}}
	report, err := Run(context.Background(), dataset, Config{
		RepoRoot: repo, CommitSHA: "abc", Manifest: manifest, Sparse: sparse,
		Vector: staticVector{chunks: []*model.Chunk{chunk}}, Embedder: embedder,
		Embedding: EmbeddingMetadata{QueryProvider: "fake", IndexedProvider: "fake", SemanticallyMeaningful: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.GraphBoost.Available {
		t.Fatal("graph boost unexpectedly marked available")
	}
	if got := len(report.Systems); got != 4 {
		t.Fatalf("systems = %d, want hybrid/bm25/vector/ripgrep only", got)
	}
	if report.LabelCounts[VerificationSource] != 20 || report.LabelCounts[VerificationIDE] != 0 {
		t.Fatalf("label counts = %#v", report.LabelCounts)
	}
	output := filepath.Join(t.TempDir(), "nested", "results.json")
	if err := WriteReport(output, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}
