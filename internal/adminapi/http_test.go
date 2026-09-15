package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

func TestAdminAuthorizationAndVocabularyLifecycle(t *testing.T) {
	_, handler := testHandler(t)
	for _, path := range []string{"/session", "/tables", "/analytics", "/suggestions", "/database", "/tables/vocabulary_items", "/vocabulary/missing"} {
		for _, token := range []string{"", "wrong-token"} {
			r := adminRequest(handler, "GET", path, "", token)
			if r.Code != http.StatusUnauthorized {
				t.Fatalf("%s: unauthorized request got %d", path, r.Code)
			}
		}
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		if r := adminRequest(handler, method, "/vocabulary", `{}`, ""); r.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected mutation: %s", method)
		}
	}
	created := adminRequest(handler, "POST", "/vocabulary", `{"term":"wringer","customDescription":"a difficult experience","notes":["Check usage"],"tags":["idiom"]}`, testToken)
	if created.Code != 200 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	id := item["itemId"].(string)
	changed := adminRequest(handler, "PATCH", "/vocabulary/"+id, fmt.Sprintf(`{"expectedRevision":%.0f,"status":"learning","notes":["Updated note"]}`, item["revision"]), testToken)
	if changed.Code != 200 || !strings.Contains(changed.Body.String(), "Updated note") {
		t.Fatalf("update: %s", changed.Body.String())
	}
	for _, input := range []string{`{"expectedRevision":2,"status":"invalid"}`, `{"expectedRevision":2,"unsupported":true}`, `{"expectedRevision":2,"status":"new"} {}`, `{"expectedRevision":2,"notes":"wrong type"}`} {
		if r := adminRequest(handler, "PATCH", "/vocabulary/"+id, input, testToken); r.Code != 400 {
			t.Fatalf("accepted invalid input %s: %d", input, r.Code)
		}
	}
	page := adminRequest(handler, "GET", "/tables/learning_cards?column=vocabulary_item_id&value="+id, "", testToken)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "wringer") {
		t.Fatalf("linked card: %s", page.Body.String())
	}
	if r := adminRequest(handler, "GET", "/analytics", "", testToken); r.Code != 200 || !strings.Contains(r.Body.String(), "learning") {
		t.Fatalf("analytics: %s", r.Body.String())
	}
	if r := adminRequest(handler, "DELETE", "/vocabulary/"+id, "", testToken); r.Code != 200 {
		t.Fatalf("delete: %s", r.Body.String())
	}
	if r := adminRequest(handler, "GET", "/vocabulary/"+id, "", testToken); r.Code != 404 {
		t.Fatalf("deleted record: %d", r.Code)
	}
	page = adminRequest(handler, "GET", "/tables/learning_cards?column=vocabulary_item_id&value="+id, "", testToken)
	if !strings.Contains(page.Body.String(), `"total":0`) {
		t.Fatalf("card was not removed: %s", page.Body.String())
	}
}

func TestAdminQueriesAndReadOnlyHistory(t *testing.T) {
	_, handler := testHandler(t)
	for _, path := range []string{
		"/tables/vocabulary_items?sort=bad", "/tables/vocabulary_items?direction=desc%3BDELETE",
		"/tables/vocabulary_items?column=unknown", "/tables/vocabulary_items?limit=101",
		"/tables/vocabulary_items?limit=no", "/tables/vocabulary_items?offset=-1",
		"/tables/vocabulary_items?from=bad-date", "/tables/vocabulary_items?comments=true",
		"/tables/vocabulary_items?from=2026-09-10&to=2026-09-09",
		"/tables/review_attempts?comments=tru",
		"/tables/vocabulary_items?column=term&value=%ZZ",
		"/suggestions?limit=0", "/suggestions?limit=201", "/suggestions?limit=no",
		"/suggestions?offset=-1", "/suggestions?offset=no", "/suggestions?limit=%ZZ",
	} {
		if r := adminRequest(handler, "GET", path, "", testToken); r.Code != 400 {
			t.Fatalf("%s: got %d %s", path, r.Code, r.Body.String())
		}
	}
	if r := adminRequest(handler, "GET", "/tables/not_a_table", "", testToken); r.Code != 404 {
		t.Fatalf("unknown table: %d", r.Code)
	}
	for _, table := range []string{"review_attempts", "learning_presentations", "dictionary_snapshots", "schema_migrations", "sqlite_sequence"} {
		if r := adminRequest(handler, "GET", "/tables/"+table, "", testToken); r.Code != 200 {
			t.Fatalf("read %s: %s", table, r.Body.String())
		}
		if r := adminRequest(handler, "DELETE", "/tables/"+table, "", testToken); r.Code != 405 {
			t.Fatalf("mutable %s: %d", table, r.Code)
		}
	}
	if r := adminRequest(handler, "GET", "/tables/vocabulary_items?q=%27%20OR%201%3D1--", "", testToken); r.Code != 200 {
		t.Fatalf("search wasn't parameterized: %d", r.Code)
	}
}

