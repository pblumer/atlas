package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The orchestration ends only when nothing is coming back.
//
// An entry bundle seen live on 2026-11-11: the package, the licence, and the
// memberships, which need the licence. The first round starts the package and the
// licence together. When the package reported first, the wake asked which lines may
// start, the memberships were still waiting on the licence, and the answer was
// empty — and the orchestration read an empty answer as "nothing more to do" and
// ended. The licence reported a moment later into an order nobody was waiting on,
// and the memberships stayed pending for good. Three orders of eight hit it; the
// other five passed only because the licence happened to settle first.

// waitProvisionBPMN provisions by waiting: a user task nobody completes, so the
// test and not the process decides when a position reports.
const waitProvisionBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs_wait_prov" targetNamespace="http://atlas/test">
  <process id="wait_prov" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <userTask id="t" name="Bereitstellen"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

// TestTheOrchestrationWaitsForALineStillRunning: the third line settles while the
// one the second needs is still running, and the orchestration must still be there
// to start the second once it reports.
func TestTheOrchestrationWaitsForALineStillRunning(t *testing.T) {
	ts, admin := aServerThatCanCallItself(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", waitProvisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}

	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Eintritt"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	for _, id := range []string{"paket", "lizenz", "mitgliedschaften"} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
			`{"id":"`+id+`","homeCatalog":"`+cat+`","state":"active","texts":{"de":"`+id+`"},`+
				`"approval":{"kind":"none"},"provisionProcess":"wait_prov","deprovisionProcess":"wait_prov"}`,
		); code != http.StatusOK {
			t.Fatalf("save %s: %d (%s)", id, code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["paket","lizenz","mitgliedschaften"],`+
			`"edges":[{"from":"mitgliedschaften","to":"lizenz","kind":"requires"}]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["paket","lizenz","mitgliedschaften"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	ord := idOf(t, body)

	// Round one: the package and the licence run, the memberships wait.
	awaitLines(t, admin, ts, ord, "round one started",
		map[string]string{"paket": "running", "lizenz": "running", "mitgliedschaften": "pending"})

	// The package settles first, with the licence still running.
	report(t, admin, ts, ord, "paket", "done")

	// The wake has to be worked before the licence reports, or both reports land
	// before the orchestration asks again and the race this test is about is not
	// run. It has been worked once the orchestration has asked again — or has
	// ended, which is the defect.
	deadline := time.Now().Add(20 * time.Second)
	for {
		vars, state := orchestrationOf(t, admin, ts, ord)
		if state != "" && state != "active" {
			t.Fatalf("the orchestration ended (state %q) while the licence was still running: "+
				"the memberships, which need the licence, will never be started", state)
		}
		if strings.Contains(vars["runde"], "lizenz") && !strings.Contains(vars["runde"], "paket") {
			break // asked again after the package: nothing ready, the licence outstanding
		}
		if time.Now().After(deadline) {
			t.Fatalf("the orchestration never asked again after the package reported: state=%q vars=%v",
				state, vars)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The licence reports, and the memberships it held back are started.
	report(t, admin, ts, ord, "lizenz", "done")
	awaitLines(t, admin, ts, ord, "the memberships started once the licence was provisioned",
		map[string]string{"paket": "done", "lizenz": "done", "mitgliedschaften": "running"})

	// And the last one settling ends it: nothing ready, nothing running.
	report(t, admin, ts, ord, "mitgliedschaften", "done")
	deadline = time.Now().Add(20 * time.Second)
	for {
		if _, state := orchestrationOf(t, admin, ts, ord); state != "" && state != "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the orchestration did not end once every line was provisioned")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// report records a position's outcome the way its provisioning process does.
func report(t *testing.T, c *http.Client, ts *httptest.Server, ord, item, status string) {
	t.Helper()
	if code, b := cReq(t, c, ts, "POST", "/api/v1/orders/"+ord+"/lines/"+item,
		`{"status":"`+status+`"}`); code != http.StatusOK {
		t.Fatalf("report %s %s: %d (%s)", item, status, code, b)
	}
}

// awaitLines polls the order until every named line has the status wanted.
func awaitLines(t *testing.T, c *http.Client, ts *httptest.Server, ord, what string, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+ord, "")
		if code != http.StatusOK {
			t.Fatalf("read the order: %d (%s)", code, body)
		}
		var o struct {
			Lines []struct {
				ItemID string `json:"itemId"`
				Status string `json:"status"`
			} `json:"lines"`
		}
		if err := json.Unmarshal(body, &o); err != nil {
			t.Fatalf("decode the order: %v (%s)", err, body)
		}
		got := map[string]string{}
		for _, l := range o.Lines {
			got[l.ItemID] = l.Status
		}
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
			}
		}
		if match {
			return
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, c, ts, "GET", "/api/v1/incidents", "")
			vars, state := orchestrationOf(t, c, ts, ord)
			t.Fatalf("%s: lines = %v, want %v\norchestration: state=%q vars=%v\nincidents: %s",
				what, got, want, state, vars, inc)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
