package bm25

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sort"
	"sync"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// Default Okapi BM25 parameters. k1 controls term-frequency saturation
// (how quickly repeated occurrences of a term in a document stop adding
// score); b controls document-length normalization (0 = none, 1 = full).
// 1.2 and 0.75 are the values from the original Okapi BM25 paper and are
// the de facto default in most search engines (Lucene/Elasticsearch used
// these for years); code chunks are short and fairly uniform in length
// compared to general prose, so there's no codebase-specific reason to
// deviate from the standard defaults.
const (
	DefaultK1 = 1.2
	DefaultB  = 0.75
)

// postingsEntry is one (document, term frequency) pair in a term's
// postings list.
type postingsEntry struct {
	ChunkID int64
	Freq    int
}

// document holds the per-chunk bookkeeping BM25 scoring needs.
type document struct {
	ChunkID  int64
	Length   int // token count, for length normalization
	TermFreq map[string]int
}

// Index is an in-process, disk-persistable Okapi BM25 index over
// model.Chunk text. It implements SparseIndex.
//
// Choice of backend: plan.md offers a choice between this in-process index
// and Postgres FTS (ts_rank_cd over the chunks.tsv generated column from
// migration 00006). This package picks in-process for two reasons: (1) it
// keeps internal/bm25 fully independent of internal/store and the DB
// schema, so this Wave-2 package can be built and tested without
// coordinating with the store worker or needing a live Postgres instance
// (the task's "no network access in tests" requirement is otherwise hard
// to satisfy for FTS); (2) an in-process inverted index with explicit
// postings lists is easy to unit-test against hand-computed scores. The
// tradeoff: Postgres FTS would avoid duplicating chunk text into a second
// on-disk structure and would let retrieval push ranking into the
// database, and ts_rank_cd's proximity-aware ranking is more
// sophisticated than plain Okapi BM25. If per-repo scale ever makes an
// in-memory postings map impractical, swapping to a Postgres-FTS
// SparseIndex implementation is a drop-in change behind this same
// interface.
//
// Persistence/incremental-update note (plan.md Day 20-21 "incremental
// reindex"): Index(ctx, chunks) treats the given chunks as authoritative
// for their FileID(s) — see Index's doc comment for the exact replace
// semantics an incremental reindexer should rely on. The whole index is
// (de)serialized as one JSON blob via Save/Load; a future incremental path
// can call Save after every Index() call, or batch several Index() calls
// per file-change event before persisting.
type Index struct {
	mu sync.RWMutex

	k1 float64
	b  float64

	docs        map[int64]*document // chunkID -> document
	postings    map[string][]postingsEntry
	totalLength int64 // sum of all doc lengths, for avgdl
}

// New returns an empty in-process BM25 index using DefaultK1 and DefaultB.
func New() SparseIndex {
	return NewIndex(DefaultK1, DefaultB)
}

// NewIndex returns an empty in-process BM25 index with explicit k1/b
// parameters, for tests and callers that want to tune scoring.
func NewIndex(k1, b float64) *Index {
	return &Index{
		k1:       k1,
		b:        b,
		docs:     make(map[int64]*document),
		postings: make(map[string][]postingsEntry),
	}
}

// Index (re-)indexes the given chunks. Each chunk replaces any existing
// entry for the same ChunkID (its old postings are removed first), so
// calling Index again with a changed chunk's new text correctly updates
// scoring without a full rebuild — this is the "incremental update"
// semantics referenced by plan.md's Day 20-21 incremental reindex: when a
// file changes, the caller re-chunks it and calls Index with just that
// file's new chunks plus the IDs of any chunks that no longer exist
// removed via Remove. A nil or empty slice is a no-op.
func (idx *Index) Index(ctx context.Context, chunks []*model.Chunk) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, c := range chunks {
		idx.indexOneLocked(c)
	}
	return nil
}

