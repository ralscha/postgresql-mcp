package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"postgresql-mcp/internal/config"
	pgdb "postgresql-mcp/internal/db"
	"postgresql-mcp/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("configuration error: %v", err)
		return 1
	}

	client, err := pgdb.Open(cfg)
	if err != nil {
		log.Printf("database setup error: %v", err)
		return 1
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("database close error: %v", err)
		}
	}()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "postgresql-mcp",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Instructions: "MCP server for PostgreSQL. Tools are registered according to POSTGRESQL_ACCESS_LEVEL.",
	})
	tools.Register(server, client)

	if err := runServer(context.Background(), cfg, server); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func runServer(ctx context.Context, cfg config.Config, server *mcp.Server) error {
	switch cfg.Transport {
	case config.StdioTransport:
		return server.Run(ctx, &mcp.StdioTransport{})
	case config.SSETransport:
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		mux := http.NewServeMux()
		mux.Handle(cfg.SSEPath, handler)
		log.Printf("postgresql-mcp listening for SSE at http://%s%s", cfg.HTTPAddr, cfg.SSEPath)
		return http.ListenAndServe(cfg.HTTPAddr, mux)
	default:
		return cfg.Validate()
	}
}
