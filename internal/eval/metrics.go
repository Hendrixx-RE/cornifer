package eval

// Item is one retrieved source location. Chunk-based systems return chunk
// spans; ripgrep returns matching source lines. Both are evaluated against
// the same labelled source spans.
type Item struct {
	ID        int64   `json:"id,omitempty"`
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
}

// Metrics follows standard IR definitions at a fixed cutoff. Precision uses
// the fixed denominator k (rather than returned-result count), so a system
// with fewer than five hits is not accidentally rewarded. Recall counts each
// labelled source span once even if multiple returned chunks overlap it.
type Metrics struct {
	Queries    int     `json:"queries"`
	Precision5 float64 `json:"precision_at_5"`
	Recall5    float64 `json:"recall_at_5"`
	MRR        float64 `json:"mrr"`
}

func metricsFor(items []Item, labels []Label) Metrics {
	if len(labels) == 0 {
		return Metrics{Queries: 1}
	}
	limit := len(items)
	if limit > Cutoff {
		limit = Cutoff
	}
	matched := make(map[int]bool)
	relevantResults := 0
	firstRank := 0
	for i := 0; i < limit; i++ {
		matches := matchingLabels(items[i], labels)
		if len(matches) == 0 {
			continue
		}
		relevantResults++
		if firstRank == 0 {
			firstRank = i + 1
		}
		for _, labelIndex := range matches {
			matched[labelIndex] = true
		}
	}
	m := Metrics{
		Queries:    1,
		Precision5: float64(relevantResults) / Cutoff,
		Recall5:    float64(len(matched)) / float64(len(labels)),
	}
	if firstRank > 0 {
		m.MRR = 1 / float64(firstRank)
	}
	return m
}

func matchingLabels(item Item, labels []Label) []int {
	var matched []int
	for i, label := range labels {
		if item.Path != label.Path || item.EndLine < label.StartLine || item.StartLine > label.EndLine {
			continue
		}
		matched = append(matched, i)
	}
	return matched
}

func averageMetrics(metrics []Metrics) Metrics {
	if len(metrics) == 0 {
		return Metrics{}
	}
	var result Metrics
	for _, m := range metrics {
		result.Queries += m.Queries
		result.Precision5 += m.Precision5
		result.Recall5 += m.Recall5
		result.MRR += m.MRR
	}
	n := float64(len(metrics))
	result.Precision5 /= n
	result.Recall5 /= n
	result.MRR /= n
	return result
}