func TestAdminDatabaseExportIsStandalone(t *testing.T) {
	_, handler := testHandler(t)
	adminRequest(handler, "POST", "/vocabulary", `{"term":"exported","notes":["WAL content"]}`, testToken)
	r := adminRequest(handler, "GET", "/database", "", testToken)
	if r.Code != 200 {
		t.Fatalf("export: %d %s", r.Code, r.Body.String())
	}
	if r.Header().Get("Content-Type") != "application/vnd.sqlite3" || !strings.HasPrefix(r.Header().Get("Content-Disposition"), "attachment;") || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing private download headers")
	}
	if !bytes.HasPrefix(r.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatal("not a SQLite file")
	}
	path := filepath.Join(t.TempDir(), "download.sqlite")
	if err := os.WriteFile(path, r.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity, notes string
	if err := backup.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	if err := backup.QueryRow("SELECT notes_json FROM vocabulary_items WHERE term = 'exported'").Scan(&notes); err != nil || !strings.Contains(notes, "WAL content") {
		t.Fatalf("missing committed data: %s %v", notes, err)
	}
	var count int
	if err := backup.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count == 0 {
		t.Fatalf("missing migrations: %d %v", count, err)
	}
}

func TestEmptyAdminTokenDisablesHandler(t *testing.T) {
	store, _ := testHandler(t)
	handler := NewHandler(store, vocabulary.NewService(store, "default", storage.SourceVersion{}), "default", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if r := adminRequest(handler, "GET", "/database", "", testToken); r.Code != 404 {
		t.Fatalf("empty-token handler enabled: %d", r.Code)
	}
}

func TestAdminRequestDeadlineInterruptsStalledBody(t *testing.T) {
	_, handler := testHandler(t)
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()
		defer close(finished)
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "POST /admin/api/vocabulary HTTP/1.1\r\nHost: example.test\r\nAuthorization: Bearer "+testToken+"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("admin deadline did not release a handler blocked reading an incomplete JSON body")
	}
}

func TestAdminSuggestionsPage(t *testing.T) {
	_, handler := testHandler(t)
	empty := adminRequest(handler, "GET", "/suggestions", "", testToken)
	var page storage.AdminSuggestionsPage
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if empty.Code != 200 || page.Rows == nil || len(page.Rows) != 0 || page.Total != 0 {
		t.Fatalf("empty suggestions: %d %s", empty.Code, empty.Body.String())
	}
	created := adminRequest(handler, "POST", "/vocabulary", `{"term":"wringer","customDescription":"a difficult experience"}`, testToken)
	if created.Code != 200 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	response := adminRequest(handler, "GET", "/suggestions?limit=1&offset=0&owner=someone-else", "", testToken)
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || page.Owner != "default" || page.Total != 1 || page.Selectable != 1 ||
		len(page.Rows) != 1 || page.Rows[0].Term != "wringer" || page.Rows[0].Probability != 1 {
		t.Fatalf("suggestions: %d %s", response.Code, response.Body.String())
	}
	if _, err := time.Parse(time.RFC3339Nano, page.GeneratedAt); err != nil {
		t.Fatalf("snapshot timestamp: %v", err)
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		if result := adminRequest(handler, method, "/suggestions", `{}`, testToken); result.Code != http.StatusMethodNotAllowed {
			t.Fatalf("mutable suggestions: %s returned %d", method, result.Code)
		}
	}
}

