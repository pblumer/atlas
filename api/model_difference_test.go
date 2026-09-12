package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The difference between what an application builds and what it plans, end to end
// (ADR-0310).
//
// The rules of the comparison are held in api/infomodel. These are about the read
// arriving whole, refusing what it cannot answer, and writing nothing.

type diffResp struct {
	Planned []struct {
		Side, Kind, Class, Name, From, To, Note string
	} `json:"planned"`
	Built []struct {
		Side, Kind, Class, Name, Note string
	} `json:"built"`
	Excluded []string `json:"excluded"`
	Modeled  bool     `json:"modeled"`
}

func TestDifferenceReadsOneApplicationsTwoStatements(t *testing.T) {
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	startLifecycleInstance(t, ts, appID)

	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/difference?applicationId="+appID, "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var d diffResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !d.Modeled {
		t.Fatal("the fixture authors a model, and the reading says it does not")
	}
	// The fixture's model declares `cancelled` and nothing in the application ever
	// writes it — the backlog row this whole feature exists to produce.
	found := false
	for _, f := range d.Planned {
		if f.Class == "Order" && f.Name == "cancelled" {
			found = true
			if f.Side != "planned-not-built" {
				t.Errorf("side = %q, want planned-not-built", f.Side)
			}
		}
	}
	if !found {
		t.Errorf("`cancelled` is modelled and unbuilt, and is not on the backlog: %+v", d.Planned)
	}
	// And the reading says what it never looked at, so a short list is not read as a
	// clean bill.
	if len(d.Excluded) == 0 {
		t.Error("the reading names none of its exclusions")
	}
}

func TestDifferenceAgainstNoModelIsSilent(t *testing.T) {
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
		"/api/v1/infomodel/difference?applicationId="+app.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var d diffResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Nothing has been planned, so nothing is missing from the plan.
	if d.Modeled || len(d.Planned) != 0 || len(d.Built) != 0 {
		t.Errorf("an application with no model produced a backlog: %+v", d)
	}
}

func TestDifferenceNeedsAnApplication(t *testing.T) {
	ts := newTestServer(t)
	// The comparison is between one application's processes and its own model; without
	// one there is no pair to read.
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/difference", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestDifferenceIsNeverWrittenAnywhere(t *testing.T) {
	// The posture ADR-0301 set and this record keeps: a reading, to neither document.
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	startLifecycleInstance(t, ts, appID)

	before := readModelsOf(t, ts, appID)
	if code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/difference?applicationId="+appID, "", ""); code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if after := readModelsOf(t, ts, appID); before != after {
		t.Errorf("the authored models changed:\n before %s\n after  %s", before, after)
	}
}
