package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The event log (ADR-0472): what was sent and where it arrived, read as the
// instances it is about are read (ADR-0275).

const pingCatcherBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="S" name="evt.ping"/>
  <process id="ping-catcher" name="Ping catcher" isExecutable="true">
    <startEvent id="s"/>
    <intermediateCatchEvent id="wait"><signalEventDefinition signalRef="S"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

const pingThrowerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="S" name="evt.ping"/>
  <process id="ping-thrower" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="ping"><signalEventDefinition signalRef="S"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="ping"/>
    <sequenceFlow id="f2" sourceRef="ping" targetRef="e"/>
  </process>
</definitions>`

type logParty struct {
	InstanceKey   uint64 `json:"instanceKey"`
	DefinitionKey uint64 `json:"definitionKey"`
	ProcessID     string `json:"processId"`
	ProjectID     string `json:"projectId"`
	ElementID     string `json:"elementId"`
}

type logRow struct {
	Position       string    `json:"position"`
	Cause          string    `json:"cause"`
	Kind           string    `json:"kind"`
	Channel        string    `json:"channel"`
	Name           string    `json:"name"`
	CorrelationKey string    `json:"correlationKey"`
	Source         string    `json:"source"`
	Sender         *logParty `json:"sender"`
	Receiver       *logParty `json:"receiver"`
	Reached        *uint32   `json:"reached"`
	Started        *uint32   `json:"started"`
}

type logPage struct {
	Rows   []logRow `json:"rows"`
	Next   string   `json:"next"`
	Latest string   `json:"latest"`
	More   bool     `json:"more"`
}

func readEventLog(t *testing.T, c *http.Client, ts *httptest.Server, query string) logPage {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/event-log"+query, "")
	var page logPage
	if code != http.StatusOK || json.Unmarshal(body, &page) != nil {
		t.Fatalf("event log%s = %d %s", query, code, body)
	}
	return page
}

func kindsOf(rows []logRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

// deployIntoProject deploys a model, into a project when one is named, and answers its key.
func deployIntoProject(t *testing.T, admin *http.Client, ts *httptest.Server, bpmn, project string) uint64 {
	t.Helper()
	path := "/api/v1/deployments"
	if project != "" {
		path += "?projectId=" + project
	}
	code, body := cReq(t, admin, ts, "POST", path, bpmn)
	var dep struct{ Key uint64 }
	if code != http.StatusOK || json.Unmarshal(body, &dep) != nil {
		t.Fatalf("deploy = %d: %s", code, body)
	}
	return dep.Key
}

func startLogInstance(t *testing.T, admin *http.Client, ts *httptest.Server, defKey uint64) {
	t.Helper()
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{}`); code != http.StatusOK {
		t.Fatalf("start %d = %d: %s", defKey, code, body)
	}
}

