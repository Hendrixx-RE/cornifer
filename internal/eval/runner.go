package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
)

const reportSchemaVersion = 1

// EmbeddingMetadata makes a result interpretable later. In particular, a
// fake provider is recorded as non-semantic so no vector/hybrid difference
// can be presented as semantic evidence.
type EmbeddingMetadata struct {
	QueryProvider          string `json:"query_provider"`
	QueryModel             string `json:"query_model"`
	IndexedProvider        string `json:"indexed_provider"`
	IndexedModel           string `json:"indexed_model"`
	IndexProviderSource    string `json:"indexed_provider_source"`
	SemanticallyMeaningful bool   `json:"semantically_meaningful"`
}

type GraphBoostMetadata struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Config supplies the already-open index resources to the evaluator.
type Config struct {
	RepoRoot  string
	CommitSHA string
	Manifest  *indexer.Manifest
	Sparse    bm25.SparseIndex
	Vector    retrieve.VectorSearcher
	Embedder  embed.Embedder
	Embedding EmbeddingMetadata

	// GraphBoost is optional. A non-nil real boost produces both full hybrid
	// and hybrid-without-graph-boost. The current production wiring has no
	// graph boost, so callers leave this nil and get an explicit limitation.
	GraphBoost            retrieve.BoostStage
	GraphBoostDescription string
}

type Report struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Target        Target             `json:"target"`
	IndexCommit   string             `json:"index_commit"`
	Embedding     EmbeddingMetadata  `json:"embedding"`
	GraphBoost    GraphBoostMetadata `json:"graph_boost"`
	LabelCounts   map[string]int     `json:"label_counts_by_verification"`
	Systems       []SystemResult     `json:"systems"`
}

type SystemResult struct {
	System        string             `json:"system"`
	Metrics       Metrics            `json:"metrics"`
	MetricsByType map[string]Metrics `json:"metrics_by_query_type"`
	Queries       []QueryResult      `json:"queries"`
}

type QueryResult struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Text           string  `json:"query"`
	Verification   string  `json:"verification"`
	RelevantLabels []Label `json:"relevant_labels"`
	Results        []Item  `json:"results"`
	Metrics        Metrics `json:"metrics"`
}

// Run executes every available system for every query and returns the raw,
// per-query ranks as well as aggregate metrics. It never invents a
// hybrid-without-graph-boost system when a real boost is absent.
func Run(ctx context.Context, dataset *Dataset, cfg Config) (*Report, error) {
	if dataset == nil || cfg.Manifest == nil || cfg.Sparse == nil || cfg.Vector == nil || cfg.Embedder == nil {
		return nil, errors.New("eval: dataset, manifest, sparse index, vector searcher, and embedder are required")
	}
	if cfg.CommitSHA != dataset.Target.Commit {
		return nil, fmt.Errorf("eval: index commit %s does not match dataset target commit %s", cfg.CommitSHA, dataset.Target.Commit)
	}
	if err := dataset.VerifySourceLabels(cfg.RepoRoot); err != nil {
		return nil, err
	}

	graphBoost := GraphBoostMetadata{
		Available: false,
		Reason:    "graph-adjacency boosting is not wired into Cornifer retrieval; no hybrid-without-graph-boost comparison was run",
	}
	if cfg.GraphBoost != nil {
		if _, noBoost := cfg.GraphBoost.(retrieve.NoBoost); !noBoost {
			graphBoost = GraphBoostMetadata{Available: true, Reason: cfg.GraphBoostDescription}
		}
	}

	report := &Report{
		SchemaVersion: reportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Target:        dataset.Target,
		IndexCommit:   cfg.CommitSHA,
		Embedding:     cfg.Embedding,
		GraphBoost:    graphBoost,
		LabelCounts:   map[string]int{},
	}
	for _, q := range dataset.Queries {
		report.LabelCounts[q.Verification]++
	}

	systems := []string{"hybrid", "bm25", "vector", "ripgrep"}
	if graphBoost.Available {
		systems = append(systems, "hybrid_without_graph_boost")
	}
	for _, system := range systems {
		result, err := runSystem(ctx, system, dataset, cfg)
		if err != nil {
			return nil, fmt.Errorf("eval: %s: %w", system, err)
		}
		report.Systems = append(report.Systems, result)
	}
	return report, nil
}

func runSystem(ctx context.Context, system string, dataset *Dataset, cfg Config) (SystemResult, error) {
	out := SystemResult{System: system, MetricsByType: map[string]Metrics{}}
	byType := map[string][]Metrics{}
	var all []Metrics
	for _, q := range dataset.Queries {
		items, err := runQuery(ctx, system, q, cfg)
		if err != nil {
			return SystemResult{}, fmt.Errorf("query %s: %w", q.ID, err)
		}
		m := metricsFor(items, q.Relevant)
		out.Queries = append(out.Queries, QueryResult{
			ID: q.ID, Type: q.Type, Text: q.Text, Verification: q.Verification,
			RelevantLabels: q.Relevant, Results: items, Metrics: m,
		})
		all = append(all, m)
		byType[q.Type] = append(byType[q.Type], m)
	}
	out.Metrics = averageMetrics(all)
	for typ, ms := range byType {
		out.MetricsByType[typ] = averageMetrics(ms)
	}
	return out, nil
}

