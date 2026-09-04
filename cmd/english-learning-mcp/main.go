package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"english-learning-mcp/internal/config"
	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/mcpserver"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

const shutdownTimeout = 10 * time.Second

type httpEndpoint struct {
	name    string
	address string
	handler http.Handler
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "english-learning-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	configuration, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := newLogger(configuration)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, configuration.SQLitePath)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close storage: %w", err))
		}
	}()

	provider := dictionary.NewCambridgeProvider(
		configuration.CambridgeBaseURL,
		configuration.CambridgeTimeout,
		logger,
	)
	currentSource := storage.SourceVersion{
		Provider:       provider.Name(),
		ParserVersion:  provider.ParserVersion(),
		DatasetVersion: provider.DatasetVersion(),
	}
	server, err := mcpserver.New(mcpserver.Services{
		Dictionary: dictionary.NewService(store, provider, logger),
		Vocabulary: vocabulary.NewService(store, configuration.OwnerKey, currentSource),
		Learning:   learning.NewService(store, configuration.OwnerKey),
	}, logger)
	if err != nil {
		return fmt.Errorf("create MCP server: %w", err)
	}

	endpoints := []httpEndpoint{
		{
			name:    "tunnel",
			address: configuration.MCPTunnelListenAddress,
			handler: mcpserver.NewHTTPHandler(server, logger),
		},
		{
			name:    "external",
			address: configuration.MCPExternalListenAddress,
			handler: mcpserver.NewAuthenticatedHTTPHandler(server, configuration.MCPBearerToken, logger),
		},
	}
	if err := runHTTPServers(ctx, endpoints, logger); err != nil {
		return fmt.Errorf("run MCP HTTP servers: %w", err)
	}
	return nil
}

func runHTTPServers(ctx context.Context, endpoints []httpEndpoint, logger *slog.Logger) error {
	serverContext, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, len(endpoints))
	for _, endpoint := range endpoints {
		go func() {
			err := runHTTPServer(serverContext, endpoint, logger)
			if err != nil {
				err = fmt.Errorf("%s listener: %w", endpoint.name, err)
			}
			results <- err
		}()
	}

	var runErr error
	for range endpoints {
		if err := <-results; err != nil {
			runErr = errors.Join(runErr, err)
		}
		cancel()
	}
	return runErr
}

func runHTTPServer(ctx context.Context, endpoint httpEndpoint, logger *slog.Logger) error {
	httpServer := &http.Server{
		Addr:              endpoint.address,
		Handler:           endpoint.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}

	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			logger.Error("MCP HTTP server shutdown failed", "error", err)
		}
	}()

	logger.Info(
		"MCP HTTP server listening",
		"access", endpoint.name,
		"address", endpoint.address,
		"path", mcpserver.EndpointPath,
	)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newLogger(configuration config.Config) *slog.Logger {
	options := &slog.HandlerOptions{Level: configuration.LogLevel}
	if configuration.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, options))
}