// TestTheEventLogShowsEachReaderWhatTheirInstancesShow: an administrator reads the
// throw, its reception and a publish nobody received; a member of the catcher's
// project reads the reception alone, because the thrower is no project's and the
// publish names no definition; an account outside every project reads nothing, with
// a 200 — the route is open and the rows decide.
func TestTheEventLogShowsEachReaderWhatTheirInstancesShow(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["modeler"]`)
	createUserWithRoles(t, admin, ts.URL, "stranger", `["modeler"]`)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Pings"}`)
	var proj struct {
		ID string `json:"id"`
	}
	if (code != http.StatusCreated && code != http.StatusOK) || json.Unmarshal(body, &proj) != nil {
		t.Fatalf("create project = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+proj.ID+"/members/"+memberID,
		`{"role":"viewer"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add project member = %d: %s", code, body)
	}
	catcher := deployIntoProject(t, admin, ts, pingCatcherBPMN, proj.ID)
	thrower := deployIntoProject(t, admin, ts, pingThrowerBPMN, "")
	startLogInstance(t, admin, ts, catcher)
	startLogInstance(t, admin, ts, thrower)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/messages", `{"name":"lonely","correlationKey":"k-1"}`); code != http.StatusOK {
		t.Fatalf("publish = %d: %s", code, body)
	}

	all := readEventLog(t, admin, ts, "")
	if got := kindsOf(all.Rows); fmt.Sprint(got) != "[message.sent signal.thrown signal.received]" {
		t.Fatalf("administrator's log = %v, want the publish, the throw and its reception, newest first", got)
	}
	sent, thrown, received := all.Rows[0], all.Rows[1], all.Rows[2]
	if sent.Source != "publish" || sent.CorrelationKey != "k-1" || sent.Reached == nil || *sent.Reached != 0 {
		t.Errorf("publish row = %+v, want a publish of k-1 that reached nobody", sent)
	}
	if thrown.Sender == nil || thrown.Sender.ProcessID != "ping-thrower" || thrown.Sender.ElementID != "ping" ||
		thrown.Reached == nil || *thrown.Reached != 1 {
		t.Errorf("throw row = %+v (sender %+v), want ping-thrower's ping, reaching one", thrown, thrown.Sender)
	}
	if received.Receiver == nil || received.Receiver.ProcessID != "ping-catcher" || received.Receiver.ElementID != "wait" ||
		received.Receiver.ProjectID != proj.ID || received.Sender == nil || received.Sender.InstanceKey != thrown.Sender.InstanceKey {
		t.Errorf("reception row = %+v (receiver %+v, sender %+v)", received, received.Receiver, received.Sender)
	}
	if received.Cause != thrown.Cause {
		t.Errorf("the reception's cause %s is not the throw's %s", received.Cause, thrown.Cause)
	}

	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")
	if got := kindsOf(readEventLog(t, member, ts, "").Rows); fmt.Sprint(got) != "[signal.received]" {
		t.Fatalf("project member's log = %v, want the reception in their project alone", got)
	}
	stranger := signInAs(t, ts.URL, "stranger", "a-password-that-is-long")
	if rows := readEventLog(t, stranger, ts, "").Rows; len(rows) != 0 {
		t.Fatalf("an account outside every project read %v", kindsOf(rows))
	}

	// The filters narrow, and a cursor reads what came after.
	if got := kindsOf(readEventLog(t, admin, ts, "?channel=message").Rows); fmt.Sprint(got) != "[message.sent]" {
		t.Errorf("channel=message = %v", got)
	}
	if got := kindsOf(readEventLog(t, admin, ts, "?name=evt.ping").Rows); fmt.Sprint(got) != "[signal.thrown signal.received]" {
		t.Errorf("name=evt.ping = %v", got)
	}
	inst := fmt.Sprint(received.Receiver.InstanceKey)
	if got := kindsOf(readEventLog(t, admin, ts, "?instance="+inst).Rows); fmt.Sprint(got) != "[signal.received]" {
		t.Errorf("instance=%s = %v", inst, got)
	}
	if got := readEventLog(t, admin, ts, "?limit=1"); len(got.Rows) != 1 || !got.More || got.Next != got.Rows[0].Position {
		t.Errorf("limit=1 = %+v, want one row, more, and its position as next", got)
	}
	if older := readEventLog(t, admin, ts, "?before="+all.Rows[0].Position); fmt.Sprint(kindsOf(older.Rows)) != "[signal.thrown signal.received]" {
		t.Errorf("before the newest = %v", kindsOf(older.Rows))
	}
	if newer := readEventLog(t, admin, ts, "?after="+all.Latest); len(newer.Rows) != 0 || newer.Latest != all.Latest {
		t.Errorf("after the latest = %+v, want nothing and the same cursor", newer)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/messages", `{"name":"lonely","correlationKey":"k-2"}`); code != http.StatusOK {
		t.Fatalf("publish again = %d: %s", code, body)
	}
	if newer := readEventLog(t, admin, ts, "?after="+all.Latest); len(newer.Rows) != 1 || newer.Rows[0].CorrelationKey != "k-2" {
		t.Errorf("after the latest, once more was sent = %+v", newer)
	}
	for _, q := range []string{"?channel=email", "?before=x", "?limit=501", "?before=1&after=1", "?instance=x"} {
		if code, _ := cReq(t, admin, ts, "GET", "/api/v1/event-log"+q, ""); code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/event-log%s = %d, want 400", q, code)
		}
	}
}
