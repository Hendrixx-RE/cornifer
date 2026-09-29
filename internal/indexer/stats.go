package indexer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Hendrixx-RE/cornifer/internal/resolve"
)

// Stats summarizes one Index run: counts at every stage plus per-phase
// timings and internal/resolve's resolution stats, so exit-criteria checks
// ("index FastAPI in under a few minutes", "log resolution stats and
// timings") have something concrete to report.
type Stats struct {
	RepoID    int64
	Root      string
	CommitSHA string

	Files              int
	ParseErrors        int
	Symbols            int
	Edges              int
	UnresolvedRefs     int
	Chunks             int
	Embedded           int
	OversizedTruncated int

	Resolve resolve.Stats

	// Durations maps a phase name ("walk", "parse+symbols", "resolve",
	// "store symbols+edges", "chunk", "embed", "store chunks", "bm25",
	// "total") to how long it took.
	Durations map[string]time.Duration
}

func newStats() *Stats {
	return &Stats{Durations: map[string]time.Duration{}}
}

func (s *Stats) track(name string) func() {
	start := time.Now()
	return func() {
		s.Durations[name] += time.Since(start)
	}
}

// String renders a human-readable multi-line summary suitable for direct
// logging after an Index run.
func (s Stats) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "repo %d (%s @ %s)\n", s.RepoID, s.Root, s.CommitSHA)
	fmt.Fprintf(&b, "files=%d parse_errors=%d symbols=%d edges=%d unresolved_refs=%d chunks=%d embedded=%d oversized_truncated=%d\n",
		s.Files, s.ParseErrors, s.Symbols, s.Edges, s.UnresolvedRefs, s.Chunks, s.Embedded, s.OversizedTruncated)
	b.WriteString(s.Resolve.String())

	names := make([]string, 0, len(s.Durations))
	for n := range s.Durations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "  %-20s %s\n", n, s.Durations[n].Round(time.Millisecond))
	}
	return b.String()
}
