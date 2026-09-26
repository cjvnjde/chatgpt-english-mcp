package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type imageReaderFixture struct {
	media map[string]storage.MediaObject
	calls []string
}

func (reader *imageReaderFixture) MediaByID(_ context.Context, id string) (storage.MediaObject, error) {
	reader.calls = append(reader.calls, id)
	if media, ok := reader.media[id]; ok {
		return media, nil
	}
	return storage.MediaObject{}, storage.ErrNotFound
}
func imageMetadata(id, thumbnail string) map[string]any {
	return map[string]any{"imageUrl": "https://dictionary.cambridge.org/image.png", "thumbnailUrl": "https://dictionary.cambridge.org/thumb.png", "mediaId": id, "thumbnailMediaId": thumbnail, "credit": "Photographer"}
}

func TestEmbeddedImagesAreDeduplicatedAndPrivate(t *testing.T) {
	reader := &imageReaderFixture{media: map[string]storage.MediaObject{"full": {ContentType: "image/png", Data: []byte("saved bytes")}, "thumb": {ContentType: "image/jpeg", Data: []byte("thumbnail")}, "audio": {ContentType: "audio/mpeg", Data: []byte("ID3")}}}
	images := []any{imageMetadata("full", "thumb"), imageMetadata("full", "thumb"), imageMetadata("missing", "thumb"), imageMetadata("audio", ""), imageMetadata("missing", "")}
	value := map[string]any{"items": []any{map[string]any{"lookup": map[string]any{"images": images}}}}
	content, changed := embedDictionaryImages(context.WithValue(context.Background(), imageMediaReaderKey{}, reader), value)
	if !changed || len(content) != 2 {
		t.Fatalf("changed=%v content=%v", changed, content)
	}
	if image := content[0].(*mcp.ImageContent); string(image.Data) != "saved bytes" || image.MIMEType != "image/png" || image.Meta["mediaId"] != "full" {
		t.Fatalf("image=%#v", image)
	}
	encoded, _ := json.Marshal(value)
	if strings.Contains(string(encoded), "cambridge.org") || !strings.Contains(string(encoded), "Photographer") {
		t.Fatalf("metadata=%s", encoded)
	}
	if strings.Join(reader.calls, ",") != "full,missing,thumb,audio" {
		t.Fatalf("unexpected reads: %v", reader.calls)
	}
}

func TestEmbeddedImagesHaveBoundedCountsBytesAndReads(t *testing.T) {
	for _, scenario := range []string{"count", "bytes", "reads"} {
		t.Run(scenario, func(t *testing.T) {
			reader := &imageReaderFixture{media: map[string]storage.MediaObject{}}
			images := []any{}
			data := []byte("image")
			if scenario == "bytes" {
				data = make([]byte, maxEmbeddedImageBytes/2+1)
			}
			for i := 0; i < 30; i++ {
				id := fmt.Sprint(i)
				images = append(images, imageMetadata(id, ""))
				if scenario != "reads" {
					reader.media[id] = storage.MediaObject{ContentType: "image/png", Data: data}
				}
			}
			content, _ := embedDictionaryImages(context.WithValue(context.Background(), imageMediaReaderKey{}, reader), map[string]any{"images": images})
			want := maxEmbeddedImages
			if scenario == "bytes" {
				want = 1
			}
			if scenario == "reads" {
				want = 0
			}
			if len(content) != want || len(reader.calls) > maxImageReadAttempts {
				t.Fatalf("images=%d calls=%d", len(content), len(reader.calls))
			}
		})
	}
}

func TestOversizedImageFallsBackToSavedThumbnail(t *testing.T) {
	reader := &imageReaderFixture{media: map[string]storage.MediaObject{
		"full":  {ContentType: "image/png", Data: make([]byte, maxEmbeddedImageBytes+1)},
		"thumb": {ContentType: "image/png", Data: []byte("small thumbnail")},
	}}
	value := map[string]any{"images": []any{imageMetadata("full", "thumb")}}
	content, _ := embedDictionaryImages(context.WithValue(context.Background(), imageMediaReaderKey{}, reader), value)
	if len(content) != 1 || content[0].(*mcp.ImageContent).Meta["mediaId"] != "thumb" {
		t.Fatalf("thumbnail fallback = %#v", content)
	}
}

