package store

// These tests exercise pgStore against a real Postgres+pgvector instance.
// They SKIP (not fail) when no database is reachable, so `go test ./...`
// stays green without Docker/`make up` — see connectOrSkip.
//
// Point them at a specific database with CORNIFER_DATABASE_URL; otherwise
// they default to the docker-compose instance started by `make up`
// (localhost:5433).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

// connectOrSkip returns a live *pgStore, or skips the test if no Postgres
// is reachable within a short timeout.
func connectOrSkip(t *testing.T) *pgStore {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnvVar)
	if dsn == "" {
		dsn = DefaultDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := NewPostgres(ctx, Config{DSN: dsn})
	if err != nil {
		t.Skipf("skipping: no reachable postgres at %s (start one with `make up && make migrate`): %v", dsn, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.(*pgStore)
}

// newTestRepo creates a uniquely-identified Repo (and registers cleanup) so
// concurrent/parallel test runs against the same database don't collide.
func newTestRepo(t *testing.T, s *pgStore, ctx context.Context) *model.Repo {
	t.Helper()
	repo := &model.Repo{
		Root:      fmt.Sprintf("/test/%s/%d", t.Name(), time.Now().UnixNano()),
		CommitSHA: fmt.Sprintf("%040x", time.Now().UnixNano()),
	}
	if _, err := s.CreateRepo(ctx, repo); err != nil {
		t.Fatalf("CreateRepo() error = %v", err)
	}
	t.Cleanup(func() {
		// repos has no Store.DeleteRepo; delete directly so test data
		// (and everything ON DELETE CASCADEd from it) doesn't linger.
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repo.ID)
	})
	return repo
}

func TestPostgresRoundTrip(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()

	repo := newTestRepo(t, s, ctx)

	got, err := s.GetRepoByCommit(ctx, repo.Root, repo.CommitSHA)
	if err != nil {
		t.Fatalf("GetRepoByCommit() error = %v", err)
	}
	if got.ID != repo.ID || got.Root != repo.Root || got.CommitSHA != repo.CommitSHA {
		t.Errorf("GetRepoByCommit() = %+v, want match of %+v", got, repo)
	}

	if _, err := s.GetRepoByCommit(ctx, "/does/not/exist", "0000000000000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRepoByCommit() for missing repo err = %v, want ErrNotFound", err)
	}

	file := &model.File{
		RepoID:      repo.ID,
		Path:        "pkg/mod.py",
		Language:    "python",
		ContentHash: "abc123",
		ModuleName:  "pkg.mod",
	}
	if err := s.UpsertFiles(ctx, []*model.File{file}); err != nil {
		t.Fatalf("UpsertFiles() error = %v", err)
	}
	if file.ID == 0 {
		t.Fatalf("UpsertFiles() did not backfill file.ID")
	}

	// Re-upserting the same (repo_id, path) updates in place rather than
	// duplicating.
	file.ContentHash = "def456"
	if err := s.UpsertFiles(ctx, []*model.File{file}); err != nil {
		t.Fatalf("UpsertFiles() (update) error = %v", err)
	}

	files, err := s.ListFiles(ctx, repo.ID)
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if len(files) != 1 || files[0].ContentHash != "def456" {
		t.Errorf("ListFiles() = %+v, want single file with updated content hash", files)
	}

	moduleSym := &model.Symbol{
		FileID:        file.ID,
		Kind:          model.SymbolKindModule,
		Name:          "mod",
		QualifiedName: "pkg.mod",
		StartLine:     1,
		EndLine:       10,
	}
	classSym := &model.Symbol{
		FileID:        file.ID,
		Kind:          model.SymbolKindClass,
		Name:          "Widget",
		QualifiedName: "pkg.mod.Widget",
		StartLine:     3,
		EndLine:       8,
		Signature:     "class Widget:",
	}
	if err := s.InsertSymbols(ctx, []*model.Symbol{moduleSym, classSym}); err != nil {
		t.Fatalf("InsertSymbols() error = %v", err)
	}
	if moduleSym.ID == 0 || classSym.ID == 0 || moduleSym.ID == classSym.ID {
		t.Fatalf("InsertSymbols() did not backfill distinct IDs: module=%d class=%d", moduleSym.ID, classSym.ID)
	}

	methodSym := &model.Symbol{
		FileID:        file.ID,
		Kind:          model.SymbolKindMethod,
		Name:          "greet",
		QualifiedName: "pkg.mod.Widget.greet",
		ParentID:      &classSym.ID,
		StartLine:     4,
		EndLine:       5,
		Signature:     "def greet(self):",
		Docstring:     "Say hello.",
	}
	if err := s.InsertSymbols(ctx, []*model.Symbol{methodSym}); err != nil {
		t.Fatalf("InsertSymbols() (method) error = %v", err)
	}

	got2, err := s.FindSymbolByQualifiedName(ctx, repo.ID, "pkg.mod.Widget.greet")
	if err != nil {
		t.Fatalf("FindSymbolByQualifiedName() error = %v", err)
	}
	if got2.ID != methodSym.ID || got2.ParentID == nil || *got2.ParentID != classSym.ID || got2.Docstring != "Say hello." {
		t.Errorf("FindSymbolByQualifiedName() = %+v, want match of %+v", got2, methodSym)
	}

	if _, err := s.FindSymbolByQualifiedName(ctx, repo.ID, "pkg.mod.NoSuchThing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSymbolByQualifiedName() for missing symbol err = %v, want ErrNotFound", err)
	}

	byName, err := s.FindSymbolsByName(ctx, repo.ID, "greet")
	if err != nil {
		t.Fatalf("FindSymbolsByName() error = %v", err)
	}
	if len(byName) != 1 || byName[0].ID != methodSym.ID {
		t.Errorf("FindSymbolsByName() = %+v, want single match on methodSym", byName)
	}

	edge := &model.Edge{
		SrcSymbolID: moduleSym.ID,
		DstSymbolID: classSym.ID,
		Kind:        model.EdgeKindCalls,
		Confidence:  model.ConfidenceExact,
	}
	if err := s.InsertEdges(ctx, []*model.Edge{edge}); err != nil {
		t.Fatalf("InsertEdges() error = %v", err)
	}
	if edge.ID == 0 {
		t.Fatalf("InsertEdges() did not backfill edge.ID")
	}

	callees, err := s.GetCallees(ctx, moduleSym.ID)
	if err != nil {
		t.Fatalf("GetCallees() error = %v", err)
	}
	if len(callees) != 1 || callees[0].ID != edge.ID {
		t.Errorf("GetCallees() = %+v, want single edge %+v", callees, edge)
	}

	callers, err := s.GetCallers(ctx, classSym.ID)
	if err != nil {
		t.Fatalf("GetCallers() error = %v", err)
	}
	if len(callers) != 1 || callers[0].ID != edge.ID {
		t.Errorf("GetCallers() = %+v, want single edge %+v", callers, edge)
	}

	ref := &model.UnresolvedRef{
		SrcSymbolID: methodSym.ID,
		Name:        "requests.get",
		Kind:        model.EdgeKindCalls,
	}
	if err := s.InsertUnresolvedRefs(ctx, []*model.UnresolvedRef{ref}); err != nil {
		t.Fatalf("InsertUnresolvedRefs() error = %v", err)
	}
	if ref.ID == 0 {
		t.Fatalf("InsertUnresolvedRefs() did not backfill ref.ID")
	}

	// A chunk with no embedding yet ("insert text first, embed later").
	textChunk := &model.Chunk{
		SymbolID:      &methodSym.ID,
		FileID:        file.ID,
		Text:          "def greet(self):\n    return 'hi'",
		ContextHeader: "pkg/mod.py Widget.greet",
		TokenCount:    8,
	}
	if err := s.InsertChunks(ctx, []*model.Chunk{textChunk}); err != nil {
		t.Fatalf("InsertChunks() (no embedding) error = %v", err)
	}
	if textChunk.ID == 0 {
		t.Fatalf("InsertChunks() did not backfill chunk.ID")
	}

	embedded := make([]float32, s.embeddingDim)
	for i := range embedded {
		embedded[i] = 1
	}
	embedChunk := &model.Chunk{
		SymbolID:      &classSym.ID,
		FileID:        file.ID,
		Text:          "class Widget:",
		ContextHeader: "pkg/mod.py Widget",
		TokenCount:    3,
		Embedding:     embedded,
	}
	if err := s.InsertChunks(ctx, []*model.Chunk{embedChunk}); err != nil {
		t.Fatalf("InsertChunks() (with embedding) error = %v", err)
	}

	results, err := s.VectorSearch(ctx, embedded, 5)
	if err != nil {
		t.Fatalf("VectorSearch() error = %v", err)
	}
	if len(results) == 0 || results[0].ID != embedChunk.ID {
		t.Fatalf("VectorSearch() = %+v, want embedChunk (%d) first", results, embedChunk.ID)
	}
	for _, c := range results {
		if c.SymbolID == nil {
			continue
		}
		if *c.SymbolID == *textChunk.SymbolID && c.ID == textChunk.ID {
			t.Errorf("VectorSearch() returned chunk %d, which has no embedding", c.ID)
		}
	}
}

