package bm25

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func chunk(id int64, text string) *model.Chunk {
	return &model.Chunk{ID: id, Text: text}
}

// TestScoringAgainstHandComputedCorpus verifies the scorer's output
// against a value independently hand-derived from the Okapi BM25 formula
// (see the comment below), rather than trusting the implementation by
// inspection.
//
// Corpus:
//
//	doc 1: "cat cat dog"  (tokens: cat, cat, dog -> length 3)
//	doc 2: "dog bird"     (tokens: dog, bird     -> length 2)
//
// Query: "cat". n=2 docs, avgdl=(3+2)/2=2.5. "cat" appears in df=1 doc
// with term frequency tf=2 in doc 1, and does not appear in doc 2.
//
//	idf  = ln(1 + (n-df+0.5)/(df+0.5)) = ln(1 + 1.5/1.5) = ln(2) = 0.6931471805599453
//	norm = 1-b + b*(len/avgdl) = 0.25 + 0.75*(3/2.5) = 1.15
//	score(doc1) = idf * tf*(k1+1) / (tf + k1*norm)
//	            = 0.6931471805599453 * (2*2.2) / (2 + 1.2*1.15)
//	            = 0.902321773509988
//
// doc 2 has no occurrence of "cat" so it must not appear in results at
// all (BM25 score 0 for an absent term contributes nothing, and Search
// only returns documents with at least one matching term).
func TestScoringAgainstHandComputedCorpus(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{
		chunk(1, "cat cat dog"),
		chunk(2, "dog bird"),
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	results, err := idx.Search(ctx, "cat", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() = %v, want exactly 1 result", results)
	}
	if results[0].ChunkID != 1 {
		t.Errorf("Search() top result ChunkID = %d, want 1", results[0].ChunkID)
	}

	const want = 0.902321773509988
	if math.Abs(results[0].Score-want) > 1e-9 {
		t.Errorf("Search() score = %.15f, want %.15f", results[0].Score, want)
	}
}

func TestSearchRanksHigherTermFrequencyFirst(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{
		chunk(1, "walker parses files on disk"),
		chunk(2, "walker walker walker traverses the walker tree"),
		chunk(3, "completely unrelated content about databases"),
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	results, err := idx.Search(ctx, "walker", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search() = %v, want 2 results", results)
	}
	if results[0].ChunkID != 2 {
		t.Errorf("top result = chunk %d, want chunk 2 (higher term frequency)", results[0].ChunkID)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("expected strictly decreasing scores, got %v", results)
	}
}

func TestSearchMultiTermQueryUnion(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{
		chunk(1, "parse the chunk header"),
		chunk(2, "embed the chunk vector"),
		chunk(3, "walk the file tree"),
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	results, err := idx.Search(ctx, "chunk header", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	got := map[int64]bool{}
	for _, r := range results {
		got[r.ChunkID] = true
	}
	if !got[1] || !got[2] {
		t.Errorf("Search() = %v, want chunks 1 and 2 both matched (union of query terms)", results)
	}
	if got[3] {
		t.Errorf("Search() unexpectedly matched chunk 3: %v", results)
	}
	if len(results) < 2 || results[0].ChunkID != 1 {
		t.Errorf("chunk 1 (matches both terms) should outrank chunk 2 (matches one): %v", results)
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	chunks := make([]*model.Chunk, 0, 5)
	for i := int64(1); i <= 5; i++ {
		chunks = append(chunks, chunk(i, "shared token"))
	}
	if err := idx.Index(ctx, chunks); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	results, err := idx.Search(ctx, "shared", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Search() returned %d results, want 2 (limit)", len(results))
	}
}

func TestSearchExactIdentifierRanksAboveSplitOnlyMatch(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{
		chunk(1, "func parseChunkHeader() {}"),
		chunk(2, "func parse() { var header string }"),
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// The exact identifier query should match chunk 1 via the unsplit
	// "parsechunkheader" token (and also via its "parse"/"header" parts),
	// giving it more matching terms than chunk 2, which only matches the
	// split parts.
	results, err := idx.Search(ctx, "parseChunkHeader", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 || results[0].ChunkID != 1 {
		t.Errorf("Search() = %v, want chunk 1 ranked first", results)
	}
}

func TestEmptyCorpusSearch(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	results, err := idx.Search(context.Background(), "anything", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search() on empty corpus = %v, want empty", results)
	}
}

func TestEmptyQuerySearch(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{chunk(1, "some content")}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	results, err := idx.Search(ctx, "", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search(\"\") = %v, want empty", results)
	}
}

func TestDegenerateChunksDoNotPanic(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	err := idx.Index(ctx, []*model.Chunk{
		chunk(1, ""),
		chunk(2, "   "),
		chunk(3, "___...---"),
		chunk(4, "real content here"),
	})
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	results, err := idx.Search(ctx, "real content", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != 4 {
		t.Errorf("Search() = %v, want only chunk 4", results)
	}
}

func TestIndexNilAndEmptyChunksIsNoop(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, nil); err != nil {
		t.Fatalf("Index(nil) error = %v", err)
	}
	if err := idx.Index(ctx, []*model.Chunk{}); err != nil {
		t.Fatalf("Index([]) error = %v", err)
	}
	results, err := idx.Search(ctx, "anything", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search() = %v, want empty", results)
	}
}

func TestReindexReplacesChunk(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{chunk(1, "original content about caching")}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if results, _ := idx.Search(ctx, "caching", 10); len(results) != 1 {
		t.Fatalf("expected chunk 1 to match 'caching' before reindex, got %v", results)
	}

	// Simulate a file changing: the same ChunkID is re-indexed with new
	// text, which is the incremental-update contract Index documents.
	if err := idx.Index(ctx, []*model.Chunk{chunk(1, "rewritten content about routing")}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	if results, _ := idx.Search(ctx, "caching", 10); len(results) != 0 {
		t.Errorf("stale term 'caching' still matches after reindex: %v", results)
	}
	results, err := idx.Search(ctx, "routing", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != 1 {
		t.Errorf("Search() = %v, want chunk 1 matching new content", results)
	}
}

func TestRemoveDeletesChunk(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{chunk(1, "temporary content")}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	idx.Remove(1)
	// Removing a never-indexed ID must not panic or error.
	idx.Remove(999)

	results, err := idx.Search(ctx, "temporary", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search() after Remove = %v, want empty", results)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	idx := NewIndex(DefaultK1, DefaultB)
	ctx := context.Background()
	if err := idx.Index(ctx, []*model.Chunk{
		chunk(1, "cat cat dog"),
		chunk(2, "dog bird"),
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want, err := idx.Search(ctx, "cat", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	got, err := loaded.Search(ctx, "cat", 10)
	if err != nil {
		t.Fatalf("Search() on loaded index error = %v", err)
	}
	if len(got) != len(want) || got[0].ChunkID != want[0].ChunkID {
		t.Fatalf("Search() after Load = %v, want %v", got, want)
	}
	if math.Abs(got[0].Score-want[0].Score) > 1e-9 {
		t.Errorf("Search() score after Load = %v, want %v", got[0].Score, want[0].Score)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Error("Load() on missing file, want error")
	}
}

func TestNewImplementsSparseIndex(t *testing.T) {
	var _ SparseIndex = New()
}

// pythonFixture is a small realistic Python-like corpus used to exercise
// ranking over a more natural query set than the synthetic corpora above.
var pythonFixture = []*model.Chunk{
	chunk(1, `
class RateLimiter:
    def __init__(self, max_requests, window_seconds):
        self.max_requests = max_requests
        self.window_seconds = window_seconds

    def check_limit(self, user_id):
        """Return True if user_id is within the rate limit."""
        count = self._get_request_count(user_id)
        return count < self.max_requests
`),
	chunk(2, `
def parse_chunk_header(text):
    """Extract the file path and enclosing symbol from a chunk header."""
    lines = text.splitlines()
    return lines[0] if lines else ""
`),
	chunk(3, `
class HTTPServer:
    def handle_request(self, request):
        response = self.router.dispatch(request)
        return response
`),
	chunk(4, `
def connect_to_database(db_url):
    """Open a connection pool to the configured database."""
    return DatabasePool(db_url)
`),
	chunk(5, `
def _private_helper(x):
    # unrelated arithmetic helper
    return x * 2 + 1
`),
}

func newPythonFixtureIndex(t *testing.T) *Index {
	t.Helper()
	idx := NewIndex(DefaultK1, DefaultB)
	if err := idx.Index(context.Background(), pythonFixture); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	return idx
}

func TestRealisticQuerySet(t *testing.T) {
	idx := newPythonFixtureIndex(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		query     string
		wantTopID int64
	}{
		{"rate limiting concept", "rate limiting", 1},
		{"exact identifier", "check_limit", 1},
		{"chunk header split terms", "chunk header", 2},
		{"camel-case class name", "HTTPServer", 3},
		{"database connection", "database connection", 4},
		{"dispatch request", "dispatch request", 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := idx.Search(ctx, tc.query, 5)
			if err != nil {
				t.Fatalf("Search(%q) error = %v", tc.query, err)
			}
			if len(results) == 0 {
				t.Fatalf("Search(%q) = empty, want chunk %d on top", tc.query, tc.wantTopID)
			}
			if results[0].ChunkID != tc.wantTopID {
				t.Errorf("Search(%q) top = chunk %d, want chunk %d (results: %v)", tc.query, results[0].ChunkID, tc.wantTopID, results)
			}
		})
	}
}
