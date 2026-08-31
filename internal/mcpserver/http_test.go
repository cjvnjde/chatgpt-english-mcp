package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerRoundTripper struct {
	token string
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	authorizedRequest := request.Clone(request.Context())
	authorizedRequest.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(authorizedRequest)
}

func TestAuthenticatedHTTPHandlerRequiresBearerToken(t *testing.T) {
	token := strings.Repeat("secret", 6)
	handler := NewAuthenticatedHTTPHandler(
		mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil),
		token,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic " + token, wantStatus: http.StatusUnauthorized},
		{name: "wrong token", authorization: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "valid", authorization: "Bearer " + token, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`)
			request := httptest.NewRequest(http.MethodPost, EndpointPath, bytes.NewReader(requestBody))
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Content-Type", "application/json")
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q, want Bearer", response.Header().Get("WWW-Authenticate"))
			}
			if test.wantStatus == http.StatusOK {
				assertInitializeResponse(t, response.Body.Bytes())
			}
		})
	}
}

func TestHTTPHandlerAllowsTunnelRequestWithoutAuthentication(t *testing.T) {
	handler := NewHTTPHandler(
		mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil),
		nil,
	)
	requestBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`)
	request := httptest.NewRequest(http.MethodPost, EndpointPath, bytes.NewReader(requestBody))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertInitializeResponse(t, response.Body.Bytes())
}

func TestHTTPHandlerOnlyExposesMCPPath(t *testing.T) {
	handler := NewHTTPHandler(
		mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil),
		nil,
	)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestAuthenticatedHTTPHandlerSupportsStreamableClient(t *testing.T) {
	token := strings.Repeat("secret", 6)
	handler := NewAuthenticatedHTTPHandler(
		mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil),
		token,
		nil,
	)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + EndpointPath,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(result.Tools) != 0 {
		t.Fatalf("tools length = %d, want 0", len(result.Tools))
	}
}

func assertInitializeResponse(t *testing.T, responseBody []byte) {
	t.Helper()

	var response struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatalf("decode initialize response %q: %v", responseBody, err)
	}
	if response.Result.ServerInfo.Name != "test-server" {
		t.Fatalf("server name = %q, want test-server", response.Result.ServerInfo.Name)
	}
}
