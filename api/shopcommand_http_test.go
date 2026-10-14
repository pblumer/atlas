package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// commanderBPMN is a process that does not carry the tool and commands one of its
// actions: the order comes from a variable, the position is a literal, the command id
// lands in `asked`, and a refusal is an incident at once.
func commanderBPMN(id, action string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="S"/>
    <sendTask id="Command"><extensionElements><atlas:shopTask mode="command" product="tool" action="` + action +
		`" order="= orderRef" position="tool" resultVariable="asked" retries="1"/></extensionElements></sendTask>
    <endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="Command"/>
    <sequenceFlow id="f2" sourceRef="Command" targetRef="E"/>
  </process>
</definitions>`
}

// anApplication creates an application and answers its id and its portable key.
func anApplication(t *testing.T, admin *http.Client, ts *httptest.Server, name string) (string, string) {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/applications", `{"name":"`+name+`"}`)
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &app) != nil || app.ID == "" || app.Key == "" {
		t.Fatalf("create application: %d (%s)", code, body)
	}
	return app.ID, app.Key
}

// commandIncidents are the messages of the incidents raised in process id.
func commandIncidents(t *testing.T, admin *http.Client, ts *httptest.Server, id string) []string {
	t.Helper()
	code, body := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
	var page struct {
		Items []struct {
			ProcessID string `json:"processId"`
			Message   string `json:"message"`
		} `json:"items"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &page) != nil {
		t.Fatalf("incidents: %d (%s)", code, body)
	}
	var out []string
	for _, in := range page.Items {
		if in.ProcessID == id {
			out = append(out, in.Message)
		}
	}
	return out
}

// listToolFor publishes the tool again with the given applications allowed to
// command it.
func listToolFor(t *testing.T, admin *http.Client, ts *httptest.Server, keys string) {
	t.Helper()
	code, body := cReq(t, admin, ts, "GET", "/api/v1/catalogs", "")
	var cats []struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &cats) != nil || len(cats) != 1 {
		t.Fatalf("catalogues: %d (%s)", code, body)
	}
	cat := cats[0].ID
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"tool","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Werkzeug"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"tool-strand","lifecycleForm":"per-position",`+
			`"actions":`+toolActions(`["customer","operator"]`)+`,"commandedBy":`+keys+`}`); code != http.StatusOK {
		t.Fatalf("save: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", ""); code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, b)
	}
}

// TestAProcessCommandsAnActionInTheNameOfItsApplication: a process that does not
// carry a position asks it for an action through the order act — but only in the
// name of an application the product's newest release lists, only for an action an
// operator or a system may ask for, and never for the provision. A return it
// commands is the order's return, and commanding it again while it is under way
// changes nothing.
func TestAProcessCommandsAnActionInTheNameOfItsApplication(t *testing.T) {
	ts, admin, _, _, ord, strand := aToolHeldForRita(t, `["customer","operator"]`)
	appID, appKey := anApplication(t, admin, ts, "HR Leavers")
	for _, c := range []struct{ id, action string }{
		{"leaver-audit", "audit"}, {"leaver-reset", "password-reset"},
		{"leaver-provision", "provision"}, {"leaver-return", "deprovision"},
	} {
		if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments?projectId="+appID, "application/xml",
			commanderBPMN(c.id, c.action)); code != http.StatusOK {
			t.Fatalf("deploy %s: %d (%s)", c.id, code, b)
		}
	}
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml",
		commanderBPMN("stray-audit", "audit")); code != http.StatusOK {
		t.Fatalf("deploy stray: %d (%s)", code, b)
	}
	startFor := func(id, orderRef string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances",
			`{"processId":"`+id+`","variables":{"orderRef":"`+orderRef+`"}}`); code != http.StatusOK {
			t.Fatalf("start %s: %d (%s)", id, code, b)
		}
	}
	start := func(id string) { t.Helper(); startFor(id, ord) }
	refusedWith := func(id, want string) int {
		t.Helper()
		got := commandIncidents(t, admin, ts, id)
		for _, m := range got {
			if strings.Contains(m, want) {
				return len(got)
			}
		}
		t.Fatalf("%s raised %q, want an incident naming %q", id, got, want)
		return 0
	}

	// Not listed yet: nothing may command the tool.
	start("leaver-audit")
	before := refusedWith("leaver-audit", "does not list application "+appKey)

	listToolFor(t, admin, ts, `["`+appKey+`"]`)
	start("leaver-audit")
	_, outcomes, body := outcomesOf(t, admin, ts, ord, "tool")
	var audit map[string]any
	for id, o := range outcomes {
		if strings.HasPrefix(id, "task-") && o["action"] == "audit" {
			audit = o
		}
	}
	if audit == nil || audit["outcome"] != "completed" || uint64(audit["instanceKey"].(float64)) != strand {
		t.Fatalf("the commanded audit ended %v (%s), want completed by the strand", audit, body)
	}

	start("leaver-reset")
	refusedWith("leaver-reset", "a process commands only what an operator or a system may ask for")
	start("leaver-provision")
	refusedWith("leaver-provision", "the provision action is the order's own")
	start("stray-audit")
	refusedWith("stray-audit", "belongs to no application")
	startFor("leaver-audit", "ord_nope")
	refusedWith("leaver-audit", "no order ord_nope")
	startFor("leaver-audit", "")
	refusedWith("leaver-audit", "it needs both")

	start("leaver-return")
	if got := commandIncidents(t, admin, ts, "leaver-return"); len(got) != 0 {
		t.Fatalf("the commanded return raised %q", got)
	}
	code, b := cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord, "")
	if code != http.StatusOK || !strings.Contains(string(b), `"status":"returning"`) {
		t.Fatalf("after the commanded return the order is %d (%s), want the tool returning", code, b)
	}
	// Again while it is under way: what the command wants is already happening.
	start("leaver-return")
	if got := commandIncidents(t, admin, ts, "leaver-return"); len(got) != 0 {
		t.Fatalf("a second commanded return raised %q", got)
	}
	// A right on its way back takes no other action.
	start("leaver-audit")
	refusedWith("leaver-audit", "is returning; an action is asked only of a held right")

	// Taken off the list in a newer release, the application commands nothing more.
	before = len(commandIncidents(t, admin, ts, "leaver-audit"))
	listToolFor(t, admin, ts, `[]`)
	start("leaver-audit")
	if after := refusedWith("leaver-audit", "does not list application "+appKey); after != before+1 {
		t.Fatalf("an application taken off the list raised %d incident(s) in all, want %d", after, before+1)
	}
}
