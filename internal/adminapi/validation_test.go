package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAdminRejectsMalformedUTF8BeforeDecoding(t *testing.T) {
	_, handler := testHandler(t)
	for _, body := range []string{
		"{\"term\":\"\xff\"}",
		"{\"term\":\"bank\",\"context\":\"\xfe\"}",
		`{"term":"bank","notes":["` + strings.Repeat("x", 1<<20) + `"]}`,
	} {
		response := adminRequest(handler, "POST", "/vocabulary", body, testToken)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid body accepted: %d %s", response.Code, response.Body.String())
		}
	}
	page := adminRequest(handler, "GET", "/tables/vocabulary_items", "", testToken)
	if !strings.Contains(page.Body.String(), `"total":0`) {
		t.Fatalf("invalid saves persisted: %s", page.Body.String())
	}

	// U+FFFD itself is valid input; only malformed byte sequences are rejected.
	created := adminRequest(handler, "POST", "/vocabulary", `{"term":"bank","notes":["valid replacement �"]}`, testToken)
	var saved vocabularyResponse
	if created.Code != 200 {
		t.Fatal(created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	path := "/vocabulary/" + saved.ItemID
	response := adminRequest(handler, "PATCH", path, fmt.Sprintf("{\"expectedRevision\":%d,\"notes\":[\"\xff\"]}", saved.Revision), testToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed update accepted: %d", response.Code)
	}
	current := adminRequest(handler, "GET", path, "", testToken)
	var loaded vocabularyResponse
	if err := json.Unmarshal(current.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != saved.Revision || len(loaded.Notes) != 1 || loaded.Notes[0] != saved.Notes[0] {
		t.Fatalf("malformed update changed metadata: %#v", loaded)
	}
}

func TestAdminRejectsNULText(t *testing.T) {
	_, handler := testHandler(t)
	for _, field := range []string{
		`"term":"before\u0000after"`, `"term":"bank","context":"\u0000"`,
		`"term":"bank","customDescription":"before\u0000after"`,
		`"term":"bank","tags":["before\u0000after"]`,
		`"term":"bank","notes":["before\u0000after"]`,
		`"term":"bank","examples":["before\u0000after"]`,
		`"term":"bank","customDescription":"valid","descriptionSource":{"title":"\u0000"}`,
	} {
		response := adminRequest(handler, "POST", "/vocabulary", "{"+field+"}", testToken)
		if response.Code != http.StatusBadRequest {
			t.Errorf("NUL input accepted: %d %s", response.Code, response.Body.String())
		}
	}
	page := adminRequest(handler, "GET", "/tables/learning_cards", "", testToken)
	if !strings.Contains(page.Body.String(), `"total":0`) {
		t.Fatalf("NUL input created cards: %s", page.Body.String())
	}
}
