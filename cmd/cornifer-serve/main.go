// Command cornifer-serve runs Cornifer's localhost-only companion website
// and Streamable HTTP MCP endpoint. API keys are read only in this process.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hendrixx-RE/cornifer/internal/companion"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

//go:embed web/*
var web embed.FS

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dbCfg, err := store.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	engine, err := store.NewPostgres(ctx, dbCfg)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()
	product, err := companion.NewPostgresStore(ctx, dbCfg.DSN)
	if err != nil {
		log.Fatal(err)
	}
	defer product.Close()
	runtime := companion.RuntimeConfigFromEnv()
	service := companion.NewService(product, companion.LocalRunner{Engine: engine, Config: runtime}, companion.EngineContextBuilder{Engine: engine, Config: runtime})
	app := companion.HTTPHandler(service, runtime, companion.Explorer{Engine: engine})
	assets, err := fs.Sub(web, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", app)
	mux.Handle("/mcp", sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return companion.NewMCPServer(service) }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
	mux.Handle("/", http.FileServer(http.FS(assets)))
	addr := os.Getenv("CORNIFER_COMPANION_ADDR")
	if addr == "" {
		addr = "127.0.0.1:7788"
	}
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("Cornifer companion listening at http://%s (MCP: /mcp)", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(fmt.Errorf("serve: %w", err))
	}
}
