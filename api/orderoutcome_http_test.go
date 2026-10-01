package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// outcomesOf reads how a position's commands ended, by command id.
func outcomesOf(t *testing.T, c *http.Client, ts *httptest.Server, ord, item string) (int, map[string]map[string]any, string) {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+ord+"/lines/"+item+"/outcomes", "")
	out := map[string]map[string]any{}
	if code != http.StatusOK {
		return code, out, string(body)
	}
	var resp struct {
		Outcomes []map[string]any `json:"outcomes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode outcomes: %v (%s)", err, body)
	}
	for _, o := range resp.Outcomes {
		out[o["commandId"].(string)] = o
	}
	return code, out, string(body)
}

// TestAnActionsEndingIsAFact: the act hands the process its command id; the process
// reports how the command ended, once — the same report again answers with the
// first, a different ending is refused — under the event type the action is
// published as; and the person who holds the right reads it back beside how the
// provision ended.
func TestAnActionsEndingIsAFact(t *testing.T) {
	ts, admin, rita, _, ord, strand := aToolHeldForRita(t, `["customer","operator"]`)
	if code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/password-reset",
		`{"commandId":"r-1","reason":"locked out"}`); code != http.StatusOK {
		t.Fatalf("ask: %d (%s)", code, b)
	}
	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, `"commandId"`) || !strings.Contains(vars, `"r-1"`) {
		t.Fatalf("the strand was not told which command it carries: %s", vars)
	}

	outcome := "/api/v1/orders/" + ord + "/lines/tool/actions/r-1/outcome"
	code, body := cReq(t, admin, ts, "POST", outcome, `{"outcome":"completed","result":{"ticket":"INC-1","minutes":3}}`)
	var got struct {
		EventType string         `json:"eventType"`
		Action    string         `json:"action"`
		Source    string         `json:"source"`
		Replayed  bool           `json:"replayed"`
		Result    map[string]any `json:"result"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.EventType != "tool.reset.completed" ||
		got.Action != "password-reset" || got.Source != "atlas:rest" || got.Replayed || got.Result["ticket"] != "INC-1" {
		t.Fatalf("report: %d (%s), want recorded as tool.reset.completed", code, body)
	}
	code, body = cReq(t, admin, ts, "POST", outcome, `{"outcome":"completed"}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"replayed":true`) || !strings.Contains(string(body), "INC-1") {
		t.Fatalf("the same report again: %d (%s), want the first, replayed", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", outcome, `{"outcome":"failed"}`); code != http.StatusConflict || !strings.Contains(string(b), "already ended completed") {
		t.Fatalf("a different ending: %d (%s), want 409", code, b)
	}

	code, outcomes, body2 := outcomesOf(t, rita, ts, ord, "tool")
	if code != http.StatusOK || outcomes["r-1"]["outcome"] != "completed" {
		t.Fatalf("rita reads: %d (%s), want the reset completed", code, body2)
	}
	if p := outcomes["order:"+ord+":tool:provision:1"]; p == nil || p["outcome"] != "completed" ||
		p["eventType"] != "tool.provision.completed" || p["source"] != "atlas:order" {
		t.Fatalf("the provision's ending = %v (%s), want completed beside its grant", p, body2)
	}
}

// TestAnOutcomeReportIsCheckedBeforeItIsWritten: only an operator reports; the
// ending comes from the closed set; the result is a bounded object of scalars; the
// command is one the position took; and the provision and the return are reported
// through the line's own route, which records the right they change.
func TestAnOutcomeReportIsCheckedBeforeItIsWritten(t *testing.T) {
	ts, admin, rita, _, ord, _ := aToolHeldForRita(t, `["customer","operator"]`)
	if code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/password-reset",
		`{"commandId":"r-1"}`); code != http.StatusOK {
		t.Fatalf("ask: %d (%s)", code, b)
	}
	outcome := "/api/v1/orders/" + ord + "/lines/tool/actions/r-1/outcome"
	if code, b := cReq(t, rita, ts, "POST", outcome, `{"outcome":"completed"}`); code != http.StatusForbidden {
		t.Fatalf("the holder reports: %d (%s), want 403", code, b)
	}
	for body, want := range map[string]string{
		`{"outcome":"done"}`:                                                         "outcome must be one of",
		`{"outcome":"completed","result":[1,2]}`:                                     "result must be a JSON object",
		`{"outcome":"completed","result":{"nested":{"a":1}}}`:                        "is not a scalar",
		`{"outcome":"completed","result":{"x":"` + strings.Repeat("a", 5000) + `"}}`: "larger than 4 KiB",
	} {
		if code, b := cReq(t, admin, ts, "POST", outcome, body); code != http.StatusBadRequest || !strings.Contains(string(b), want) {
			t.Fatalf("%.60s: %d (%s), want 400 naming %q", body, code, b, want)
		}
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/nope/outcome",
		`{"outcome":"completed"}`); code != http.StatusNotFound || !strings.Contains(string(b), "took no command nope") {
		t.Fatalf("an unknown command: %d (%s), want 404", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/order:"+ord+":tool:provision:1/outcome",
		`{"outcome":"failed"}`); code != http.StatusConflict || !strings.Contains(string(b), "reported through") {
		t.Fatalf("the provision through this route: %d (%s), want 409", code, b)
	}
	if _, outcomes, body := outcomesOf(t, admin, ts, ord, "tool"); outcomes["r-1"] != nil {
		t.Fatalf("a refused report was written: %s", body)
	}
}

// TestAReturnEndsBesideItsRevocation: a return reported done writes its outcome with
// the revocation, under the attempt the return was delivered as.
func TestAReturnEndsBesideItsRevocation(t *testing.T) {
	ts, admin, rita, _, ord, _ := aToolHeldForRita(t, `["customer","operator"]`)
	if code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/return", ""); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool", `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report returned: %d (%s)", code, b)
	}
	_, outcomes, body := outcomesOf(t, admin, ts, ord, "tool")
	ret := outcomes["order:"+ord+":tool:deprovision:1"]
	if ret == nil || ret["outcome"] != "completed" || ret["action"] != "deprovision" || ret["eventType"] != "tool.deprovision.completed" {
		t.Fatalf("the return's ending = %v (%s), want completed beside the revocation", ret, body)
	}
}
