// Command cornifer-mcp exposes Cornifer's index over MCP (stdio transport).
// Phase 0 wires up the server with no tools registered; later waves add
// search_code, find_definition, find_references, get_dependencies,
// get_call_graph, get_blast_radius, and find_cycles via internal/mcp (see
// plan.md "MCP server").
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cornifer",
		Version: "0.0.0-phase0",
	}, nil)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("cornifer-mcp: %v", err)
	}
}
