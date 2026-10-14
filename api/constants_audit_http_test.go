package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

type constantAuditEvent struct {
	ApplicationID   string `json:"applicationId"`
	ApplicationName string `json:"applicationName"`
	ActorName       string `json:"actorName"`
	Action          string `json:"action"`
	Constants       []struct {
		Name     string `json:"name"`
		Change   string `json:"change"`
		From     string `json:"from"`
		FromType string `json:"fromType"`
		To       string `json:"to"`
		ToType   string `json:"toType"`
	} `json:"constants"`
}

// A change to the constants an application or the installation holds is in the audit log
// with who made it and what each name was and became (ADR-0456,
// ADR-0184): an administrator can answer what PROD ran with, and since when. A save that
// changes nothing leaves no record.
func TestConstantChangesAreInTheAuditLog(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")

	put := func(path, body string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "PUT", path, body); code != http.StatusOK {
			t.Fatalf("put %s: %d %s", path, code, b)
		}
	}
	put("/api/v1/server/constants", `{"constants":[{"name":"TENANT_ID","value":"contoso-int"},{"name":"REGION","value":"ch"}]}`)
	put("/api/v1/server/constants", `{"constants":[{"name":"TENANT_ID","value":"contoso-prod"},{"name":"MAX","type":"number","value":"20"}]}`)
	put("/api/v1/server/constants", `{"constants":[{"name":"TENANT_ID","value":"contoso-prod"},{"name":"MAX","type":"number","value":"20"}]}`) // no change

	_, pb := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"UCC"}`)
	appID := decodeProject(t, pb).ID
	put("/api/v1/applications/"+appID+"/constants", `{"constants":[{"name":"CLIENT_ID","value":"app-1"}]}`)

	code, body := cReq(t, admin, ts, "GET", "/api/v1/audit?action=constants", "")
	if code != http.StatusOK {
		t.Fatalf("audit: %d %s", code, body)
	}
	var page struct {
		Items []constantAuditEvent `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode audit: %v (%s)", err, body)
	}
	if len(page.Items) != 3 {
		t.Fatalf("constants events = %d, want 3 — the unchanged save leaves none (%s)", len(page.Items), body)
	}
	// Events written within one second have no order among them, so each is found by
	// what it records rather than by its position.
	type change struct{ Name, Change, From, FromType, To, ToType string }
	changesOf := func(e constantAuditEvent) []change {
		var got []change
		for _, c := range e.Constants {
			got = append(got, change{c.Name, c.Change, c.From, c.FromType, c.To, c.ToType})
		}
		return got
	}
	want := map[string][]change{
		"app": {{"CLIENT_ID", "added", "", "", "app-1", "string"}},
		"first": {
			{"REGION", "added", "", "", "ch", "string"},
			{"TENANT_ID", "added", "", "", "contoso-int", "string"},
		},
		"second": {
			{"MAX", "added", "", "", "20", "number"},
			{"REGION", "removed", "ch", "string", "", ""},
			{"TENANT_ID", "changed", "contoso-int", "string", "contoso-prod", "string"},
		},
	}
	seen := map[string]bool{}
	for _, e := range page.Items {
		if e.ActorName != "admin" {
			t.Errorf("event %+v names actor %q, want admin", e, e.ActorName)
		}
		got := changesOf(e)
		switch {
		case e.ApplicationID == appID && e.ApplicationName == "UCC" && reflect.DeepEqual(got, want["app"]):
			seen["app"] = true
		case e.ApplicationID == "" && reflect.DeepEqual(got, want["first"]):
			seen["first"] = true
		case e.ApplicationID == "" && reflect.DeepEqual(got, want["second"]):
			seen["second"] = true
		default:
			t.Errorf("unexpected constants event %+v", e)
		}
	}
	for _, k := range []string{"app", "first", "second"} {
		if !seen[k] {
			t.Errorf("no %s event with changes %+v in %s", k, want[k], body)
		}
	}
}
