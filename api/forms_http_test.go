package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestFormCRUD(t *testing.T) {
	ts := newTestServer(t)

	schema := `{"type":"default","components":[{"type":"textfield","key":"comment","label":"Comment"}]}`
	// Create.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms",
		`{"id":"review-form","name":"Review","schema":`+schema+`}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("save form: status=%d body=%s", code, body)
	}
	var meta struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		SavedAt int64  `json:"savedAt"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("decode meta: %v (%s)", err, body)
	}
	if meta.ID != "review-form" || meta.Name != "Review" {
		t.Fatalf("meta = %+v, want id=review-form name=Review", meta)
	}

	// List returns metadata only (no schema field).
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/forms", "", "")
	if code != http.StatusOK {
		t.Fatalf("list forms: status=%d body=%s", code, body)
	}
	var list []struct {
		ID     string          `json:"id"`
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	if len(list) != 1 || list[0].ID != "review-form" {
		t.Fatalf("list = %+v, want one review-form", list)
	}
	if len(list[0].Schema) != 0 {
		t.Errorf("list should omit the schema, got %s", list[0].Schema)
	}

	// Get returns the full form with the schema as a JSON document.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/forms/review-form", "", "")
	if code != http.StatusOK {
		t.Fatalf("get form: status=%d body=%s", code, body)
	}
	var full struct {
		ID     string `json:"id"`
		Schema struct {
			Type       string `json:"type"`
			Components []struct {
				Key string `json:"key"`
			} `json:"components"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatalf("decode full: %v (%s)", err, body)
	}
	if full.Schema.Type != "default" || len(full.Schema.Components) != 1 || full.Schema.Components[0].Key != "comment" {
		t.Fatalf("schema not round-tripped: %s", body)
	}

	// Overwrite (same id) then delete.
	code, _ = doReq(t, ts, http.MethodPost, "/api/v1/forms",
		`{"id":"review-form","schema":{"type":"default","components":[]}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("overwrite form: status=%d", code)
	}
	code, _ = doReq(t, ts, http.MethodDelete, "/api/v1/forms/review-form", "", "")
	if code != http.StatusOK {
		t.Fatalf("delete form: status=%d", code)
	}
	code, _ = doReq(t, ts, http.MethodGet, "/api/v1/forms/review-form", "", "")
	if code != http.StatusNotFound {
		t.Errorf("get after delete: %d, want 404", code)
	}
}

func TestSaveFormValidation(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name, body string
	}{
		{"malformed json", `not-json`},
		{"missing id", `{"schema":{"type":"default"}}`},
		{"missing schema", `{"id":"x"}`},
		{"invalid schema json", `{"id":"x","schema":"not-an-object-but-still"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _ := doReq(t, ts, http.MethodPost, "/api/v1/forms", c.body, "application/json")
			if code != http.StatusBadRequest {
				t.Errorf("%s: status=%d, want 400", c.name, code)
			}
		})
	}
}
