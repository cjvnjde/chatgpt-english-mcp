package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSQLitePath   = "/app/data/english-mcp.sqlite"
	defaultCambridgeURL = "https://dictionary.cambridge.org"
)

type Config struct {
	SQLitePath       string
	OwnerKey         string
	DictionaryTTL    time.Duration
	CambridgeBaseURL *url.URL
	CambridgeTimeout time.Duration
	LogLevel         slog.Level
	LogFormat        string
}

func Load() (Config, error) {
	cacheDays, err := positiveIntEnvironment("DICTIONARY_CACHE_TTL_DAYS", 30)
	if err != nil {
		return Config{}, err
	}
	timeoutSeconds, err := positiveIntEnvironment("CAMBRIDGE_TIMEOUT_SECONDS", 20)
	if err != nil {
		return Config{}, err
	}

	baseURL, err := url.Parse(environment("CAMBRIDGE_BASE_URL", defaultCambridgeURL))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return Config{}, fmt.Errorf("CAMBRIDGE_BASE_URL must be an absolute HTTP or HTTPS URL")
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return Config{}, fmt.Errorf("CAMBRIDGE_BASE_URL must use HTTP or HTTPS")
	}

	logLevel, err := parseLogLevel(environment("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	logFormat := strings.ToLower(environment("LOG_FORMAT", "json"))
	if logFormat != "json" && logFormat != "text" {
		return Config{}, fmt.Errorf("LOG_FORMAT must be json or text")
	}

	sqlitePath := environment("SQLITE_PATH", defaultSQLitePath)
	if sqlitePath == ":memory:" {
		return Config{}, fmt.Errorf("SQLITE_PATH=:memory: is allowed only in automated tests")
	}
	ownerKey := environment("MCP_OWNER_KEY", "default")
	if strings.TrimSpace(ownerKey) == "" {
		return Config{}, fmt.Errorf("MCP_OWNER_KEY must not be empty")
	}

	return Config{
		SQLitePath:       sqlitePath,
		OwnerKey:         ownerKey,
		DictionaryTTL:    time.Duration(cacheDays) * 24 * time.Hour,
		CambridgeBaseURL: baseURL,
		CambridgeTimeout: time.Duration(timeoutSeconds) * time.Second,
		LogLevel:         logLevel,
		LogFormat:        logFormat,
	}, nil
}

func environment(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func positiveIntEnvironment(name string, fallback int) (int, error) {
	value := environment(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
}
