package ankiexport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/mcpserver"
	"english-learning-mcp/internal/storage"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSnapshotHandlerRequiresUnambiguousExportAuthentication(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	token := strings.Repeat("export-secret", 3)
	handler := NewHTTPHandler(store, "owner", "english-mcp", token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "MCP token", headers: []string{"Bearer " + strings.Repeat("mcp-secret", 4)}},
		{name: "wrong scheme", headers: []string{"Basic " + token}},
		{name: "extra space", headers: []string{"Bearer  " + token}},
		{name: "trailing space", headers: []string{"Bearer " + token + " "}},
		{name: "tab separator", headers: []string{"Bearer\t" + token}},
		{name: "duplicate header", headers: []string{"Bearer " + token, "Bearer " + token}},
		{name: "combined header", headers: []string{"Bearer " + token + ",Bearer " + token}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, EndpointPath, nil)
			for _, header := range test.headers {
				request.Header.Add("Authorization", header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("unauthorized request returned %d with headers %v", response.Code, response.Header())
			}
			if strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), "complete") {
				t.Fatal("unauthorized response exposed token or snapshot")
			}
		})
	}
}

func TestSnapshotHandlerExportsOnlyConfiguredOwnerAndRejectsWrites(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"owner", "other"} {
		if _, _, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
			OwnerKey: owner, Term: owner + " word", NormalizedTerm: owner + " word",
			Status: domain.LearningStatusArchived, Now: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	token := strings.Repeat("export-secret", 3)
	handler := NewHTTPHandler(store, "owner", "english-mcp", token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: EndpointPath + "?owner=other&limit=0", status: http.StatusOK},
		{method: http.MethodPost, path: EndpointPath, status: http.StatusMethodNotAllowed},
		{method: http.MethodDelete, path: EndpointPath, status: http.StatusMethodNotAllowed},
		{method: http.MethodHead, path: EndpointPath, status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: mcpserver.EndpointPath, status: http.StatusNotFound},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "bEaReR "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d, headers %v, body %s", response.Code, response.Header(), response.Body.String())
			}
			if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodGet {
				t.Fatal("read-only endpoint did not advertise GET")
			}
			if test.status != http.StatusOK {
				return
			}
			var snapshot storage.VocabularySnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if !snapshot.Complete || snapshot.Owner != "owner" || snapshot.ItemCount != 1 || len(snapshot.Items) != 1 || snapshot.Items[0].Vocabulary.Term != "owner word" {
				t.Fatalf("owner export = %#v", snapshot)
			}
		})
	}
	snapshot, err := store.ExportVocabulary(ctx, "owner", "english-mcp")
	if err != nil || snapshot.ItemCount != 1 {
		t.Fatalf("write requests modified vocabulary: %#v, %v", snapshot, err)
	}
}

func TestSnapshotHandlerFailureIsNeverAnEmptySnapshot(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("export-secret", 3)
	handler := NewHTTPHandler(store, "owner", "english-mcp", token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, EndpointPath, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || json.Valid(response.Body.Bytes()) {
		t.Fatalf("failed export became an authoritative response: %d %s", response.Code, response.Body.String())
	}
}

func TestMCPHandlersDoNotExposeSnapshot(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	token := strings.Repeat("mcp-secret", 4)
	for _, handler := range []http.Handler{
		mcpserver.NewHTTPHandler(server, nil),
		mcpserver.NewAuthenticatedHTTPHandler(server, token, nil),
	} {
		request := httptest.NewRequest(http.MethodGet, EndpointPath, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("MCP listener exposed export: %d", response.Code)
		}
	}
}