func (idx *Index) indexOneLocked(c *model.Chunk) {
	if c == nil {
		return
	}
	idx.removeLocked(c.ID)

	tokens := Tokenize(c.ContextHeader + " " + c.Text)
	if len(tokens) == 0 {
		// Still register the document so callers can distinguish "empty
		// chunk indexed" from "chunk never indexed" if ever needed; an
		// all-stopword or blank chunk simply never matches any query.
		idx.docs[c.ID] = &document{ChunkID: c.ID, TermFreq: map[string]int{}}
		return
	}

	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}

	idx.docs[c.ID] = &document{ChunkID: c.ID, Length: len(tokens), TermFreq: tf}
	idx.totalLength += int64(len(tokens))

	for term, freq := range tf {
		idx.postings[term] = append(idx.postings[term], postingsEntry{ChunkID: c.ID, Freq: freq})
	}
}

// Remove deletes a chunk from the index, e.g. when a file is deleted or a
// chunk boundary shifts during incremental reindex. It is safe to call for
// a ChunkID that was never indexed.
func (idx *Index) Remove(chunkID int64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeLocked(chunkID)
}

func (idx *Index) removeLocked(chunkID int64) {
	doc, ok := idx.docs[chunkID]
	if !ok {
		return
	}
	idx.totalLength -= int64(doc.Length)
	for term := range doc.TermFreq {
		entries := idx.postings[term]
		for i, e := range entries {
			if e.ChunkID == chunkID {
				entries = append(entries[:i], entries[i+1:]...)
				break
			}
		}
		if len(entries) == 0 {
			delete(idx.postings, term)
		} else {
			idx.postings[term] = entries
		}
	}
	delete(idx.docs, chunkID)
}

// Search returns the top `limit` chunks for query, ranked by Okapi BM25
// score, highest first. Ties break by ascending ChunkID for determinism.
func (idx *Index) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if limit <= 0 || len(idx.docs) == 0 {
		return nil, nil
	}

	queryTerms := uniqueTokens(Tokenize(query))
	if len(queryTerms) == 0 {
		return nil, nil
	}

	n := float64(len(idx.docs))
	avgdl := 0.0
	if n > 0 {
		avgdl = float64(idx.totalLength) / n
	}

	scores := make(map[int64]float64)
	for _, term := range queryTerms {
		entries := idx.postings[term]
		if len(entries) == 0 {
			continue
		}
		// Okapi BM25 IDF, with the standard +1 inside the log to keep it
		// non-negative even when a term appears in every document.
		idf := math.Log(1 + (n-float64(len(entries))+0.5)/(float64(len(entries))+0.5))

		for _, e := range entries {
			doc := idx.docs[e.ChunkID]
			tf := float64(e.Freq)
			norm := 1 - idx.b + idx.b*(float64(doc.Length)/avgdl)
			score := idf * (tf * (idx.k1 + 1)) / (tf + idx.k1*norm)
			scores[e.ChunkID] += score
		}
	}

	results := make([]Result, 0, len(scores))
	for chunkID, score := range scores {
		results = append(results, Result{ChunkID: chunkID, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ChunkID < results[j].ChunkID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func uniqueTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := tokens[:0:0]
	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// persisted is the on-disk JSON representation of an Index.
type persisted struct {
	K1   float64             `json:"k1"`
	B    float64             `json:"b"`
	Docs map[int64]*document `json:"docs"`
}

// Save writes the index to path as JSON, for the disk persistence plan.md
// requires of an in-process BM25 index. Postings are not stored directly;
// Load rebuilds them from the persisted per-document term frequencies so
// the two structures can never drift out of sync.
func (idx *Index) Save(path string) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	data, err := json.Marshal(persisted{K1: idx.k1, B: idx.b, Docs: idx.docs})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load reads an index previously written by Save.
func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}

	idx := NewIndex(p.K1, p.B)
	for id, doc := range p.Docs {
		doc.ChunkID = id
		idx.docs[id] = doc
		idx.totalLength += int64(doc.Length)
		for term, freq := range doc.TermFreq {
			idx.postings[term] = append(idx.postings[term], postingsEntry{ChunkID: id, Freq: freq})
		}
	}
	return idx, nil
}
