// Command cornifer-companion-mcp exposes the companion repository registry,
// cited context packs, and explicit application memory over stdio. It uses the
// same Service and tool registration as cornifer-serve's Streamable HTTP MCP.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/companion"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

func main() {
	log.SetOutput(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dbCfg, err := store.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	engine, err := store.NewPostgres(ctx, dbCfg)
	if err != nil {
		log.Fatal(fmt.Errorf("connect engine store: %w", err))
	}
	defer engine.Close()
	product, err := companion.NewPostgresStore(ctx, dbCfg.DSN)
	if err != nil {
		log.Fatal(err)
	}
	defer product.Close()
	runtime := companion.RuntimeConfigFromEnv()
	service := companion.NewService(product, companion.LocalRunner{Engine: engine, Config: runtime}, companion.EngineContextBuilder{Engine: engine, Config: runtime})
	if err := companion.NewMCPServer(service).Run(ctx, &sdk.StdioTransport{}); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
