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

// storeDeps wires the real backend.
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

// storeCatalog adapts store.Store to mcp.Catalog: a thin passthrough now
// that internal/store has ID-based lookups and bulk edge listing. loadEdges
// binds repoID to satisfy graph.EdgeLoader, whose LoadEdges takes no repoID.
type storeCatalog struct {
	st     store.Store
	repoID int64
}

func (c storeCatalog) GetFiles(ctx context.Context, ids []int64) (map[int64]*model.File, error) {
	return c.st.GetFiles(ctx, ids)
}

func (c storeCatalog) GetChunks(ctx context.Context, ids []int64) (map[int64]*model.Chunk, error) {
	return c.st.GetChunks(ctx, ids)
}

func (c storeCatalog) GetSymbols(ctx context.Context, ids []int64) (map[int64]*model.Symbol, error) {
	return c.st.GetSymbols(ctx, ids)
}

func (c storeCatalog) loadEdges(ctx context.Context) ([]*model.Edge, error) {
	return c.st.LoadEdges(ctx, c.repoID)
}
