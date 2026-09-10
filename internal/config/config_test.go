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
		{name: "embedded newline", token: strings.Repeat("x", minimumBearerTokenBytes) + "\ny", wantError: true},
		{name: "embedded space", token: strings.Repeat("x", minimumBearerTokenBytes) + " y", wantError: true},
		{name: "non bearer characters", token: strings.Repeat("x", minimumBearerTokenBytes) + ",y", wantError: true},
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
	t.Setenv("MCP_EXTERNAL_LISTEN_ADDRESS", defaultExternalAddress)
	t.Setenv("MCP_BEARER_TOKEN", strings.Repeat("x", minimumBearerTokenBytes))
	t.Setenv("ADMIN_BEARER_TOKEN", "")
	t.Setenv("ANKI_SYNC_ENABLED", "false")
	t.Setenv("ANKI_EXPORT_TOKEN", "")
	t.Setenv("ANKI_EXPORT_TOKEN_FILE", "")
	t.Setenv("ANKI_EXPORT_LISTEN_ADDRESS", defaultAnkiExportAddress)
	t.Setenv("ANKI_SOURCE_NAMESPACE", "english-mcp")
}

func TestAdminTokenIsOptionalStrongAndSeparate(t *testing.T) {
	for _, item := range []struct {
		token string
		valid bool
	}{
		{"", true}, {strings.Repeat("a", 32), true}, {"short", false},
		{strings.Repeat("x", 32), false}, {strings.Repeat("a", 32) + " b", false},
		{strings.Repeat("a", 32) + "\vb", false},
		{strings.Repeat("a", 32) + ",b", false},
	} {
		t.Run(item.token, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("ADMIN_BEARER_TOKEN", item.token)
			configuration, err := Load()
			if (err == nil) != item.valid {
				t.Fatalf("valid=%v, error=%v", item.valid, err)
			}
			if err == nil && configuration.AdminBearerToken != item.token {
				t.Fatal("admin token was not retained")
			}
		})
	}
}

func TestLoadRejectsOverflowingCambridgeTimeout(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("CAMBRIDGE_TIMEOUT_SECONDS", "9223372037")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a timeout that overflows time.Duration and disables the HTTP client deadline")
	}
}

func TestLoadRejectsNonPersistentSQLitePaths(t *testing.T) {
	for _, path := range []string{
		"",
		":memory:",
		":memory:?cache=shared",
		"file::memory:?cache=shared",
		"file:%3Amemory%3A?cache=shared",
		"file:learning?mode=memory&cache=shared",
		"file:",
		"file:?cache=shared",
	} {
		t.Run(path, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("SQLITE_PATH", path)
			if _, err := Load(); err == nil {
				t.Fatal("Load accepted a SQLite path that loses saved vocabulary on shutdown")
			}
		})
	}
}

func TestLoadAcceptsPersistentSQLiteURIs(t *testing.T) {
	for _, path := range []string{
		"file:learning.sqlite?mode=rwc",
		"file:/tmp/learning.sqlite?mode=rw",
	} {
		t.Run(path, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("SQLITE_PATH", path)
			if _, err := Load(); err != nil {
				t.Fatalf("Load rejected a persistent SQLite URI: %v", err)
			}
		})
	}
}
