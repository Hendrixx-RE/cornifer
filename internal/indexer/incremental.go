package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"

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

// IncrementalIndex applies a content-hash diff to an already-indexed
// root+commit snapshot. Added and changed files are re-parsed, re-chunked,
// and re-embedded; deleted files are removed. Resolution is deliberately
// recomputed across every current parse tree because a changed export or
// signature can alter cross-file call/import edges. BM25 is rebuilt from the
// authoritative chunk rows, keeping lexical and vector state consistent.
//
// A new git commit still creates a separate immutable Repo snapshot through
// Index. IncrementalIndex is the fast path for an existing snapshot (most
// commonly an uncommitted working-tree edit or a forced same-commit refresh).
func IncrementalIndex(ctx context.Context, st store.Store, cfg Config) (*Stats, error) {
	stats := newStats()
	doneTotal := stats.track("total")
	defer doneTotal()

	root, err := absPath(cfg.RepoRoot)
	if err != nil {
		return nil, err
	}
	commitSHA, err := resolveCommitSHA(root)
	if err != nil {
		return nil, fmt.Errorf("indexer: resolve commit sha: %w", err)
	}
	repo, err := st.GetRepoByCommit(ctx, root, commitSHA)
	if errors.Is(err, store.ErrNotFound) {
		return Index(ctx, st, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("indexer: get existing repo: %w", err)
	}
	stats.RepoID, stats.Root, stats.CommitSHA = repo.ID, root, commitSHA

	embedCfg := resolvedEmbedConfig(cfg.Embedder)
	if err := requireSidecarModel(embedCfg); err != nil {
		return nil, err
	}
	provider, embeddingModel := embeddingProvenance(embedCfg)
	if repo.EmbeddingProvider != "" && repo.EmbeddingProvider != "unknown" && repo.EmbeddingProvider != provider {
		return nil, fmt.Errorf("indexer: existing repo uses embedding provider %q, requested %q; use `cornifer index` for a clean re-embed", repo.EmbeddingProvider, provider)
	}
	if repo.EmbeddingModel != "" && repo.EmbeddingModel != "unknown" && repo.EmbeddingModel != embeddingModel {
		return nil, fmt.Errorf("indexer: existing repo uses embedding model %q, requested %q; use `cornifer index` for a clean re-embed", repo.EmbeddingModel, embeddingModel)
	}
	if err := st.UpdateRepoEmbeddingProvenance(ctx, repo.ID, provider, embeddingModel); err != nil {
		return nil, err
	}

	oldFiles, err := st.ListFiles(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: list existing files: %w", err)
	}
	oldByPath := make(map[string]*model.File, len(oldFiles))
	for _, file := range oldFiles {
		oldByPath[file.Path] = file
	}

	doneWalk := stats.track("walk")
	walked, err := walker.Walk(ctx, root)
	doneWalk()
	if err != nil {
		return nil, fmt.Errorf("indexer: walk: %w", err)
	}
	stats.Files = len(walked)
	current := make(map[string]bool, len(walked))
	changed := make(map[string]bool)
	var upsert []*model.File
	for i := range walked {
		file := &walked[i].File
		current[file.Path] = true
		old := oldByPath[file.Path]
		if old == nil || old.ContentHash != file.ContentHash {
			changed[file.Path] = true
			file.RepoID = repo.ID
			upsert = append(upsert, file)
		}
	}
	var deleted []*model.File
	for path, file := range oldByPath {
		if !current[path] {
			deleted = append(deleted, file)
		}
	}
	if len(changed) == 0 && len(deleted) == 0 {
		cfg.log("incremental reindex: no content-hash changes for %s @ %s", root, commitSHA)
		return stats, nil
	}
	cfg.log("incremental reindex: %d added/changed, %d deleted file(s)", len(changed), len(deleted))

	for _, file := range deleted {
		if err := st.DeleteFile(ctx, file.ID); err != nil {
			return nil, fmt.Errorf("indexer: delete %s: %w", file.Path, err)
		}
	}
	for path := range changed {
		if old := oldByPath[path]; old != nil {
			if err := st.DeleteFileContents(ctx, old.ID); err != nil {
				return nil, fmt.Errorf("indexer: clear changed %s: %w", path, err)
			}
		}
	}
	if err := st.UpsertFiles(ctx, upsert); err != nil {
		return nil, fmt.Errorf("indexer: upsert changed files: %w", err)
	}

	files, err := st.ListFiles(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: reload files: %w", err)
	}
	fileByPath := make(map[string]*model.File, len(files))
	for _, file := range files {
		fileByPath[file.Path] = file
	}
	existingSymbols, err := st.ListSymbols(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: reload symbols: %w", err)
	}
	symsByFile := make(map[int64][]*model.Symbol)
	for _, sym := range existingSymbols {
		symsByFile[sym.FileID] = append(symsByFile[sym.FileID], sym)
	}

	doneParse := stats.track("parse+symbols")
	parser := parse.New()
	results := make([]*parse.Result, len(walked))
	perFile := make([][]*model.Symbol, len(walked))
	ok := make([]bool, len(walked))
	var fresh [][]*model.Symbol
	for i := range walked {
		file := fileByPath[walked[i].Path]
		if file == nil {
			closeResults(results)
			return nil, fmt.Errorf("indexer: missing reloaded file %s", walked[i].Path)
		}
		result, err := parser.Parse(ctx, file.Path, walked[i].Content)
		if err != nil {
			cfg.log("warning: %s: parse failed, skipping: %v", file.Path, err)
			continue
		}
		if len(result.Errors) > 0 {
			stats.ParseErrors += len(result.Errors)
		}
		results[i], ok[i] = result, true
		if changed[file.Path] {
			perFile[i] = symbols.Extract(result, file.ID, file.ModuleName)
			fresh = append(fresh, perFile[i])
		} else {
			perFile[i] = symsByFile[file.ID]
		}
	}
	doneParse()
	defer closeResults(results)

	if err := remapAndInsertSymbols(ctx, st, fresh); err != nil {
		return nil, err
	}
	for _, syms := range perFile {
		stats.Symbols += len(syms)
	}

	doneResolve := stats.track("resolve")
	var inputs []resolve.FileInput
	for i, valid := range ok {
		if !valid {
			continue
		}
		file := fileByPath[walked[i].Path]
		if len(perFile[i]) == 0 {
			return nil, fmt.Errorf("indexer: %s has no symbols after incremental update", file.Path)
		}
		inputs = append(inputs, resolve.FileInput{File: file, Symbols: perFile[i], Result: results[i]})
	}
	resolved, err := resolve.Resolve(inputs)
	doneResolve()
	if err != nil {
		return nil, fmt.Errorf("indexer: resolve: %w", err)
	}
	stats.Edges, stats.UnresolvedRefs, stats.Resolve = len(resolved.Edges), len(resolved.UnresolvedRefs), resolved.Stats
	if err := st.DeleteEdgesForRepo(ctx, repo.ID); err != nil {
		return nil, err
	}
	if err := st.DeleteUnresolvedRefsForRepo(ctx, repo.ID); err != nil {
		return nil, err
	}
	if err := st.InsertEdges(ctx, resolved.Edges); err != nil {
		return nil, fmt.Errorf("indexer: insert edges: %w", err)
	}
	if err := st.InsertUnresolvedRefs(ctx, resolved.UnresolvedRefs); err != nil {
		return nil, fmt.Errorf("indexer: insert unresolved refs: %w", err)
	}

	doneChunk := stats.track("chunk")
	chunker := chunk.NewWithOptions(cfg.ChunkOptions)
	var changedChunks []*model.Chunk
	for i, valid := range ok {
		if !valid || !changed[walked[i].Path] {
			continue
		}
		file := fileByPath[walked[i].Path]
		chunks, err := chunker.Chunk(ctx, file, walked[i].Content, perFile[i])
		if err != nil {
			cfg.log("warning: %s: chunk failed, skipping: %v", file.Path, err)
			continue
		}
		changedChunks = append(changedChunks, chunks...)
	}
	doneChunk()
	if err := embedAndStore(ctx, st, changedChunks, embedCfg, cfg); err != nil {
		return nil, err
	}

	doneBM25 := stats.track("bm25")
	allChunks, err := st.ListChunks(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("indexer: list chunks for bm25: %w", err)
	}
	cacheDir, err := resolveCacheDir(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("indexer: create cache dir %s: %w", cacheDir, err)
	}
	idx := bm25.NewIndex(bm25.DefaultK1, bm25.DefaultB)
	if err := idx.Index(ctx, allChunks); err != nil {
		return nil, fmt.Errorf("indexer: rebuild bm25: %w", err)
	}
	if err := idx.Save(bm25Path(cacheDir, repo.ID)); err != nil {
		return nil, fmt.Errorf("indexer: save bm25: %w", err)
	}
	doneBM25()
	stats.Chunks, stats.Embedded = len(allChunks), len(changedChunks)
	return stats, nil
}

func embedAndStore(ctx context.Context, st store.Store, chunks []*model.Chunk, embedCfg embed.Config, cfg Config) error {
	if len(chunks) == 0 {
		return nil
	}
	embedder, err := BuildEmbedder(embedCfg)
	if err != nil {
		return fmt.Errorf("indexer: build embedder: %w", err)
	}
	maxTokens := cfg.maxEmbedTokens()
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		text := chunk.ContextHeader + "\n" + chunk.Text
		if n := chunk.TokenCount; n > maxTokens {
			text = truncateToTokens(text, maxTokens)
			cfg.log("warning: chunk %d exceeds %d tokens (%d), truncating for embedding", chunk.ID, maxTokens, n)
		}
		texts[i] = text
	}
	vectors, err := embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("indexer: embed changed chunks: %w", err)
	}
	if len(vectors) != len(chunks) {
		return fmt.Errorf("indexer: embedder returned %d vectors for %d chunks", len(vectors), len(chunks))
	}
	for i, chunk := range chunks {
		chunk.Embedding = vectors[i]
	}
	if err := st.InsertChunks(ctx, chunks); err != nil {
		return fmt.Errorf("indexer: insert changed chunks: %w", err)
	}
	return nil
}