func TestInsertChunksRejectsWrongEmbeddingDim(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()
	repo := newTestRepo(t, s, ctx)

	file := &model.File{RepoID: repo.ID, Path: "a.py", Language: "python", ContentHash: "h"}
	if err := s.UpsertFiles(ctx, []*model.File{file}); err != nil {
		t.Fatalf("UpsertFiles() error = %v", err)
	}

	bad := &model.Chunk{
		FileID:     file.ID,
		Text:       "x = 1",
		TokenCount: 1,
		Embedding:  make([]float32, s.embeddingDim+1),
	}
	if err := s.InsertChunks(ctx, []*model.Chunk{bad}); err == nil {
		t.Fatal("InsertChunks() with wrong embedding dimension: want error, got nil")
	}
}

func TestVectorSearchRejectsWrongQueryDim(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()

	if _, err := s.VectorSearch(ctx, make([]float32, s.embeddingDim+1), 5); err == nil {
		t.Fatal("VectorSearch() with wrong query dimension: want error, got nil")
	}
}

// TestUpsertFilesIsTransactional verifies UpsertFiles either applies every
// file in a batch or none: a batch with one valid file and one file that
// violates a foreign key must leave neither committed.
func TestUpsertFilesIsTransactional(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()
	repo := newTestRepo(t, s, ctx)

	valid := &model.File{RepoID: repo.ID, Path: "ok.py", Language: "python", ContentHash: "h1"}
	invalid := &model.File{RepoID: -1, Path: "bad.py", Language: "python", ContentHash: "h2"} // no such repo_id: FK violation

	err := s.UpsertFiles(ctx, []*model.File{valid, invalid})
	if err == nil {
		t.Fatal("UpsertFiles() with an FK-violating row: want error, got nil")
	}

	files, err := s.ListFiles(ctx, repo.ID)
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ListFiles() after failed transactional UpsertFiles() = %+v, want none committed", files)
	}
}

