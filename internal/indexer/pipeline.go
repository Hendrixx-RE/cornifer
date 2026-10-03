package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/chunk"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
	"github.com/Hendrixx-RE/cornifer/internal/resolve"
	"github.com/Hendrixx-RE/cornifer/internal/store"
	"github.com/Hendrixx-RE/cornifer/internal/symbols"
	"github.com/Hendrixx-RE/cornifer/internal/walker"
)

// Index runs the full indexing pipeline against cfg.RepoRoot and persists
// its output to st (Postgres) plus a local BM25 index under cfg.CacheDir.
//
// If root+commitSHA was already indexed (store.GetRepoByCommit finds it),
// Index reuses that Repo row and first deletes its existing files — which,
// per files.doc "ON DELETE CASCADE", cascades to their symbols, edges,
// unresolved_refs, and chunks — so re-running index (or `reindex --force`)
// against an unchanged commit is a clean full rebuild rather than a unique-
// constraint error or duplicated rows. IncrementalIndex is the content-hash
// path for an existing snapshot; a genuinely new commit still gets its own
// fresh Repo row to preserve snapshot attribution.
func Index(ctx context.Context, st store.Store, cfg Config) (*Stats, error) {
	stats := newStats()
	doneTotal := stats.track("total")
	defer doneTotal()

	absRoot, err := absPath(cfg.RepoRoot)
	if err != nil {
		return nil, err
	}
	commitSHA, err := resolveCommitSHA(absRoot)
	if err != nil {
		return nil, fmt.Errorf("indexer: resolve commit sha: %w", err)
	}

	embedCfg := resolvedEmbedConfig(cfg.Embedder)
	if err := requireSidecarModel(embedCfg); err != nil {
		return nil, err
	}
	provider, embeddingModel := embeddingProvenance(embedCfg)
	repoID, err := getOrCreateCleanRepo(ctx, st, absRoot, commitSHA, provider, embeddingModel)
	if err != nil {
		return nil, err
	}
	stats.RepoID = repoID
	stats.Root = absRoot
	stats.CommitSHA = commitSHA
	cfg.log("indexing %s @ %s as repo %d", absRoot, commitSHA, repoID)

	// Walk

	doneWalk := stats.track("walk")
	walked, err := walker.Walk(ctx, absRoot)
	if cfg.IncludeText {
		walked, err = walker.WalkWithText(ctx, absRoot)
	}
	doneWalk()
	if err != nil {
		return nil, fmt.Errorf("indexer: walk: %w", err)
	}
	stats.Files = len(walked)
	cfg.log("walked %d indexable file(s)", len(walked))

	files := make([]*model.File, len(walked))
	for i := range walked {
		f := walked[i].File
		f.RepoID = repoID
		files[i] = &f
	}
	if err := st.UpsertFiles(ctx, files); err != nil {
		return nil, fmt.Errorf("indexer: upsert files: %w", err)
	}

	// Parse + extract symbols

	doneParse := stats.track("parse+symbols")
	parser := parse.New()
	results := make([]*parse.Result, len(files))
	perFile := make([][]*model.Symbol, len(files))
	ok := make([]bool, len(files)) // false if the file could not be parsed at all
	chunkable := make([]bool, len(files))

	for i, f := range files {
		chunkable[i] = true
		if f.Language != "python" {
			// Generic text/source files are lexical-only. Do not run the Python
			// extractor/resolver or imply graph coverage for them.
			continue
		}
		res, err := parser.Parse(ctx, f.Path, walked[i].Content)
		if err != nil {
			cfg.log("warning: %s: parse failed, skipping: %v", f.Path, err)
			continue
		}
		if len(res.Errors) > 0 {
			stats.ParseErrors += len(res.Errors)
		}
		results[i] = res
		perFile[i] = symbols.Extract(res, f.ID, f.ModuleName)
		ok[i] = true
	}
	doneParse()

	// Remap temporary symbol IDs to repo-unique ones and insert them.

	doneStoreSyms := stats.track("store symbols")
	var symsToRemap [][]*model.Symbol
	for i, isOK := range ok {
		if isOK {
			symsToRemap = append(symsToRemap, perFile[i])
		}
	}
	if err := remapAndInsertSymbols(ctx, st, symsToRemap); err != nil {
		closeResults(results)
		return nil, err
	}
	doneStoreSyms()

	for _, syms := range perFile {
		stats.Symbols += len(syms)
	}

	// Resolve imports/calls/inheritance into edges.

	doneResolve := stats.track("resolve")
	var inputs []resolve.FileInput
	for i, canChunk := range chunkable {
		if !canChunk {
			continue
		}
		inputs = append(inputs, resolve.FileInput{File: files[i], Symbols: perFile[i], Result: results[i]})
	}
	resolved, err := resolve.Resolve(inputs)
	doneResolve()
	if err != nil {
		closeResults(results)
		return nil, fmt.Errorf("indexer: resolve: %w", err)
	}
	stats.Edges = len(resolved.Edges)
	stats.UnresolvedRefs = len(resolved.UnresolvedRefs)
	stats.Resolve = resolved.Stats
	cfg.log("resolved %d edge(s), %d unresolved reference(s)", len(resolved.Edges), len(resolved.UnresolvedRefs))
	cfg.log("%s", resolved.Stats.String())

	doneStoreEdges := stats.track("store edges")
	if err := st.InsertEdges(ctx, resolved.Edges); err != nil {
		closeResults(results)
		return nil, fmt.Errorf("indexer: insert edges: %w", err)
	}
	if err := st.InsertUnresolvedRefs(ctx, resolved.UnresolvedRefs); err != nil {
		closeResults(results)
		return nil, fmt.Errorf("indexer: insert unresolved refs: %w", err)
	}
	doneStoreEdges()

	closeResults(results)

	// Chunk

	doneChunk := stats.track("chunk")
	chunker := chunk.NewWithOptions(cfg.ChunkOptions)
	var allChunks []*model.Chunk
	for i, isOK := range ok {
		if !isOK {
			continue
		}
		cs, err := chunker.Chunk(ctx, files[i], walked[i].Content, perFile[i])
		if err != nil {
			cfg.log("warning: %s: chunk failed, skipping file: %v", files[i].Path, err)
			continue
		}
		allChunks = append(allChunks, cs...)
	}
	doneChunk()
	stats.Chunks = len(allChunks)
	cfg.log("built %d chunk(s)", len(allChunks))

	// Embed

	embedder, err := BuildEmbedder(embedCfg)
	if err != nil {
		return nil, fmt.Errorf("indexer: build embedder: %w", err)
	}

	doneEmbed := stats.track("embed")
	symbolByID := make(map[int64]*model.Symbol)
	for _, syms := range perFile {
		for _, s := range syms {
			symbolByID[s.ID] = s
		}
	}
	fileByID := make(map[int64]*model.File)
	for _, f := range files {
		fileByID[f.ID] = f
	}

	maxTok := cfg.maxEmbedTokens()
	texts := make([]string, len(allChunks))
	for i, c := range allChunks {
		text := chunk.EmbeddingText(c)
		if n := chunk.CountTokens(text); n > maxTok {
			stats.OversizedTruncated++
			text = truncateToTokens(text, maxTok)
			cfg.log("warning: %s: chunk %s exceeds %d tokens (%d), truncating for embedding",
				chunkLocation(c, fileByID, symbolByID), describeChunk(c, symbolByID), maxTok, n)
		}
		texts[i] = text
	}

	if len(texts) > 0 {
		vectors, err := embedder.Embed(ctx, texts)
		if err != nil {
			doneEmbed()
			return nil, fmt.Errorf("indexer: embed chunks: %w", err)
		}
		if len(vectors) != len(allChunks) {
			doneEmbed()
			return nil, fmt.Errorf("indexer: embedder returned %d vectors for %d chunks", len(vectors), len(allChunks))
		}
		for i, c := range allChunks {
			c.Embedding = vectors[i]
		}
		stats.Embedded = len(allChunks)
	}
	doneEmbed()

	// Store chunks

	doneStoreChunks := stats.track("store chunks")
	if err := st.InsertChunks(ctx, allChunks); err != nil {
		return nil, fmt.Errorf("indexer: insert chunks: %w", err)
	}
	doneStoreChunks()

	// BM25

	doneBM25 := stats.track("bm25")
	cacheDir, err := resolveCacheDir(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("indexer: create cache dir %s: %w", cacheDir, err)
	}
	bmIdx := bm25.NewIndex(bm25.DefaultK1, bm25.DefaultB)
	if err := bmIdx.Index(ctx, allChunks); err != nil {
		return nil, fmt.Errorf("indexer: build bm25 index: %w", err)
	}
	if err := bmIdx.Save(bm25Path(cacheDir, repoID)); err != nil {
		return nil, fmt.Errorf("indexer: save bm25 index: %w", err)
	}
	doneBM25()

	return stats, nil
}

