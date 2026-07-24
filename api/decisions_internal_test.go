package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListDecisions covers the decision catalog the Modeler's picker reads: once a
// DMN reference to the seeded dish model exists, the endpoint lists its decision
// with the inputs and output the panel auto-fills from (ADR-0050).
func TestListDecisions(t *testing.T) {
	srv, _ := newValidateServer(t) // seeds dmn-models/dish.dmn (Season → Dish)
	h := srv.Handler()

	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	// With no reference yet, the catalog is empty (but a valid, non-null array).
	if code, b := do(http.MethodGet, "/api/v1/decisions", ""); code != http.StatusOK || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("decisions before any ref = %d %s, want 200 []", code, b)
	}

	// Add a reference to the dish model.
	if code, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}

	code, b := do(http.MethodGet, "/api/v1/decisions", "")
	if code != http.StatusOK {
		t.Fatalf("list decisions: %d %s", code, b)
	}
	var got []decisionCatalogItem
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("decisions = %d, want 1: %s", len(got), b)
	}
	d := got[0]
	if d.ID != "Dish" || d.ModelRef != "dish" {
		t.Errorf("decision = %+v, want id Dish from model dish", d)
	}
	if len(d.Inputs) != 1 || d.Inputs[0].Name != "Season" {
		t.Errorf("inputs = %+v, want one Season", d.Inputs)
	}
	if d.Output.Name != "Dish" {
		t.Errorf("output = %+v, want Dish", d.Output)
	}

	// A second reference to the same model is de-duplicated: the decision is listed
	// once, not twice.
	if code, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish again","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("create second ref: %d %s", code, b)
	}
	code, b = do(http.MethodGet, "/api/v1/decisions", "")
	var again []decisionCatalogItem
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code != http.StatusOK || len(again) != 1 {
		t.Fatalf("after a duplicate ref: %d, %d decisions, want 200 and 1 (deduped)", code, len(again))
	}

	// A reference to a model that does not resolve contributes nothing rather than
	// failing the whole catalog.
	if code, _ := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Ghost","modelRef":"ghost"}`); code != http.StatusOK {
		t.Fatalf("create ghost ref: %d", code)
	}
	if code, b := do(http.MethodGet, "/api/v1/decisions", ""); code != http.StatusOK {
		t.Fatalf("list with an unresolvable ref = %d %s, want 200 (ghost skipped)", code, b)
	}
}