func TestAdminPersonalInterestPersistsAndRejectsInvalidChangesAtomically(t *testing.T) {
	_, handler := testHandler(t)
	created := adminRequest(handler, "POST", "/vocabulary", `{"term":"wringer","personalInterest":"high","notes":["Keep this note"]}`, testToken)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var saved vocabularyResponse
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	path := "/vocabulary/" + saved.ItemID
	checkInterest := func(want domain.PersonalInterest) {
		t.Helper()
		response := adminRequest(handler, "GET", path, "", testToken)
		var current domain.VocabularyItem
		if response.Code != http.StatusOK {
			t.Fatalf("get: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if current.PersonalInterest != want || current.Usefulness != saved.Usefulness ||
			len(current.Notes) != 1 || current.Notes[0] != "Keep this note" {
			t.Fatalf("persisted preference or unrelated metadata changed: %#v", current)
		}
	}
	checkInterest(domain.PersonalInterestHigh)
	changed := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"personalInterest":"low"}`, saved.Revision), testToken)
	if changed.Code != http.StatusOK {
		t.Fatalf("interest-only update: %d %s", changed.Code, changed.Body.String())
	}
	var updated vocabularyResponse
	if err := json.Unmarshal(changed.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	checkInterest(domain.PersonalInterestLow)
	rejected := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"personalInterest":"urgent","notes":["Must not persist"]}`, updated.Revision), testToken)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid interest update: %d %s", rejected.Code, rejected.Body.String())
	}
	checkInterest(domain.PersonalInterestLow)
	reset := adminRequest(handler, "PATCH", path, fmt.Sprintf(`{"expectedRevision":%d,"personalInterest":"normal"}`, updated.Revision), testToken)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", reset.Code, reset.Body.String())
	}
	checkInterest(domain.PersonalInterestNormal)
}

func TestAdminInvalidPersonalInterestDoesNotCreateVocabulary(t *testing.T) {
	_, handler := testHandler(t)
	response := adminRequest(handler, "POST", "/vocabulary", `{"term":"wringer","personalInterest":"urgent"}`, testToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid interest save: %d %s", response.Code, response.Body.String())
	}
	page := adminRequest(handler, "GET", "/tables/vocabulary_items", "", testToken)
	var result storage.AdminPage
	if page.Code != http.StatusOK {
		t.Fatalf("list: %d %s", page.Code, page.Body.String())
	}
	if err := json.Unmarshal(page.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("invalid save persisted vocabulary: %#v", result)
	}
}