// TestInsertSymbolsIsAtomic verifies that a CopyFrom-backed bulk insert is
// all-or-nothing: a batch containing one row that violates a constraint
// (here, a bogus file_id) must leave none of the batch's rows committed.
func TestInsertSymbolsIsAtomic(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()
	repo := newTestRepo(t, s, ctx)

	file := &model.File{RepoID: repo.ID, Path: "atomic.py", Language: "python", ContentHash: "h"}
	if err := s.UpsertFiles(ctx, []*model.File{file}); err != nil {
		t.Fatalf("UpsertFiles() error = %v", err)
	}

	good := &model.Symbol{FileID: file.ID, Kind: model.SymbolKindFunction, Name: "f", QualifiedName: "atomic.f", StartLine: 1, EndLine: 1}
	bad := &model.Symbol{FileID: -1, Kind: model.SymbolKindFunction, Name: "g", QualifiedName: "atomic.g", StartLine: 1, EndLine: 1}

	if err := s.InsertSymbols(ctx, []*model.Symbol{good, bad}); err == nil {
		t.Fatal("InsertSymbols() with an FK-violating row: want error, got nil")
	}

	if _, err := s.FindSymbolByQualifiedName(ctx, repo.ID, "atomic.f"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSymbolByQualifiedName(%q) after failed atomic InsertSymbols() err = %v, want ErrNotFound", "atomic.f", err)
	}
}

// TestHNSWRecallVsExactScan builds a small sample of random vectors, runs
// VectorSearch (which goes through the HNSW index per migration
// 00007_add_chunks_embedding.go), and compares its top-K against an
// exact, brute-force cosine-distance scan computed in Go. Per plan.md
// ("Days 10-11: embeddings and ANN"), recall must be verified against an
// exact scan on a small sample.
func TestHNSWRecallVsExactScan(t *testing.T) {
	s := connectOrSkip(t)
	ctx := context.Background()
	repo := newTestRepo(t, s, ctx)

	file := &model.File{RepoID: repo.ID, Path: "vectors.py", Language: "python", ContentHash: "h"}
	if err := s.UpsertFiles(ctx, []*model.File{file}); err != nil {
		t.Fatalf("UpsertFiles() error = %v", err)
	}

	const sampleSize = 200
	const k = 10

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, sampleSize)
	chunks := make([]*model.Chunk, sampleSize)
	for i := 0; i < sampleSize; i++ {
		v := randomUnitVector(rng, s.embeddingDim)
		vectors[i] = v
		chunks[i] = &model.Chunk{
			FileID:     file.ID,
			Text:       fmt.Sprintf("sample %d", i),
			TokenCount: 2,
			Embedding:  v,
		}
	}
	if err := s.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("InsertChunks() error = %v", err)
	}

	query := randomUnitVector(rng, s.embeddingDim)

	approx, err := s.VectorSearch(ctx, query, k)
	if err != nil {
		t.Fatalf("VectorSearch() error = %v", err)
	}

	type scored struct {
		id       int64
		distance float64
	}
	exactAll := make([]scored, sampleSize)
	for i, v := range vectors {
		exactAll[i] = scored{id: chunks[i].ID, distance: cosineDistance(query, v)}
	}
	sort.Slice(exactAll, func(i, j int) bool { return exactAll[i].distance < exactAll[j].distance })

	exactTopK := make(map[int64]bool, k)
	for _, s := range exactAll[:k] {
		exactTopK[s.id] = true
	}

	hits := 0
	for _, c := range approx {
		if exactTopK[c.ID] {
			hits++
		}
	}
	recall := float64(hits) / float64(k)
	if recall < 0.8 {
		t.Errorf("HNSW recall@%d vs exact scan = %.2f, want >= 0.80", k, recall)
	}
	t.Logf("HNSW recall@%d vs exact scan on %d vectors: %.2f", k, sampleSize, recall)
}

func randomUnitVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var norm float64
	for i := range v {
		x := rng.NormFloat64()
		v[i] = float32(x)
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

func cosineDistance(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb))
}
