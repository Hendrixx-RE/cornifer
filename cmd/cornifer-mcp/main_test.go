package main

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/mcp"
)

func TestNewServerWithEmptyDeps(t *testing.T) {
	ctx := context.Background()
	server := newServer(mcp.Deps{})
	ct, st := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 7 {
		t.Fatalf("want 7 tools, got %d", len(tools.Tools))
	}
	// An unconfigured backend must yield a tool error, not a crash.
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "find_cycles", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("want tool error, got %v %+v", err, res)
	}
}
