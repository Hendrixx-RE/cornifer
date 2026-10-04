package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Hendrixx-RE/cornifer/internal/indexer"
	"github.com/Hendrixx-RE/cornifer/internal/model"
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
				citation.SymbolID = fmt.Sprintf("s:%d", sym.ID)
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
	pack.Symbols = contextSymbols(session, pack.Evidence, pack.Relationships)
	return pack, nil
}

func trimEvidence(text string, budget int) (string, bool) {
	if len(text) <= budget {
		return text, false
	}
	if budget < 32 {
		return "", true
	}
	end := budget - len("…")
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…", true
}
func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func evidenceRelationships(session *indexer.Session, evidence []Citation, depth int) []Relationship {
	if depth <= 0 {
		depth = 1
	}
	bySymbol := evidenceSymbolIDs(session, evidence)
	var out []Relationship
	for _, edge := range session.Manifest.Edges {
		from, fromOK := session.Symbol(edge.SrcSymbolID)
		to, toOK := session.Symbol(edge.DstSymbolID)
		if !fromOK || !toOK {
			continue
		}
		fromSymbolID, toSymbolID := fmt.Sprintf("s:%d", from.ID), fmt.Sprintf("s:%d", to.ID)
		fromID, toID := bySymbol[fromSymbolID], bySymbol[toSymbolID]
		if fromID == "" && toID == "" {
			continue
		}
		out = append(out, Relationship{FromSymbolID: fromSymbolID, ToSymbolID: toSymbolID, FromCitationID: fromID, ToCitationID: toID, FromSymbol: from.QualifiedName, ToSymbol: to.QualifiedName, Kind: string(edge.Kind), Confidence: float64(edge.Confidence), Depth: 1})
		if len(out) == 32 {
			break
		}
	}
	return out
}

func contextSymbols(session *indexer.Session, evidence []Citation, relationships []Relationship) []ContextSymbol {
	wanted, direct := map[string]bool{}, map[string]bool{}
	for id := range evidenceSymbolIDs(session, evidence) {
		wanted[id], direct[id] = true, true
	}
	for _, r := range relationships {
		wanted[r.FromSymbolID], wanted[r.ToSymbolID] = true, true
	}
	out := []ContextSymbol{}
	for _, sym := range session.Manifest.Symbols {
		id := fmt.Sprintf("s:%d", sym.ID)
		if !wanted[id] {
			continue
		}
		file, ok := session.File(sym.FileID)
		if !ok {
			continue
		}
		out = append(out, ContextSymbol{ID: id, Name: sym.Name, QualifiedName: sym.QualifiedName, Kind: string(sym.Kind), Path: file.Path, StartLine: sym.StartLine, EndLine: sym.EndLine, Signature: sym.Signature, Evidence: direct[id]})
	}
	return out
}

// A merged/class chunk can contain multiple exact definitions. Include their
// metadata and immediate edges, not just the chunk's first owning symbol.
// At most 64 direct definitions plus the bounded relationship endpoints enter
// a pack. Generic files never gain structural metadata from matching words.
func evidenceSymbolIDs(session *indexer.Session, evidence []Citation) map[string]string {
	out := map[string]string{}
	for _, citation := range evidence {
		if citation.SymbolID != "" {
			out[citation.SymbolID] = citation.ID
		}
	}
	for _, sym := range session.Manifest.Symbols {
		if len(out) >= 64 {
			break
		}
		if sym.Kind != model.SymbolKindClass && sym.Kind != model.SymbolKindFunction && sym.Kind != model.SymbolKindMethod {
			continue
		}
		file, ok := session.File(sym.FileID)
		if !ok {
			continue
		}
		for _, citation := range evidence {
			end := citation.EndLine
			if citation.Truncated {
				end = citation.StartLine + strings.Count(citation.Snippet, "\n") - 1
			}
			if file.Path == citation.Path && sym.StartLine >= citation.StartLine && sym.EndLine <= end {
				out[fmt.Sprintf("s:%d", sym.ID)] = citation.ID
				break
			}
		}
	}
	return out
}