// getOrCreateCleanRepo returns a Repo ID for root+commitSHA with no
// existing files: a freshly created row, or an existing one wiped clean of
// its prior files (see Index's doc comment).
func getOrCreateCleanRepo(ctx context.Context, st store.Store, root, commitSHA, provider, embeddingModel string) (int64, error) {
	existing, err := st.GetRepoByCommit(ctx, root, commitSHA)
	if errors.Is(err, store.ErrNotFound) {
		repo := &model.Repo{Root: root, CommitSHA: commitSHA, EmbeddingProvider: provider, EmbeddingModel: embeddingModel}
		repoID, err := st.CreateRepo(ctx, repo)
		if err != nil {
			return 0, fmt.Errorf("indexer: create repo: %w", err)
		}
		return repoID, nil
	}
	if err != nil {
		return 0, fmt.Errorf("indexer: look up existing repo: %w", err)
	}
	if err := st.UpdateRepoEmbeddingProvenance(ctx, existing.ID, provider, embeddingModel); err != nil {
		return 0, err
	}

	oldFiles, err := st.ListFiles(ctx, existing.ID)
	if err != nil {
		return 0, fmt.Errorf("indexer: list existing files: %w", err)
	}
	for _, f := range oldFiles {
		if err := st.DeleteFile(ctx, f.ID); err != nil {
			return 0, fmt.Errorf("indexer: delete existing file %q: %w", f.Path, err)
		}
	}
	return existing.ID, nil
}

