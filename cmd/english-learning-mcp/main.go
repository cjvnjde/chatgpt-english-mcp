package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"english-learning-mcp/internal/config"
	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/mcpserver"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	}, logger)
	if err != nil {
		return fmt.Errorf("create MCP server: %w", err)
	}

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run MCP server: %w", err)
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
