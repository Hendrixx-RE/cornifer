package eval

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestLoadPinnedDatasetAndVerifyLabelProvenance(t *testing.T) {
	root := filepath.Join("..", "..")
	dataset, err := Load(filepath.Join(root, "eval", "queries.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(dataset.Queries); got < 20 {
		t.Fatalf("query count = %d, want at least 20", got)
	}
	var source, ide int
	for _, query := range dataset.Queries {
		switch query.Verification {
		case VerificationSource:
			source++
		case VerificationIDE:
			ide++
		default:
			t.Errorf("query %s verification = %q, want source or ide", query.ID, query.Verification)
		}
	}
	if source != 15 || ide != 7 {
		t.Errorf("label provenance = source:%d ide:%d, want source:15 ide:7", source, ide)
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
	if runtime.GOOS == "windows" {
		t.Skip("offline command fixture requires a POSIX shell")
	}
	// This report unit test already uses fake vectors. Give its lexical
	// baseline a deterministic command fixture too, without requiring rg to
	// be installed on the CI host. Real rg has a separate integration check.
	bin := t.TempDir()
	command := "#!/bin/sh\n" +
		"[ \"$#\" -eq 10 ] && [ \"$1\" = --json ] && [ \"$9\" = needle ] && [ \"${10}\" = . ] && [ -f pkg/needle.py ] || exit 2\n" +
		"printf '%s\\n' '{\"type\":\"match\",\"data\":{\"path\":{\"text\":\"./pkg/needle.py\"},\"line_number\":1}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "rg"), []byte(command), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
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
	for _, system := range report.Systems {
		if system.System != "ripgrep" {
			continue
		}
		for _, query := range system.Queries {
			if len(query.Results) != 1 || query.Results[0].Path != "pkg/needle.py" || query.Results[0].StartLine != 1 || query.Results[0].EndLine != 1 || query.Results[0].Score != 1 {
				t.Fatalf("offline command fixture not reflected in report: %+v", query)
			}
		}
	}
	output := filepath.Join(t.TempDir(), "nested", "results.json")
	if err := WriteReport(output, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}

func TestRipgrepMatchesRealSourceWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("install ripgrep to run the real command integration check")
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "needle.py"), []byte("needle = True\nhelper = needle\nneedles = False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("needle helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := ripgrep(t.Context(), repo, "needle needle helper")
	if err != nil || len(items) != 2 {
		t.Fatalf("real ripgrep results = %+v, err=%v", items, err)
	}
	if items[0].Path != "pkg/needle.py" || items[0].StartLine != 2 || items[0].EndLine != 2 || items[0].Score != 2 || items[1].StartLine != 1 || items[1].Score != 1 {
		t.Fatalf("real ripgrep scoring/line citations = %+v", items)
	}
	if items, err := ripgrep(t.Context(), repo, "missingtoken"); err != nil || len(items) != 0 {
		t.Fatalf("real ripgrep no-match = %+v, err=%v", items, err)
	}
}

func TestRipgrepMissingCommandReturnsError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := ripgrep(t.Context(), t.TempDir(), "needle")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing ripgrep error = %v, want executable-not-found", err)
	}
}
