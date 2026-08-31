package config

import (
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

func TestLoadTrimsListenAddress(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("MCP_LISTEN_ADDRESS", " 0.0.0.0:8080 ")

	configuration, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.MCPListenAddress != defaultMCPListenAddress {
		t.Fatalf("MCPListenAddress = %q, want %q", configuration.MCPListenAddress, defaultMCPListenAddress)
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("CAMBRIDGE_TIMEOUT_SECONDS", "20")
	t.Setenv("CAMBRIDGE_BASE_URL", defaultCambridgeURL)
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("SQLITE_PATH", "/tmp/english-mcp-test.sqlite")
	t.Setenv("MCP_OWNER_KEY", "test-owner")
	t.Setenv("MCP_LISTEN_ADDRESS", defaultMCPListenAddress)
	t.Setenv("MCP_BEARER_TOKEN", strings.Repeat("x", minimumBearerTokenBytes))
}
