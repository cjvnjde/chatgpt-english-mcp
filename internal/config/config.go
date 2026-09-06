package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultSQLitePath        = "/app/data/english-mcp.sqlite"
	defaultCambridgeURL      = "https://dictionary.cambridge.org"
	defaultTunnelAddress     = "0.0.0.0:8080"
	defaultExternalAddress   = "0.0.0.0:8081"
	defaultAnkiExportAddress = "0.0.0.0:8082"
	minimumBearerTokenBytes  = 32
)

type Config struct {
	SQLitePath               string
	OwnerKey                 string
	MCPTunnelListenAddress   string
	MCPExternalListenAddress string
	MCPBearerToken           string
	AnkiSyncEnabled          bool
	AnkiExportListenAddress  string
	AnkiExportToken          string
	AnkiSourceNamespace      string
	CambridgeBaseURL         *url.URL
	CambridgeTimeout         time.Duration
	LogLevel                 slog.Level
	LogFormat                string
}

func Load() (Config, error) {
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
	tunnelAddress := environment("MCP_TUNNEL_LISTEN_ADDRESS", defaultTunnelAddress)
	if tunnelAddress == "" {
		return Config{}, fmt.Errorf("MCP_TUNNEL_LISTEN_ADDRESS must not be empty")
	}
	externalAddress := environment("MCP_EXTERNAL_LISTEN_ADDRESS", defaultExternalAddress)
	if externalAddress == "" {
		return Config{}, fmt.Errorf("MCP_EXTERNAL_LISTEN_ADDRESS must not be empty")
	}
	if tunnelAddress == externalAddress {
		return Config{}, fmt.Errorf("MCP tunnel and external listen addresses must be different")
	}
	mcpBearerToken := environment("MCP_BEARER_TOKEN", "")
	if len(mcpBearerToken) < minimumBearerTokenBytes {
		return Config{}, fmt.Errorf("MCP_BEARER_TOKEN must be at least %d bytes", minimumBearerTokenBytes)
	}

	ankiEnabled, err := strconv.ParseBool(environment("ANKI_SYNC_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("ANKI_SYNC_ENABLED must be a boolean")
	}
	ankiAddress := environment("ANKI_EXPORT_LISTEN_ADDRESS", defaultAnkiExportAddress)
	ankiNamespace := environment("ANKI_SOURCE_NAMESPACE", "english-mcp")
	var ankiToken string
	if ankiEnabled {
		if ankiNamespace == "" || !utf8.ValidString(ankiNamespace) || strings.ContainsFunc(ankiNamespace, unicode.IsControl) {
			return Config{}, fmt.Errorf("ANKI_SOURCE_NAMESPACE must be nonempty text without control characters")
		}
		_, ankiPort, err := net.SplitHostPort(ankiAddress)
		portNumber, portErr := strconv.Atoi(ankiPort)
		if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return Config{}, fmt.Errorf("ANKI_EXPORT_LISTEN_ADDRESS must contain a host and a valid TCP port")
		}
		for _, address := range []string{tunnelAddress, externalAddress} {
			_, mcpPort, err := net.SplitHostPort(address)
			mcpPortNumber, portErr := strconv.Atoi(mcpPort)
			if address == ankiAddress || (err == nil && portErr == nil && mcpPortNumber == portNumber) {
				return Config{}, fmt.Errorf("Anki export and MCP listeners must use different ports")
			}
		}
		ankiToken, err = ankiExportToken()
		if err != nil {
			return Config{}, err
		}
		if ankiToken == mcpBearerToken {
			return Config{}, fmt.Errorf("ANKI_EXPORT_TOKEN must be distinct from MCP_BEARER_TOKEN")
		}
	}

	return Config{
		SQLitePath:               sqlitePath,
		OwnerKey:                 ownerKey,
		MCPTunnelListenAddress:   tunnelAddress,
		MCPExternalListenAddress: externalAddress,
		MCPBearerToken:           mcpBearerToken,
		AnkiSyncEnabled:          ankiEnabled,
		AnkiExportListenAddress:  ankiAddress,
		AnkiExportToken:          ankiToken,
		AnkiSourceNamespace:      ankiNamespace,
		CambridgeBaseURL:         baseURL,
		CambridgeTimeout:         time.Duration(timeoutSeconds) * time.Second,
		LogLevel:                 logLevel,
		LogFormat:                logFormat,
	}, nil
}

func ankiExportToken() (string, error) {
	token := environment("ANKI_EXPORT_TOKEN", "")
	path := environment("ANKI_EXPORT_TOKEN_FILE", "")
	if path != "" {
		if token != "" {
			return "", fmt.Errorf("set only one of ANKI_EXPORT_TOKEN and ANKI_EXPORT_TOKEN_FILE")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read ANKI_EXPORT_TOKEN_FILE")
		}
		token = strings.TrimSpace(string(content))
	}
	if len(token) < minimumBearerTokenBytes {
		return "", fmt.Errorf("ANKI_EXPORT_TOKEN must be at least %d bytes", minimumBearerTokenBytes)
	}
	unpadded := strings.TrimRight(token, "=")
	if unpadded == "" || strings.ContainsFunc(unpadded, func(character rune) bool {
		return !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~+/", character))
	}) {
		return "", fmt.Errorf("ANKI_EXPORT_TOKEN must use bearer-token characters")
	}
	return token, nil
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
