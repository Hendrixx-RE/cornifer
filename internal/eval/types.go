// Package eval evaluates Cornifer retrieval against hand-labelled source
// locations. It deliberately records how labels were verified: source
// inspection and IDE reference results are different evidence and must not
// be conflated in a report.
package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// Cutoff is the fixed rank used by the Week 3 metrics.
	Cutoff = 5

	VerificationSource = "source"
	VerificationIDE    = "ide"
)

// Dataset is the versioned, pinned collection of labelled retrieval tasks.
// The target commit prevents silently evaluating labels against a different
// FastAPI revision.
type Dataset struct {
	Version int     `yaml:"version" json:"version"`
	Target  Target  `yaml:"target" json:"target"`
	Queries []Query `yaml:"queries" json:"queries"`
}

type Target struct {
	Repository string `yaml:"repository" json:"repository"`
	Commit     string `yaml:"commit" json:"commit"`
}

// Query is one labelled task. Type is structural, semantic, or identifier.
// Verification describes the provenance of every label in Relevant.
type Query struct {
	ID           string  `yaml:"id" json:"id"`
	Type         string  `yaml:"type" json:"type"`
	Text         string  `yaml:"query" json:"query"`
	Verification string  `yaml:"verification" json:"verification"`
	Evidence     string  `yaml:"evidence" json:"evidence"`
	Relevant     []Label `yaml:"relevant" json:"relevant"`
}

// Label is a source span that is relevant to a query. Symbol is explanatory
// only: matching uses the path/span so the labels remain valid even when a
// chunker chooses a different class/header chunk boundary.
type Label struct {
	Path      string `yaml:"path" json:"path"`
	StartLine int    `yaml:"start_line" json:"start_line"`
	EndLine   int    `yaml:"end_line" json:"end_line"`
	Symbol    string `yaml:"symbol" json:"symbol"`
}

// Load reads and validates a labelled YAML dataset. Known fields are strict
// so a misspelled label key cannot quietly turn into an empty ground truth.
func Load(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read queries %s: %w", path, err)
	}
	var ds Dataset
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&ds); err != nil {
		return nil, fmt.Errorf("eval: parse queries %s: %w", path, err)
	}
	if err := ds.Validate(); err != nil {
		return nil, err
	}
	return &ds, nil
}

func (d Dataset) Validate() error {
	if d.Version <= 0 {
		return fmt.Errorf("eval: dataset version must be positive")
	}
	if d.Target.Repository == "" || d.Target.Commit == "" {
		return fmt.Errorf("eval: target.repository and target.commit are required")
	}
	if len(d.Queries) < 20 {
		return fmt.Errorf("eval: dataset has %d queries, need at least 20", len(d.Queries))
	}
	seen := map[string]bool{}
	for _, q := range d.Queries {
		if q.ID == "" || q.Text == "" {
			return fmt.Errorf("eval: every query needs id and query text")
		}
		if seen[q.ID] {
			return fmt.Errorf("eval: duplicate query id %q", q.ID)
		}
		seen[q.ID] = true
		switch q.Type {
		case "structural", "semantic", "identifier":
		default:
			return fmt.Errorf("eval: query %q has unsupported type %q", q.ID, q.Type)
		}
		if q.Verification != VerificationSource && q.Verification != VerificationIDE {
			return fmt.Errorf("eval: query %q verification must be %q or %q", q.ID, VerificationSource, VerificationIDE)
		}
		if q.Evidence == "" {
			return fmt.Errorf("eval: query %q needs an evidence note", q.ID)
		}
		if len(q.Relevant) == 0 {
			return fmt.Errorf("eval: query %q has no relevant labels", q.ID)
		}
		for _, label := range q.Relevant {
			if label.Path == "" || label.StartLine <= 0 || label.EndLine < label.StartLine {
				return fmt.Errorf("eval: query %q has invalid label %#v", q.ID, label)
			}
		}
	}
	return nil
}

// VerifySourceLabels checks that every source-verified label points to a
// non-empty span in repoRoot. It does not pretend to perform an IDE check;
// IDE-verified labels are intentionally left to the recorded provenance.
func (d Dataset) VerifySourceLabels(repoRoot string) error {
	for _, q := range d.Queries {
		if q.Verification != VerificationSource {
			continue
		}
		for _, label := range q.Relevant {
			path := filepath.Join(repoRoot, filepath.FromSlash(label.Path))
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("eval: source label %s (%s): %w", q.ID, label.Path, err)
			}
			lines := strings.Split(string(data), "\n")
			if label.EndLine > len(lines) {
				return fmt.Errorf("eval: source label %s %s:%d-%d exceeds %d source lines", q.ID, label.Path, label.StartLine, label.EndLine, len(lines))
			}
			found := false
			for _, line := range lines[label.StartLine-1 : label.EndLine] {
				if strings.TrimSpace(line) != "" {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("eval: source label %s %s:%d-%d is blank", q.ID, label.Path, label.StartLine, label.EndLine)
			}
		}
	}
	return nil
}

// QueryTypes returns dataset query types in stable order.
func (d Dataset) QueryTypes() []string {
	seen := map[string]bool{}
	for _, q := range d.Queries {
		seen[q.Type] = true
	}
	out := make([]string, 0, len(seen))
	for typ := range seen {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}
