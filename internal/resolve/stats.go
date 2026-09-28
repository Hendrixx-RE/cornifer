package resolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// KindStats counts references of one kind. External references target
// stdlib/third-party/builtin code and are excluded from recall ratios, since
// no in-repo target exists for them.
type KindStats struct {
	Resolved   int
	External   int
	Unresolved int
}

// Stats makes the heuristic's recall measurable. Counts are per reference
// (e.g. per call site), not per deduplicated edge or row.
type Stats struct {
	ByKind map[model.EdgeKind]*KindStats
	// EdgesByConfidence counts emitted (deduplicated) edges per confidence.
	EdgesByConfidence map[model.Confidence]int
	// EdgesByKind counts emitted (deduplicated) edges per kind.
	EdgesByKind map[model.EdgeKind]int
}

func (s *Stats) init() {
	s.ByKind = map[model.EdgeKind]*KindStats{}
	s.EdgesByConfidence = map[model.Confidence]int{}
	s.EdgesByKind = map[model.EdgeKind]int{}
}

func (s *Stats) finish(edges []*model.Edge) {
	for _, e := range edges {
		s.EdgesByConfidence[e.Confidence]++
		s.EdgesByKind[e.Kind]++
	}
}

func (s *Stats) kind(k model.EdgeKind) *KindStats {
	ks := s.ByKind[k]
	if ks == nil {
		ks = &KindStats{}
		s.ByKind[k] = ks
	}
	return ks
}

// ResolvedRatio returns resolved/(resolved+unresolved) for kind, ignoring
// external references, or 0 when there are none.
func (s Stats) ResolvedRatio(kind model.EdgeKind) float64 {
	ks := s.ByKind[kind]
	if ks == nil || ks.Resolved+ks.Unresolved == 0 {
		return 0
	}
	return float64(ks.Resolved) / float64(ks.Resolved+ks.Unresolved)
}

// String renders a human-readable summary.
func (s Stats) String() string {
	var b strings.Builder
	kinds := make([]string, 0, len(s.ByKind))
	for k := range s.ByKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		ks := s.ByKind[model.EdgeKind(k)]
		fmt.Fprintf(&b, "%-10s resolved=%d external=%d unresolved=%d (in-repo ratio %.1f%%)\n",
			k, ks.Resolved, ks.External, ks.Unresolved, 100*s.ResolvedRatio(model.EdgeKind(k)))
	}
	confs := make([]float64, 0, len(s.EdgesByConfidence))
	for c := range s.EdgesByConfidence {
		confs = append(confs, float64(c))
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(confs)))
	for _, c := range confs {
		fmt.Fprintf(&b, "edges at confidence %.2f: %d\n", c, s.EdgesByConfidence[model.Confidence(c)])
	}
	return b.String()
}
