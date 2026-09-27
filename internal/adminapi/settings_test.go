package adminapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"english-learning-mcp/internal/settings"
)

func TestAlgorithmSettingsAuthorizationValidationAndConflicts(t *testing.T) {
	_, handler := testHandler(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, token := range []string{"", "wrong-token"} {
			if response := adminRequest(handler, method, "/settings", `{}`, token); response.Code != http.StatusUnauthorized {
				t.Fatalf("%s unauthorized status=%d", method, response.Code)
			}
		}
	}
	get := func() settingsResponse {
		t.Helper()
		response := adminRequest(handler, http.MethodGet, "/settings", "", testToken)
		if response.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
		}
		var result settingsResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	initial := get()
	if initial.Revision != 0 || !reflect.DeepEqual(initial.Values, settings.Defaults()) || !reflect.DeepEqual(initial.Defaults, settings.Defaults()) {
		t.Fatalf("initial = %#v", initial)
	}
	values := initial.Values
	values.FastAnswerSeconds = 12
	values.LearningStepsMinutes = []float64{}
	body, err := json.Marshal(map[string]any{"values": values, "expectedRevision": 0})
	if err != nil {
		t.Fatal(err)
	}
	response := adminRequest(handler, http.MethodPut, "/settings", string(body), testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}
	saved := get()
	if saved.Revision != 1 || !reflect.DeepEqual(saved.Values, values) {
		t.Fatalf("saved = %#v", saved)
	}
	if response := adminRequest(handler, http.MethodPut, "/settings", string(body), testToken); response.Code != http.StatusConflict {
		t.Fatalf("stale update status=%d", response.Code)
	}
	encodedValues, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{}`, http.StatusPreconditionRequired},
		{`{"values":` + string(encodedValues) + `,"expectedRevision":null}`, http.StatusPreconditionRequired},
		{`{"values":null,"expectedRevision":1}`, http.StatusBadRequest},
		{`{"values":{},"expectedRevision":1}`, http.StatusBadRequest},
		{`{"values":` + string(encodedValues) + `,"expectedRevision":-1}`, http.StatusBadRequest},
		{`{"values":` + string(encodedValues) + `,"expectedRevision":1.5}`, http.StatusBadRequest},
		{`{"values":{"unsupported":true},"expectedRevision":1}`, http.StatusBadRequest},
	} {
		if response := adminRequest(handler, http.MethodPut, "/settings", test.body, testToken); response.Code != test.status {
			t.Fatalf("%s: status=%d body=%s", test.body, response.Code, response.Body.String())
		}
	}
	if got := get(); !reflect.DeepEqual(got, saved) {
		t.Fatalf("failed updates changed settings: %#v", got)
	}
}
