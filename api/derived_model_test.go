package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The derived information model, end to end
// (ADR-0301, §3).
//
// The derived model is what is *built* — read off the application's processes. What a
// person models by hand is a different statement, a target, and nothing here writes
// into it. These tests are about the read arriving whole and saying what it could not
// see; the derivation's own rules are held in api/infomodel.

type derivedResp struct {
	Model struct {
		Classes []struct {
			Name       string   `json:"name"`
			Identity   []string `json:"identity"`
			Attributes []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"attributes"`
			Lifecycle *struct {
				States []struct {
					Name    string `json:"name"`
					Initial bool   `json:"initial"`
				} `json:"states"`
				Transitions []struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"transitions"`
			} `json:"lifecycle"`
		} `json:"classes"`
	} `json:"model"`
	Gaps []struct {
		Class string `json:"class"`
		Kind  string `json:"kind"`
		Note  string `json:"note"`
	} `json:"gaps"`
}

func TestDerivedModelReadsTheApplicationsProcesses(t *testing.T) {
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	startLifecycleInstance(t, ts, appID) // deploys the fixture under the application

	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/derived?applicationId="+appID, "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var d derivedResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	byName := map[string]int{}
	for i, c := range d.Model.Classes {
		byName[c.Name] = i
	}
	// The fixture carries an order and a buyer; both are read from what the processes
	// declare, with no authored model involved.
	for _, want := range []string{"Order", "Customer"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("no derived class %q; got %v", want, byName)
		}
	}
	order := d.Model.Classes[byName["Order"]]
	if order.Lifecycle == nil {
		t.Fatal("the order has no derived lifecycle")
	}
	states := map[string]bool{}
	for _, s := range order.Lifecycle.States {
		states[s.Name] = true
	}
	// received → approved → shipped is what the fixture process actually does, and it
	// is read off the writes rather than off the model that happens to declare it.
	for _, want := range []string{"received", "approved", "shipped"} {
		if !states[want] {
			t.Errorf("state %q missing from the derived lifecycle; got %v", want, states)
		}
	}
	// `cancelled` is in the authored model and no process reaches it. The derived
	// model is what is built, so it is absent — which is the whole point of the pair.
	if states["cancelled"] {
		t.Error("`cancelled` is derived, but nothing in the application ever writes it")
	}
}

func TestDerivedModelIsKeylessAndSaysWhy(t *testing.T) {
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	startLifecycleInstance(t, ts, appID)

	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/derived?applicationId="+appID, "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var d derivedResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range d.Model.Classes {
		if len(c.Identity) != 0 {
			t.Errorf("%s: identity = %v — nothing in BPMN says which attribute identifies a thing", c.Name, c.Identity)
		}
	}
	kinds := map[string]bool{}
	for _, g := range d.Gaps {
		kinds[g.Kind] = true
	}
	// The reading qualifies itself where it draws. Without this it would be a picture
	// mistaken for a complete one, which is worse than no picture.
	for _, want := range []string{"no-business-key", "no-attribute-types"} {
		if !kinds[want] {
			t.Errorf("gap %q is missing; got %v", want, kinds)
		}
	}
}

func TestDerivedModelOfAnApplicationWithNoProcessesIsEmpty(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Leer"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/derived?applicationId="+app.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var d derivedResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(d.Model.Classes) != 0 {
		t.Errorf("classes = %d, want none", len(d.Model.Classes))
	}
	// No drawing, so nothing to qualify: the sentences exist to warn about a picture.
	if len(d.Gaps) != 0 {
		t.Errorf("gaps = %+v, want none", d.Gaps)
	}
}

func TestDerivedModelNeedsAnApplication(t *testing.T) {
	ts := newTestServer(t)
	// Derivation is per application by construction — one application's processes are
	// what imply one vocabulary. Without one there is no set to read.
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/derived", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestDerivedModelIsNeverWrittenAnywhere(t *testing.T) {
	// The decision this record turns on: the derived model is a reading, and the
	// authored one is a different statement. Reading the first must not touch the
	// second — so the authored model is exactly as it was, revision included.
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	startLifecycleInstance(t, ts, appID)

	before := readModelsOf(t, ts, appID)
	if code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/derived?applicationId="+appID, "", ""); code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	after := readModelsOf(t, ts, appID)
	if before != after {
		t.Errorf("the authored models changed:\n before %s\n after  %s", before, after)
	}
}

// readModelsOf returns the application's authored models as raw JSON, so any change
// to any of them — a class, a revision, a timestamp — shows as a difference.
func readModelsOf(t *testing.T, ts *httptest.Server, appID string) string {
	t.Helper()
	_, list := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models?applicationId="+appID, "", "")
	var models []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(list, &models); err != nil {
		t.Fatalf("decode models: %v (%s)", err, list)
	}
	out := ""
	for _, m := range models {
		_, body := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models/"+m.ID, "", "")
		out += fmt.Sprintf("%s=%s\n", m.ID, body)
	}
	return out
}