func runQuery(ctx context.Context, system string, q Query, cfg Config) ([]Item, error) {
	switch system {
	case "bm25":
		results, err := cfg.Sparse.Search(ctx, q.Text, Cutoff)
		if err != nil {
			return nil, err
		}
		items := make([]Item, 0, len(results))
		for _, result := range results {
			if item, ok := chunkItem(cfg.Manifest, result.ChunkID, result.Score); ok {
				items = append(items, item)
			}
		}
		return items, nil

	case "vector":
		return vectorItems(ctx, q.Text, cfg)

	case "hybrid", "hybrid_without_graph_boost":
		boost := retrieve.BoostStage(nil)
		if system == "hybrid" {
			boost = cfg.GraphBoost
		}
		searcher := retrieve.NewHybridSearcher(cfg.Sparse, cfg.Vector, cfg.Embedder, retrieve.Config{Boost: boost})
		result, err := searcher.Search(ctx, q.Text, Cutoff)
		if err != nil {
			return nil, err
		}
		items := make([]Item, 0, len(result.Hits))
		for _, hit := range result.Hits {
			if item, ok := chunkItem(cfg.Manifest, hit.ChunkID, hit.Score); ok {
				items = append(items, item)
			}
		}
		return items, nil

	case "ripgrep":
		return ripgrep(ctx, cfg.RepoRoot, q.Text)
	default:
		return nil, fmt.Errorf("unknown system %q", system)
	}
}

func vectorItems(ctx context.Context, query string, cfg Config) ([]Item, error) {
	vectors, err := cfg.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embed query: got %d vectors, want 1", len(vectors))
	}
	chunks, err := cfg.Vector.VectorSearch(ctx, vectors[0], Cutoff)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(chunks))
	for rank, chunk := range chunks {
		if item, ok := chunkItem(cfg.Manifest, chunk.ID, float64(Cutoff-rank)); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func chunkItem(manifest *indexer.Manifest, chunkID int64, score float64) (Item, bool) {
	chunk, ok := manifest.ByChunkID()[chunkID]
	if !ok {
		return Item{}, false
	}
	file, ok := manifest.ByFileID()[chunk.FileID]
	if !ok {
		return Item{}, false
	}
	return Item{ID: chunk.ID, Path: file.Path, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Score: score}, true
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

// ripgrep is a deliberately simple lexical baseline. It runs rg once per
// meaningful query token, scores a matching source line by the number of
// distinct query tokens it contains, and returns five deterministic lines.
// It is not a semantic substitute and its line-level units are preserved in
// raw results rather than being passed off as AST chunks.
func ripgrep(ctx context.Context, repoRoot, query string) ([]Item, error) {
	terms := bm25.Tokenize(query)
	seenTerms := map[string]bool{}
	byLine := map[string]Item{}
	for _, term := range terms {
		if len(term) < 3 || seenTerms[term] {
			continue
		}
		seenTerms[term] = true
		cmd := exec.CommandContext(ctx, "rg", "--json", "--line-number", "--no-heading", "--ignore-case", "--word-regexp", "--glob", "*.py", "--", term, ".")
		cmd.Dir = repoRoot
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("start ripgrep: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start ripgrep: %w", err)
		}
		scanner := bufio.NewScanner(stdout)
		// Source lines can be long; rg's JSON event stays useful up to 1 MiB.
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var event rgEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Type != "match" {
				continue
			}
			path := strings.TrimPrefix(filepath.ToSlash(event.Data.Path.Text), "./")
			key := fmt.Sprintf("%s:%d", path, event.Data.LineNumber)
			item := byLine[key]
			item.Path = path
			item.StartLine = event.Data.LineNumber
			item.EndLine = event.Data.LineNumber
			item.Score++
			byLine[key] = item
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read ripgrep output: %w", err)
		}
		if err := cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				return nil, fmt.Errorf("ripgrep %q: %w", term, err)
			}
		}
	}
	items := make([]Item, 0, len(byLine))
	for _, item := range byLine {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		return items[i].StartLine < items[j].StartLine
	})
	if len(items) > Cutoff {
		items = items[:Cutoff]
	}
	return items, nil
}

// WriteReport stores indented raw data. The parent directory is created so a
// new, explicit --output path works without manual setup.
func WriteReport(path string, report *Report) error {
	if report == nil {
		return errors.New("eval: cannot write nil report")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("eval: create results directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("eval: write report %s: %w", path, err)
	}
	return nil
}
