package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/mcpserver"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestContextOnlyChangesInvalidateAdminDrafts(t *testing.T) {
	for _, writer := range []string{"admin", "mcp"} {
		t.Run(writer, func(t *testing.T) {
			store, handler := testHandler(t)
			created := adminRequest(handler, "POST", "/vocabulary", `{"term":"bank","context":"original context"}`, testToken)
			var item vocabularyResponse
			if created.Code != http.StatusOK {
				t.Fatal(created.Body.String())
			}
			if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
				t.Fatal(err)
			}
			path := "/vocabulary/" + item.ItemID
			if writer == "admin" {
				response := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"context":"winning context"}`, item.Revision), testToken)
				if response.Code != http.StatusOK {
					t.Fatal(response.Body.String())
				}
			} else {
				ctx := context.Background()
				base, _ := url.Parse("https://dictionary.example")
				server, err := mcpserver.New(mcpserver.Services{
					Dictionary: dictionary.NewService(store, dictionary.NewCambridgeProvider(base, time.Second, nil), nil),
					Vocabulary: vocabulary.NewService(store, "default", storage.SourceVersion{}),
					Learning:   learning.NewService(store, "default"),
				}, nil)
				if err != nil {
					t.Fatal(err)
				}
				serverTransport, clientTransport := mcp.NewInMemoryTransports()
				session, err := server.Connect(ctx, serverTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				client, err := mcp.NewClient(&mcp.Implementation{Name: "revision-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "vocabulary_update", Arguments: map[string]any{
					"itemId": item.ItemID, "changes": map[string]any{"context": "winning context"},
				}})
				if err != nil || result.IsError {
					t.Fatalf("MCP context update: %v %#v", err, result)
				}
			}
			stale := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"context":"losing context"}`, item.Revision), testToken)
			if stale.Code != http.StatusConflict {
				t.Fatalf("stale context write returned %d: %s", stale.Code, stale.Body.String())
			}
			var current vocabularyResponse
			if err := json.Unmarshal(adminRequest(handler, "GET", path, "", testToken).Body.Bytes(), &current); err != nil {
				t.Fatal(err)
			}
			if current.Context != "winning context" || current.Revision != item.Revision+1 {
				t.Fatalf("unexpected current item: %#v", current)
			}
			// Repeating an unchanged context must not spuriously invalidate drafts.
			response := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"context":"winning context"}`, current.Revision), testToken)
			var unchanged vocabularyResponse
			if err := json.Unmarshal(response.Body.Bytes(), &unchanged); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || unchanged.Revision != current.Revision {
				t.Fatalf("no-op context changed revision: %s", response.Body.String())
			}
		})
	}
}
