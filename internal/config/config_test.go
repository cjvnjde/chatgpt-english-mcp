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
}