func TestMissingMediaReaderStillRemovesUpstreamImageURLs(t *testing.T) {
	value := map[string]any{"images": []any{imageMetadata("missing", "")}}
	content, changed := embedDictionaryImages(context.Background(), value)
	encoded, _ := json.Marshal(value)
	if !changed || len(content) != 0 || strings.Contains(string(encoded), "cambridge.org") {
		t.Fatalf("content=%v metadata=%s", content, encoded)
	}
}

func TestImageResponsePreservesLargeIntegers(t *testing.T) {
	type output struct {
		Revision int64                    `json:"revision"`
		Images   []domain.DictionaryImage `json:"images"`
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	inputSchema, err := inferredSchema[DictionaryLookupInput]()
	if err != nil {
		t.Fatal(err)
	}
	outputSchema, err := inferredSchema[output]()
	if err != nil {
		t.Fatal(err)
	}
	const revision = int64(9007199254740993)
	err = registerTool(server, &mcp.Tool{Name: "test"}, inputSchema, outputSchema, slog.Default(), func(context.Context, DictionaryLookupInput) (output, error) {
		return output{Revision: revision, Images: []domain.DictionaryImage{{ImageURL: "https://dictionary.cambridge.org/image.png"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"term":"bank"}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	NewHTTPHandler(server, nil).ServeHTTP(response, request)
	if response.Code != 200 || strings.Count(response.Body.String(), fmt.Sprint(revision)) != 2 {
		t.Fatalf("integer not preserved in text and structured output: %s", response.Body.String())
	}
}

func TestAuthenticatedToolsEmbedStoredImagesWithoutPublicRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aCuoAAAAASUVORK5CYII=")
	media, err := db.StoreMedia(ctx, storage.MediaInsert{ContentType: "image/png", Data: png, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	provider := &fixtureProvider{}
	data, _ := provider.Lookup(ctx, "bank")
	originalURL := "https://dictionary.cambridge.org/image.png"
	image := domain.DictionaryImage{ImageURL: originalURL, MediaID: media.ID, Alt: "River bank"}
	data.Images = []domain.DictionaryImage{image}
	data.Entries[0].Definitions[0].Images = []domain.DictionaryImage{image}
	snapshot, err := db.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{Provider: provider.Name(), ParserVersion: provider.ParserVersion(), NormalizedTerm: "bank", Data: data, FetchedAt: time.Now(), ExpiresAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	source := storage.SourceVersion{Provider: provider.Name(), ParserVersion: provider.ParserVersion()}
	server, err := New(Services{Dictionary: dictionary.NewService(db, provider, nil), Vocabulary: vocabulary.NewService(db, "owner", source), Learning: learning.NewService(db, "owner"), Media: db}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAuthenticatedHTTPHandler(server, "secret", nil)
	for _, tool := range []string{"dictionary_lookup", "vocabulary_save", "vocabulary_get"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"term":"bank"}}}`
		for _, authorized := range []bool{false, true} {
			request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			if authorized {
				request.Header.Set("Authorization", "Bearer secret")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !authorized {
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("unauthenticated status=%d", response.Code)
				}
				continue
			}
			var envelope struct {
				Result mcp.CallToolResult `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			result := envelope.Result
			if response.Code != 200 || result.IsError || len(result.Content) != 2 {
				t.Fatalf("%s response=%s", tool, response.Body.String())
			}
			block, ok := result.Content[1].(*mcp.ImageContent)
			if !ok || !bytes.Equal(block.Data, png) || block.MIMEType != "image/png" {
				t.Fatalf("%s image=%#v", tool, result.Content[1])
			}
			text := result.Content[0].(*mcp.TextContent).Text
			structured, _ := json.Marshal(result.StructuredContent)
			if strings.Contains(text, originalURL) || bytes.Contains(structured, []byte(originalURL)) {
				t.Fatal("Cambridge image URL leaked")
			}
		}
	}
	for _, path := range []string{"/dictionary-images/" + snapshot.ID + "/" + media.ID, "/media/" + media.ID} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("public image route status=%d", response.Code)
		}
	}
	stored, err := db.DictionarySnapshotByID(ctx, snapshot.ID)
	if err != nil || stored.Data.Images[0].ImageURL != originalURL {
		t.Fatal("stored source metadata was modified")
	}
}
