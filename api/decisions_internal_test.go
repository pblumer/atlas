package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// approvalServiceDMN publishes a decision service over two decisions. Verdict is what
// the service answers with, Score a step on the way, Amount what a caller supplies.
const approvalServiceDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Approval" namespace="http://atlas/dmn">
  <decisionService id="svc" name="Approval">
    <variable name="Approval" typeRef="string"/>
    <outputDecision href="#verdict"/>
    <encapsulatedDecision href="#score"/>
    <inputData href="#amount"/>
  </decisionService>
  <inputData id="amount" name="Amount">
    <variable name="Amount" typeRef="number"/>
  </inputData>
  <decision id="score" name="Score">
    <variable name="Score" typeRef="number"/>
    <informationRequirement><requiredInput href="#amount"/></informationRequirement>
    <literalExpression id="ls"><text>Amount / 1000</text></literalExpression>
  </decision>
  <decision id="verdict" name="Verdict">
    <variable name="Verdict" typeRef="string"/>
    <informationRequirement><requiredDecision href="#score"/></informationRequirement>
    <literalExpression id="lv"><text>if Score &gt; 1 then "refer" else "approve"</text></literalExpression>
  </decision>
</definitions>`

// TestListDecisionsScopedCarriesTheService proves the one thing a business rule task
// is meant to call is reachable where an author looks for it.
//
// A decision service reached the catalog only by being deployed, which carries no
// model handle and belongs to no application. The application-scoped listing is
// built from references, so the service was missing from it — and the picker, which
// separates "this application" from the rest by exactly that listing, could only
// file it under the rest, below every decision it is made of.
func TestListDecisionsScopedCarriesTheService(t *testing.T) {
	srv, dir := newValidateServer(t)
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "approval.dmn"), []byte(approvalServiceDMN), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
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

	code, b := do(http.MethodPost, "/api/v1/projects", `{"name":"Lending"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, b)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &app); err != nil || app.ID == "" {
		t.Fatalf("decode application: %v %s", err, b)
	}
	if code, b := do(http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Approval","modelRef":"approval","projectId":"`+app.ID+`"}`); code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}

	code, b = do(http.MethodGet, "/api/v1/decisions?projectId="+app.ID, "")
	if code != http.StatusOK {
		t.Fatalf("scoped catalog: %d %s", code, b)
	}
	var got []decisionCatalogItem
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var svc *decisionCatalogItem
	for i := range got {
		if got[i].ID == "Approval" {
			svc = &got[i]
		}
	}
	if svc == nil {
		t.Fatalf("the application's catalog does not offer the decision service: %s", b)
	}
	if !svc.Service {
		t.Errorf("Approval is offered but not marked as a decision service: %+v", *svc)
	}
	// The handle is what puts it in an application and what the editor opens it by;
	// arriving without one is exactly how it ended up outside both.
	if svc.ModelRef != "approval" {
		t.Errorf("service model handle = %q, want approval", svc.ModelRef)
	}
	if strings.Join(svc.Members, ",") != "Verdict,Score" {
		t.Errorf("service members = %v, want [Verdict Score]", svc.Members)
	}
	// Its decisions stay on offer beside it: calling one directly remains legal, and
	// the members above are what lets the picker say which is which.
	names := map[string]bool{}
	for _, d := range got {
		names[d.ID] = true
	}
	if !names["Score"] || !names["Verdict"] {
		t.Errorf("catalog = %v, want the service's decisions offered too", names)
	}
}