func closeResults(results []*parse.Result) {
	for _, r := range results {
		if r != nil && r.Tree != nil {
			r.Tree.Close()
		}
	}
}

// AbsRepoRoot is the exported form of absPath, for callers (cmd/cornifer's
// reindex command) that need to resolve a repo root the same way Index
// does before deciding whether to run the pipeline.
func AbsRepoRoot(root string) (string, error) {
	return absPath(root)
}

func absPath(root string) (string, error) {
	if root == "" {
		return "", errors.New("indexer: repo root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("indexer: resolve repo root %q: %w", root, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("indexer: repo root %q: %w", abs, err)
	}
	return abs, nil
}

// BuildEmbedder resolves cfg's provider default (ProviderVoyage when
// VOYAGE_API_KEY is set, else ProviderFake — plan.md: "Embedder selectable
// via flag/env; default to the deterministic fake when no VOYAGE_API_KEY so
// it runs anywhere") and builds the Embedder. Exported so every command
// that needs an Embedder (index, reindex, query) resolves the default the
// same way.
func BuildEmbedder(cfg embed.Config) (embed.Embedder, error) {
	return embed.New(resolvedEmbedConfig(cfg))
}

func resolvedEmbedConfig(cfg embed.Config) embed.Config {
	if cfg.Provider == "" {
		if os.Getenv(embed.VoyageAPIKeyEnvVar) != "" {
			cfg.Provider = embed.ProviderVoyage
		} else {
			cfg.Provider = embed.ProviderFake
		}
	}
	return embed.ConfigFromEnvironment(cfg)
}

// requireSidecarModel prevents a mutable endpoint address from being recorded
// as though it were a reproducible embedding-model identity. The low-level
// embed package still permits an anonymous sidecar for library callers and
// tests; indexing is the durable provenance boundary and must be stricter.
func requireSidecarModel(cfg embed.Config) error {
	if cfg.Provider == embed.ProviderSidecar && cfg.Sidecar.Model == "" {
		return fmt.Errorf("indexer: sidecar indexing requires %s to name the model and revision", embed.SidecarModelEnvVar)
	}
	return nil
}

func embeddingProvenance(cfg embed.Config) (provider, model string) {
	switch cfg.Provider {
	case embed.ProviderVoyage:
		return string(cfg.Provider), firstNonEmpty(cfg.Voyage.Model, embed.DefaultVoyageModel) + ";input_type=document"
	case embed.ProviderSidecar:
		return string(cfg.Provider), firstNonEmpty(cfg.Sidecar.Model, cfg.Sidecar.Endpoint, "unknown")
	case embed.ProviderFake:
		return string(cfg.Provider), "deterministic-hash-derived"
	default:
		return "unknown", "unknown"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// truncateToTokens cuts s down to approximately maxTok tokens by
// chunk.CountTokens' estimate (~4 chars/token for identifier runs), on a
// rune boundary, for use as embedding input only — callers must not use
// the result as the chunk's stored Text.
func truncateToTokens(s string, maxTok int) string {
	budget := maxTok * 4
	if len(s) <= budget {
		return s
	}
	// Walk back to a valid rune boundary.
	for budget > 0 && !utf8.RuneStart(s[budget]) {
		budget--
	}
	return s[:budget]
}

func chunkLocation(c *model.Chunk, files map[int64]*model.File, _ map[int64]*model.Symbol) string {
	if f, ok := files[c.FileID]; ok {
		return fmt.Sprintf("%s:%d-%d", f.Path, c.StartLine, c.EndLine)
	}
	return fmt.Sprintf("file#%d:%d-%d", c.FileID, c.StartLine, c.EndLine)
}

func describeChunk(c *model.Chunk, symbols map[int64]*model.Symbol) string {
	if c.SymbolID == nil {
		return "module-level code"
	}
	if s, ok := symbols[*c.SymbolID]; ok {
		return s.QualifiedName
	}
	return fmt.Sprintf("symbol#%d", *c.SymbolID)
}
