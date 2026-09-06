package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"postgresql-mcp/internal/config"
	pgdb "postgresql-mcp/internal/db"
	"postgresql-mcp/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

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
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "MCP server for PostgreSQL. Tools are registered according to POSTGRESQL_ACCESS_LEVEL.",
	})
	tools.Register(server, client)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, cfg, server); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func runServer(ctx context.Context, cfg config.Config, server *mcp.Server) error {
	switch cfg.Transport {
	case config.StdioTransport:
		return server.Run(ctx, &mcp.StdioTransport{})
	case config.HTTPTransport:
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true})
		mux := http.NewServeMux()
		protected := http.NewCrossOriginProtection().Handler(handler)
		mux.Handle(cfg.HTTPPath, bearerAuthentication(cfg.HTTPBearerToken, protected))
		httpServer := &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		listener, err := net.Listen("tcp", cfg.HTTPAddr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
		}
		log.Printf("postgresql-mcp listening for stateless Streamable HTTP at http://%s%s", listener.Addr(), cfg.HTTPPath)
		return serveHTTP(ctx, httpServer, listener)
	default:
		return cfg.Validate()
	}
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		return nil
	}
}

func bearerAuthentication(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		actual := sha256.Sum256([]byte(request.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request)
	})
}
