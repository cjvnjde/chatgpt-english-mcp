package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiresStrongBearerToken(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantError bool
	}{
		{name: "missing", token: "", wantError: true},
		{name: "too short", token: strings.Repeat("x", minimumBearerTokenBytes-1), wantError: true},
		{name: "minimum length", token: strings.Repeat("x", minimumBearerTokenBytes)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("MCP_BEARER_TOKEN", test.token)

			configuration, err := Load()
			if test.wantError {
				if err == nil {
					t.Fatal("Load() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if configuration.MCPBearerToken != test.token {
				t.Fatal("Load() did not retain MCP_BEARER_TOKEN")
			}
		})
	}
}

func TestLoadUsesSeparateTunnelAndExternalAddresses(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("MCP_TUNNEL_LISTEN_ADDRESS", " 0.0.0.0:8080 ")
	t.Setenv("MCP_EXTERNAL_LISTEN_ADDRESS", " 0.0.0.0:8081 ")

	configuration, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.MCPTunnelListenAddress != defaultTunnelAddress {
		t.Fatalf("MCPTunnelListenAddress = %q, want %q", configuration.MCPTunnelListenAddress, defaultTunnelAddress)
	}
	if configuration.MCPExternalListenAddress != defaultExternalAddress {
		t.Fatalf("MCPExternalListenAddress = %q, want %q", configuration.MCPExternalListenAddress, defaultExternalAddress)
	}
}

func TestLoadRejectsSharedTunnelAndExternalAddress(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("MCP_EXTERNAL_LISTEN_ADDRESS", defaultTunnelAddress)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil")
	}
}

func TestLoadAnkiExportIsDisabledWithoutSecrets(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("ANKI_EXPORT_TOKEN_FILE", "/does/not/exist")
	configuration, err := Load()
	if err != nil || configuration.AnkiSyncEnabled || configuration.AnkiExportToken != "" {
		t.Fatalf("disabled Anki configuration = %#v, error %v", configuration, err)
	}
}

func TestLoadAnkiExportSecretFile(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("ANKI_SYNC_ENABLED", "true")
	token := strings.Repeat("anki-secret", 4)
	path := filepath.Join(t.TempDir(), "export-token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANKI_EXPORT_TOKEN_FILE", path)
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !configuration.AnkiSyncEnabled || configuration.AnkiExportToken != token {
		t.Fatal("enabled Anki configuration did not load the secret file")
	}
	if configuration.AnkiExportListenAddress != defaultAnkiExportAddress || configuration.AnkiSourceNamespace != "english-mcp" {
		t.Fatalf("Anki source configuration = address %q, namespace %q", configuration.AnkiExportListenAddress, configuration.AnkiSourceNamespace)
	}
	t.Setenv("ANKI_EXPORT_TOKEN", token)
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted conflicting token sources")
	}
}

func TestLoadRejectsInvalidAnkiExportConfiguration(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid enablement", key: "ANKI_SYNC_ENABLED", value: "sometimes"},
		{name: "missing token", key: "ANKI_EXPORT_TOKEN", value: ""},
		{name: "short token", key: "ANKI_EXPORT_TOKEN", value: "short"},
		{name: "MCP token reuse", key: "ANKI_EXPORT_TOKEN", value: strings.Repeat("x", minimumBearerTokenBytes)},
		{name: "whitespace token", key: "ANKI_EXPORT_TOKEN", value: strings.Repeat("a ", 32)},
		{name: "malformed token", key: "ANKI_EXPORT_TOKEN", value: strings.Repeat("a", 32) + ",other"},
		{name: "misplaced padding", key: "ANKI_EXPORT_TOKEN", value: strings.Repeat("a", 32) + "=b"},
		{name: "missing address", key: "ANKI_EXPORT_LISTEN_ADDRESS", value: ""},
		{name: "malformed address", key: "ANKI_EXPORT_LISTEN_ADDRESS", value: "localhost"},
		{name: "dynamic port", key: "ANKI_EXPORT_LISTEN_ADDRESS", value: "0.0.0.0:0"},
		{name: "tunnel collision", key: "ANKI_EXPORT_LISTEN_ADDRESS", value: defaultTunnelAddress},
		{name: "external collision", key: "ANKI_EXPORT_LISTEN_ADDRESS", value: "127.0.0.1:8081"},
		{name: "empty namespace", key: "ANKI_SOURCE_NAMESPACE", value: ""},
		{name: "namespace control", key: "ANKI_SOURCE_NAMESPACE", value: "english\nmcp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("ANKI_SYNC_ENABLED", "true")
			t.Setenv("ANKI_EXPORT_TOKEN", strings.Repeat("anki", 10))
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load accepted invalid Anki export configuration")
			}
		})
	}
	t.Run("unreadable secret", func(t *testing.T) {
		setValidEnvironment(t)
		t.Setenv("ANKI_SYNC_ENABLED", "true")
		t.Setenv("ANKI_EXPORT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
		if _, err := Load(); err == nil {
			t.Fatal("Load accepted an unreadable token secret")
		}
	})
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("CAMBRIDGE_TIMEOUT_SECONDS", "20")
	t.Setenv("CAMBRIDGE_BASE_URL", defaultCambridgeURL)
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("SQLITE_PATH", "/tmp/english-mcp-test.sqlite")
	t.Setenv("MCP_OWNER_KEY", "test-owner")
	t.Setenv("MCP_TUNNEL_LISTEN_ADDRESS", defaultTunnelAddress)
	t.Setenv("MCP_EXTERNAL_LISTEN_ADDRESS", defaultExternalAddress)
	t.Setenv("MCP_BEARER_TOKEN", strings.Repeat("x", minimumBearerTokenBytes))
	t.Setenv("ANKI_SYNC_ENABLED", "false")
	t.Setenv("ANKI_EXPORT_TOKEN", "")
	t.Setenv("ANKI_EXPORT_TOKEN_FILE", "")
	t.Setenv("ANKI_EXPORT_LISTEN_ADDRESS", defaultAnkiExportAddress)
	t.Setenv("ANKI_SOURCE_NAMESPACE", "english-mcp")
}
