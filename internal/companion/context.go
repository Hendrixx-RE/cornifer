package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
	corestore "github.com/Hendrixx-RE/cornifer/internal/store"
)

// EngineContextBuilder is the deterministic evidence bridge shared by the
// browser and MCP. It never calls a generative model. Dense retrieval is
// enabled only when the configured hosted embedding provider is available;
// otherwise a previously indexed snapshot remains safely searchable by BM25.
type EngineContextBuilder struct {
	Engine corestore.Store
	Config RuntimeConfig
}

func (b EngineContextBuilder) Build(ctx context.Context, repo Repository, question string, opts ContextOptions) (ContextPack, error) {
	if strings.TrimSpace(question) == "" {
		return ContextPack{}, fmt.Errorf("question is required")
	}
	if b.Engine == nil {
		return ContextPack{}, fmt.Errorf("companion: engine store is not configured")
	}
	session, err := indexer.OpenSession(ctx, b.Engine, repo.CheckoutPath, repo.CacheDir)
	if err != nil {
		return ContextPack{}, err
	}
	limit := opts.EvidenceLimit
	if limit <= 0 {
		limit = DefaultEvidenceLimit
	}
	if limit > 20 {
		limit = 20
	}
	budget := opts.ContextBytes
	if budget <= 0 {
		budget = DefaultContextBytes
	}
	sparse, err := session.LoadBM25(repo.CacheDir)
	if err != nil {
		return ContextPack{}, err
	}
	result := &retrieve.Result{}
	used := []string{"bm25"}
	degraded := true
	if b.Config.configuredEmbedding() {
		embedCfg, err := b.Config.embedConfig(true, b.Config.DataDir+"/embeddings")
		if err != nil {
			return ContextPack{}, err
		}
		embedder, err := indexer.BuildEmbedder(embedCfg)
		if err != nil {
			return ContextPack{}, err
		}
		result, err = retrieve.NewHybridSearcher(sparse, session.VectorSearcher(), embedder, retrieve.Config{Boost: session.GraphBoost(retrieve.DefaultGraphBoostWeight)}).Search(ctx, question, limit)
		if err != nil {
			return ContextPack{}, err
		}
		used, degraded = []string{"bm25", "vector", "graph_adjacency_boost"}, len(result.Failed) > 0
	} else {
		lexical, err := sparse.Search(ctx, question, limit)
		if err != nil {
			return ContextPack{}, err
		}
		for i, hit := range lexical {
			result.Hits = append(result.Hits, retrieve.Hit{ChunkID: hit.ChunkID, Score: hit.Score, Sources: []retrieve.Source{{Retriever: retrieve.RetrieverBM25, Rank: i + 1, Score: hit.Score}}})
		}
	}

	var pack ContextPack
	pack.Status = "ok"
	pack.Repository.ID, pack.Repository.CanonicalURL, pack.Repository.CommitSHA, pack.Repository.Capabilities = repo.ID, repo.CanonicalURL, repo.ResolvedCommitSHA, repo.Capabilities
	pack.Query = question
	pack.Retrieval.SystemsUsed, pack.Retrieval.Degraded, pack.Retrieval.ContextByte = used, degraded, budget
	seen := map[int64]bool{}
	remaining := budget
	for _, hit := range result.Hits {
		if seen[hit.ChunkID] || remaining <= 0 {
			continue
		}
		meta, ok := session.Chunk(hit.ChunkID)
		if !ok {
			continue
		}
		file, ok := session.File(meta.FileID)
		if !ok {
			continue
		}
		text, truncated := trimEvidence(meta.Text, remaining)
		if text == "" {
			continue
		}
		citation := Citation{ID: fmt.Sprintf("e%d", len(pack.Evidence)+1), Path: file.Path, StartLine: meta.StartLine, EndLine: meta.EndLine, Snippet: text, ExcerptSHA: hashText(meta.Text), Truncated: truncated}
		if meta.SymbolID != nil {
			if sym, ok := session.Symbol(*meta.SymbolID); ok {
				citation.Symbol = sym.QualifiedName
			}
		}
		for _, source := range hit.Sources {
			citation.Sources = append(citation.Sources, fmt.Sprintf("%s#%d", source.Retriever, source.Rank))
		}
		pack.Evidence = append(pack.Evidence, citation)
		remaining -= len(text)
		seen[hit.ChunkID] = true
	}
	pack.Omitted.EvidenceCount = len(result.Hits) - len(pack.Evidence)
	if pack.Omitted.EvidenceCount > 0 {
		pack.Omitted.Reason = "context budget or citation metadata limit"
	}
	pack.Relationships = evidenceRelationships(session, pack.Evidence, opts.GraphDepth)
	return pack, nil
}

func trimEvidence(text string, budget int) (string, bool) {
	if len(text) <= budget {
		return text, false
	}
	if budget < 32 {
		return "", true
	}
	return text[:budget-1] + "…", true
}
func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func evidenceRelationships(session *indexer.Session, evidence []Citation, depth int) []Relationship {
	if depth <= 0 {
		depth = 1
	}
	bySymbol := make(map[string]string, len(evidence))
	for _, c := range evidence {
		if c.Symbol != "" {
			bySymbol[c.Symbol] = c.ID
		}
	}
	var out []Relationship
	for _, edge := range session.Manifest.Edges {
		from, fromOK := session.Symbol(edge.SrcSymbolID)
		to, toOK := session.Symbol(edge.DstSymbolID)
		if !fromOK || !toOK {
			continue
		}
		fromID, toID := bySymbol[from.QualifiedName], bySymbol[to.QualifiedName]
		if fromID == "" && toID == "" {
			continue
		}
		out = append(out, Relationship{FromCitationID: fromID, ToCitationID: toID, FromSymbol: from.QualifiedName, ToSymbol: to.QualifiedName, Kind: string(edge.Kind), Confidence: float64(edge.Confidence), Depth: 1})
		if len(out) == 32 {
			break
		}
	}
	return out
}