func TestAdminVocabularyRevisionPreconditions(t *testing.T) {
	store, handler := testHandler(t)
	decodeItem := func(response *httptest.ResponseRecorder) vocabularyResponse {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("vocabulary response: %d %s", response.Code, response.Body.String())
		}
		var item vocabularyResponse
		if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		if item.Revision <= 0 {
			t.Fatalf("missing positive admin revision: %s", response.Body.String())
		}
		return item
	}
	saved := decodeItem(adminRequest(handler, "POST", "/vocabulary",
		`{"term":"bank","customDescription":"Keep description","notes":["Keep note"],"tags":["keep"]}`, testToken))
	path := "/vocabulary/" + saved.ItemID
	loaded := decodeItem(adminRequest(handler, "GET", path, "", testToken))
	if loaded.Revision != saved.Revision {
		t.Fatalf("GET revision %d differs from POST %d", loaded.Revision, saved.Revision)
	}
	baseline := adminRequest(handler, "GET", path, "", testToken).Body.String()
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"notes":["must not persist"]}`, http.StatusPreconditionRequired},
		{`{"expectedRevision":null,"notes":["must not persist"]}`, http.StatusBadRequest},
		{`{"expectedRevision":0,"notes":["must not persist"]}`, http.StatusBadRequest},
		{`{"expectedRevision":-1,"notes":["must not persist"]}`, http.StatusBadRequest},
		{`{"expectedRevision":1.5,"notes":["must not persist"]}`, http.StatusBadRequest},
		{`{"expectedRevision":"1","notes":["must not persist"]}`, http.StatusBadRequest},
		{`{"expectedRevision":9223372036854775808,"notes":["must not persist"]}`, http.StatusBadRequest},
	} {
		response := adminRequest(handler, "PATCH", path, tc.body, testToken)
		if response.Code != tc.status {
			t.Fatalf("precondition %s returned %d: %s", tc.body, response.Code, response.Body.String())
		}
		if current := adminRequest(handler, "GET", path, "", testToken).Body.String(); current != baseline {
			t.Fatalf("rejected precondition mutated vocabulary: %s", current)
		}
	}
	first := decodeItem(adminRequest(handler, "PATCH", path,
		fmt.Sprintf(`{"expectedRevision":%d,"tags":["winner"]}`, loaded.Revision), testToken))
	if first.Revision <= loaded.Revision || first.CustomDescription != loaded.CustomDescription ||
		len(first.Notes) != 1 || first.Notes[0] != loaded.Notes[0] {
		t.Fatalf("partial edit lost metadata or did not advance revision: %#v", first)
	}
	baseline = adminRequest(handler, "GET", path, "", testToken).Body.String()
	stale := adminRequest(handler, "PATCH", path,
		fmt.Sprintf(`{"expectedRevision":%d,"notes":["loser"],"customDescription":""}`, loaded.Revision), testToken)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale writer returned %d: %s", stale.Code, stale.Body.String())
	}
	if current := adminRequest(handler, "GET", path, "", testToken).Body.String(); current != baseline {
		t.Fatalf("stale edit changed metadata: %s", current)
	}
	mcp := vocabulary.NewService(store, "default", storage.SourceVersion{Provider: "cambridge", ParserVersion: 1})
	notes := []string{"MCP edit"}
	if _, err := mcp.Update(context.Background(), saved.ItemID, "", vocabulary.UpdateChanges{Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	baseline = adminRequest(handler, "GET", path, "", testToken).Body.String()
	stale = adminRequest(handler, "PATCH", path,
		fmt.Sprintf(`{"expectedRevision":%d,"tags":["stale admin"]}`, first.Revision), testToken)
	if stale.Code != http.StatusConflict {
		t.Fatalf("MCP did not invalidate admin draft: %d %s", stale.Code, stale.Body.String())
	}
	if current := adminRequest(handler, "GET", path, "", testToken).Body.String(); current != baseline {
		t.Fatalf("stale admin overwrote MCP edit: %s", current)
	}
	fresh := decodeItem(adminRequest(handler, "GET", path, "", testToken))
	merged := decodeItem(adminRequest(handler, "PATCH", path,
		fmt.Sprintf(`{"expectedRevision":%d,"tags":["merged"]}`, fresh.Revision), testToken))
	if len(merged.Notes) != 1 || merged.Notes[0] != "MCP edit" || merged.Revision <= fresh.Revision {
		t.Fatalf("fresh partial merge lost MCP metadata: %#v", merged)
	}
	if response := adminRequest(handler, "DELETE", path, "", testToken); response.Code != http.StatusOK {
		t.Fatalf("delete: %d", response.Code)
	}
	recreated := decodeItem(adminRequest(handler, "POST", "/vocabulary", `{"term":"bank","notes":["replacement"]}`, testToken))
	if recreated.ItemID == saved.ItemID {
		t.Fatal("recreation reused the deleted item's identity")
	}
	stale = adminRequest(handler, "PATCH", path,
		fmt.Sprintf(`{"expectedRevision":%d,"notes":["old identity"]}`, merged.Revision), testToken)
	if stale.Code != http.StatusNotFound {
		t.Fatalf("old identity was accepted after recreation: %d %s", stale.Code, stale.Body.String())
	}
	replacement := decodeItem(adminRequest(handler, "GET", "/vocabulary/"+recreated.ItemID, "", testToken))
	if replacement.Revision != recreated.Revision || len(replacement.Notes) != 1 || replacement.Notes[0] != "replacement" {
		t.Fatalf("stale old identity changed replacement: %#v", replacement)
	}
}

func TestAdminVocabularyImageLifecycle(t *testing.T) {
	_, handler := testHandler(t)
	created := adminRequest(handler, http.MethodPost, "/vocabulary", `{"term":"bank"}`, testToken)
	var item vocabularyResponse
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	createdRevision := item.Revision
	image := []byte("\x89PNG\r\n\x1a\nexample image")
	path := "/vocabulary/" + item.ItemID + "/images"
	if response := imageUploadRequest(handler, path, item.Revision, "river-bank.png", "We picnicked on the bank.", image, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized upload returned %d", response.Code)
	}
	uploaded := imageUploadRequest(handler, path, item.Revision, "river-bank.png", "We picnicked on the bank.", image, testToken)
	if uploaded.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", uploaded.Code, uploaded.Body.String())
	}
	if err := json.Unmarshal(uploaded.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Revision <= createdRevision || len(item.Images) != 1 {
		t.Fatalf("upload did not update vocabulary: %#v", item)
	}
	attachment := item.Images[0]
	if attachment.MediaID == "" || attachment.AttachmentID == "" || attachment.ContentType != "image/png" ||
		attachment.ByteSize != int64(len(image)) || attachment.OriginalFilename != "river-bank.png" ||
		attachment.Example != "We picnicked on the bank." {
		t.Fatalf("unexpected image metadata: %#v", attachment)
	}
	mediaPath := "/media/" + attachment.MediaID
	if response := adminRequest(handler, http.MethodGet, mediaPath, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized media read returned %d", response.Code)
	}
	media := adminRequest(handler, http.MethodGet, mediaPath, "", testToken)
	if media.Code != http.StatusOK || media.Header().Get("Content-Type") != "image/png" ||
		media.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" ||
		media.Header().Get("Cache-Control") != "no-store" ||
		!bytes.Equal(media.Body.Bytes(), image) {
		t.Fatalf("stored media response: %d %#v %q", media.Code, media.Header(), media.Body.Bytes())
	}
	inspected := adminRequest(handler, http.MethodGet, "/tables/media_objects", "", testToken)
	var mediaPage storage.AdminPage
	if inspected.Code != http.StatusOK {
		t.Fatalf("inspect media table: %d %s", inspected.Code, inspected.Body.String())
	}
	if err := json.Unmarshal(inspected.Body.Bytes(), &mediaPage); err != nil {
		t.Fatal(err)
	}
	if len(mediaPage.Rows) != 1 || mediaPage.Rows[0]["data"] != fmt.Sprintf("[binary: %d bytes]", len(image)) {
		t.Fatalf("admin table exposed raw media data: %#v", mediaPage.Rows)
	}
	stale := imageUploadRequest(handler, path, createdRevision, "stale.png", "", image, testToken)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale upload returned %d: %s", stale.Code, stale.Body.String())
	}
	current := adminRequest(handler, http.MethodGet, "/vocabulary/"+item.ItemID, "", testToken)
	var unchanged vocabularyResponse
	if err := json.Unmarshal(current.Body.Bytes(), &unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != item.Revision || len(unchanged.Images) != 1 {
		t.Fatalf("stale upload changed the item: %#v", unchanged)
	}
	deleted := adminRequest(handler, http.MethodDelete,
		path+"/"+attachment.AttachmentID, fmt.Sprintf(`{"expectedRevision":%d}`, item.Revision), testToken)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	if err := json.Unmarshal(deleted.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Images) != 0 || item.Revision <= unchanged.Revision {
		t.Fatalf("delete did not update vocabulary: %#v", item)
	}
	if response := adminRequest(handler, http.MethodGet, mediaPath, "", testToken); response.Code != http.StatusNotFound {
		t.Fatalf("unreferenced media remained readable: %d", response.Code)
	}
}

const testToken = "admin-test-token-at-least-32-characters"

func testHandler(t *testing.T) (*storage.DB, http.Handler) {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "live.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := vocabulary.NewService(store, "default", storage.SourceVersion{Provider: "cambridge", ParserVersion: 1})
	return store, NewHandler(store, service, "default", testToken, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func adminRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/admin/api"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func imageUploadRequest(handler http.Handler, path string, revision int64, filename, example string, image []byte, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(image); err != nil {
		panic(err)
	}
	if err := writer.WriteField("example", example); err != nil {
		panic(err)
	}
	if err := writer.WriteField("expectedRevision", fmt.Sprint(revision)); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api"+path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
