// Command cornifer-mcp exposes Cornifer's index over MCP (stdio transport):
// search_code, find_definition, find_references, get_dependencies,
// get_call_graph, get_blast_radius and find_cycles (see plan.md "MCP
// server"). The handlers live in internal/mcp; this file only builds a
// backend and serves it.
//
// Configuration (environment):
//
//	CORNIFER_REPO_ID       repo id to serve (default 1)
//	CORNIFER_DATABASE_URL  Postgres DSN (default: docker-compose database)
//	CORNIFER_BM25_INDEX    path to a saved bm25 index; enables lexical search
//	VOYAGE_API_KEY         enables vector search (with CORNIFER_BM25_INDEX or alone)
//
// stdout carries the MCP protocol, so all logging goes to stderr.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/bm25"
	"github.com/Hendrixx-RE/cornifer/internal/embed"
	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/mcp"
	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/retrieve"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

const version = "0.1.0"

// newServer registers every tool on a fresh MCP server backed by deps. It
// takes interfaces only, so tests and other binaries can supply fakes.
func newServer(deps mcp.Deps) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "cornifer", Version: version}, nil)
	mcp.Register(server, deps)
	return server
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("cornifer-mcp: ")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	deps, closeFn, err := storeDeps(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFn()

	if err := newServer(deps).Run(ctx, &sdk.StdioTransport{}); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

// storeDeps wires the real backend. internal/store has no ID-based lookups
// or bulk edge listing yet, so those capabilities report a clear error
// (see storeCatalog) until the store grows them.
func storeDeps(ctx context.Context) (mcp.Deps, func(), error) {
	repoID := int64(1)
	if v := os.Getenv("CORNIFER_REPO_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return mcp.Deps{}, nil, fmt.Errorf("invalid CORNIFER_REPO_ID %q", v)
		}
		repoID = id
	}

	cfg, err := store.ConfigFromEnv()
	if err != nil {
		return mcp.Deps{}, nil, err
	}
	st, err := store.NewPostgres(ctx, cfg)
	if err != nil {
		return mcp.Deps{}, nil, fmt.Errorf("connect to store: %w", err)
	}

	cat := storeCatalog{st: st, repoID: repoID}
	deps := mcp.Deps{
		RepoID:  repoID,
		Symbols: st,
		Catalog: cat,
		Graphs:  &mcp.LazyGraph{Loader: graph.EdgeLoaderFunc(cat.loadEdges)},
	}

	var sparse bm25.SparseIndex
	if p := os.Getenv("CORNIFER_BM25_INDEX"); p != "" {
		idx, err := bm25.Load(p)
		if err != nil {
			st.Close()
			return mcp.Deps{}, nil, fmt.Errorf("load bm25 index: %w", err)
		}
		sparse = idx
	}
	var vector retrieve.VectorSearcher
	var emb embed.Embedder
	if os.Getenv(embed.VoyageAPIKeyEnvVar) != "" {
		if emb, err = embed.New(embed.Config{Provider: embed.ProviderVoyage}); err != nil {
			st.Close()
			return mcp.Deps{}, nil, err
		}
		vector = st
	}
	if sparse != nil || vector != nil {
		deps.Search = retrieve.NewHybridSearcher(sparse, vector, emb, retrieve.Config{})
	} else {
		log.Print("search_code disabled: set CORNIFER_BM25_INDEX and/or VOYAGE_API_KEY")
	}
	return deps, func() { st.Close() }, nil
}

var errStoreGap = fmt.Errorf("not supported by internal/store yet (needs ID lookup / bulk edge listing)")

// storeCatalog adapts store.Store to mcp.Catalog. Only files can be listed
// today; chunk and symbol ID lookups are store gaps.
type storeCatalog struct {
	st     store.Store
	repoID int64
}

func (c storeCatalog) GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error) {
	files, err := c.st.ListFiles(ctx, c.repoID)
	if err != nil {
		return nil, err
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[int64]*model.File, len(ids))
	for _, f := range files {
		if want[f.ID] {
			out[f.ID] = f
		}
	}
	return out, nil
}

func (storeCatalog) GetChunks(context.Context, []int64) (map[int64]*model.Chunk, error) {
	return nil, fmt.Errorf("chunk lookup: %w", errStoreGap)
}

func (storeCatalog) GetSymbols(context.Context, []int64) (map[int64]*model.Symbol, error) {
	return nil, fmt.Errorf("symbol lookup by id: %w", errStoreGap)
}

func (storeCatalog) loadEdges(context.Context) ([]*model.Edge, error) {
	return nil, fmt.Errorf("edge listing: %w", errStoreGap)
}
