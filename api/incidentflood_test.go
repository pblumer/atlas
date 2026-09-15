package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The two surfaces an incident *flood* needs, which the per-incident ones cannot be:
// a reading whose size is the number of causes rather than the number of parked
// tokens, and an action that clears a whole cause (ADR-draft-incident-floods).

// incidentGroup mirrors one row of the summary: a cause an operator can act on as one
// thing — which element of which definition parked, why, how many, and the worker /
// repair-form context that makes the fix reachable from the group.
type incidentGroup struct {
	ProcessDefKey  uint64 `json:"processDefKey"`
	ProcessID      string `json:"processId"`
	Version        int32  `json:"version"`
	ElementID      string `json:"elementId"`
	ElementIndex   int32  `json:"elementIndex"`
	Type           string `json:"type"`
	Count          int    `json:"count"`
	OldestRaisedAt int64  `json:"oldestRaisedAt"`
	NewestRaisedAt int64  `json:"newestRaisedAt"`
	Message        string `json:"message"`
	MessageVaries  bool   `json:"messageVaries"`
	Connector      string `json:"connector"`
	ConnectorKind  string `json:"connectorKind"`
	ConnectorID    string `json:"connectorId"`
	RepairForm     string `json:"repairForm"`
}

type incidentSummary struct {
	Total           int             `json:"total"`
	Groups          []incidentGroup `json:"groups"`
	GroupsTruncated bool            `json:"groupsTruncated"`
	Ungrouped       int             `json:"ungrouped"`
}

type resolveIncidentsResult struct {
	Resolved  int  `json:"resolved"`
	NotFound  int  `json:"notFound"`
	Remaining bool `json:"remaining"`
	Stats     struct {
		UnresolvedIncidents int `json:"unresolvedIncidents"`
	} `json:"stats"`
}

func incidentSummaryQuery(t *testing.T, ts *httptest.Server, query string) incidentSummary {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/incidents/summary"+query, "", "")
	if code != http.StatusOK {
		t.Fatalf("incident summary%s: status=%d body=%s", query, code, body)
	}
	var resp incidentSummary
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, body)
	}
	return resp
}

// resolveIncidents posts a bulk resolve and insists it was accepted.
func resolveIncidents(t *testing.T, ts *httptest.Server, body string) resolveIncidentsResult {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/incidents/resolve", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("bulk resolve %s: status=%d body=%s", body, code, raw)
	}
	var res resolveIncidentsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode bulk resolve: %v (%s)", err, raw)
	}
	return res
}

// parkTaskWithMessage is parkTask with the failure message the operator will read,
// so a test can build a group whose incidents do *not* all say the same thing.
func parkTaskWithMessage(t *testing.T, ts *httptest.Server, defKey uint64, message string) uint64 {
	t.Helper()
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ProcessDefKey      uint64 `json:"processDefKey"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	for _, task := range tasks {
		if task.ProcessDefKey != defKey {
			continue
		}
		payload := fmt.Sprintf(`{"retries":0,"message":%q}`, message)
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", task.Key), payload, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job: status=%d body=%s", code, body)
		}
		return task.ProcessInstanceKey
	}
	t.Fatalf("no waiting task for definition %d (%s)", defKey, body)
	return 0
}

// TestIncidentSummaryGroupsByCause is the reading a flood needs: three tokens parked
// on the same element of the same definition are one line, not three, and the line
// carries what it takes to act on the cause.
func TestIncidentSummaryGroupsByCause(t *testing.T) {
	ts := newTestServer(t)

	if s := incidentSummaryQuery(t, ts, ""); s.Total != 0 || len(s.Groups) != 0 {
		t.Fatalf("fresh server: total=%d groups=%d, want an empty summary", s.Total, len(s.Groups))
	}

	defKey := deployTaskProcess(t, ts, "approval")
	parkTaskWithMessage(t, ts, defKey, "smtp: connection refused")
	parkTaskWithMessage(t, ts, defKey, "smtp: connection refused")
	parkTaskWithMessage(t, ts, defKey, "smtp: connection refused")

	s := incidentSummaryQuery(t, ts, "")
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	if s.GroupsTruncated || s.Ungrouped != 0 {
		t.Errorf("groupsTruncated=%v ungrouped=%d, want a complete grouping", s.GroupsTruncated, s.Ungrouped)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one cause", s.Groups)
	}
	g := s.Groups[0]
	switch {
	case g.Count != 3:
		t.Errorf("count = %d, want 3", g.Count)
	case g.ProcessDefKey != defKey:
		t.Errorf("processDefKey = %d, want %d", g.ProcessDefKey, defKey)
	case g.ProcessID != "approval":
		t.Errorf("processId = %q, want %q", g.ProcessID, "approval")
	case g.ElementID != "review":
		t.Errorf("elementId = %q, want the diagram id %q", g.ElementID, "review")
	case g.Type != "job":
		t.Errorf("type = %q, want %q", g.Type, "job")
	case g.Message != "smtp: connection refused":
		t.Errorf("message = %q, want the shared failure", g.Message)
	case g.MessageVaries:
		t.Errorf("messageVaries = true, want false when every incident says the same")
	case g.OldestRaisedAt == 0 || g.NewestRaisedAt < g.OldestRaisedAt:
		t.Errorf("raised window = [%d, %d], want a sane one", g.OldestRaisedAt, g.NewestRaisedAt)
	case g.Version != 1:
		t.Errorf("version = %d, want 1", g.Version)
	}

	// A second, differently-worded failure on the same element stays in the same
	// group — the element is the cause — but the group says the wording differs.
	parkTaskWithMessage(t, ts, defKey, "smtp: no such mailbox")
	s = incidentSummaryQuery(t, ts, "")
	if len(s.Groups) != 1 || s.Groups[0].Count != 4 {
		t.Fatalf("groups = %+v, want one group of 4", s.Groups)
	}
	if !s.Groups[0].MessageVaries {
		t.Errorf("messageVaries = false, want true once two messages are in the group")
	}
	if s.Groups[0].Message != "smtp: connection refused" {
		t.Errorf("message = %q, want the oldest incident's (a stable representative)", s.Groups[0].Message)
	}
}

// TestIncidentSummaryOrdersBiggestCauseFirst pins the ordering the Operations view
// relies on: the flood is the first line, whatever order the keys are scanned in.
func TestIncidentSummaryOrdersBiggestCauseFirst(t *testing.T) {
	ts := newTestServer(t)
	small := deployTaskProcess(t, ts, "small")
	big := deployTaskProcess(t, ts, "big")
	parkTask(t, ts, small)
	for i := 0; i < 3; i++ {
		parkTask(t, ts, big)
	}

	s := incidentSummaryQuery(t, ts, "")
	if len(s.Groups) != 2 {
		t.Fatalf("groups = %+v, want two causes", s.Groups)
	}
	if s.Groups[0].ProcessID != "big" || s.Groups[0].Count != 3 {
		t.Errorf("first group = %+v, want the 3-incident cause first", s.Groups[0])
	}
	if s.Groups[1].ProcessID != "small" || s.Groups[1].Count != 1 {
		t.Errorf("second group = %+v, want the single incident second", s.Groups[1])
	}
	if s.Total != 4 {
		t.Errorf("total = %d, want 4", s.Total)
	}
}

// TestIncidentSummaryScopes covers the two scopes the summary shares with the
// listing, and that a malformed one is refused rather than ignored.
func TestIncidentSummaryScopes(t *testing.T) {
	ts := newTestServer(t)
	defA := deployTaskProcess(t, ts, "alpha")
	defB := deployTaskProcess(t, ts, "beta")
	instA := parkTask(t, ts, defA)
	parkTask(t, ts, defA)
	parkTask(t, ts, defB)

	byProcess := incidentSummaryQuery(t, ts, fmt.Sprintf("?process=%d", defA))
	if byProcess.Total != 2 || len(byProcess.Groups) != 1 || byProcess.Groups[0].ProcessDefKey != defA {
		t.Errorf("?process=%d summary = %+v, want only alpha's two", defA, byProcess)
	}
	byInstance := incidentSummaryQuery(t, ts, fmt.Sprintf("?instance=%d", instA))
	if byInstance.Total != 1 || len(byInstance.Groups) != 1 {
		t.Errorf("?instance=%d summary = %+v, want that instance's one incident", instA, byInstance)
	}
	if empty := incidentSummaryQuery(t, ts, "?instance=999999"); empty.Total != 0 || len(empty.Groups) != 0 {
		t.Errorf("?instance=999999 = %+v, want an empty summary", empty)
	}
	for _, q := range []string{"?instance=nope", "?process=nope"} {
		if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents/summary"+q, "", ""); code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/incidents/summary%s: status=%d, want 400", q, code)
		}
	}
}

// TestListIncidentsByElementTypeAndMessage covers the filters that make a group's
// rows readable as a page — and that the bulk resolve evaluates as its scope, so
// what the table shows and what the action touches cannot disagree.
func TestListIncidentsByElementTypeAndMessage(t *testing.T) {
	ts := newTestServer(t)
	defA := deployTaskProcess(t, ts, "alpha")
	defB := deployTaskProcess(t, ts, "beta")
	parkTaskWithMessage(t, ts, defA, "smtp: connection refused")
	parkTaskWithMessage(t, ts, defA, "smtp: no such mailbox")
	parkTaskWithMessage(t, ts, defB, "smtp: connection refused")

	if rows := listIncidentsQuery(t, ts, "?element=review"); len(rows) != 3 {
		t.Errorf("?element=review returned %d, want all 3 (both processes use that id)", len(rows))
	}
	if rows := listIncidentsQuery(t, ts, fmt.Sprintf("?process=%d&element=review", defA)); len(rows) != 2 {
		t.Errorf("?process&element returned %d, want alpha's 2", len(rows))
	}
	if rows := listIncidentsQuery(t, ts, "?element=nosuchelement"); len(rows) != 0 {
		t.Errorf("?element=nosuchelement returned %d, want none", len(rows))
	}
	if rows := listIncidentsQuery(t, ts, "?type=job"); len(rows) != 3 {
		t.Errorf("?type=job returned %d, want all 3 parked jobs", len(rows))
	}
	if rows := listIncidentsQuery(t, ts, "?type=timer"); len(rows) != 0 {
		t.Errorf("?type=timer returned %d, want none", len(rows))
	}
	// Substring, case-insensitive: an operator pastes a fragment of what they read.
	if rows := listIncidentsQuery(t, ts, "?message=CONNECTION+refused"); len(rows) != 2 {
		t.Errorf("?message= returned %d, want the 2 refused ones", len(rows))
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents?type=nonsense", "", ""); code != http.StatusBadRequest {
		t.Errorf("?type=nonsense: status=%d, want 400", code)
	}
}

// TestResolveIncidentsByKeys is the hand-picked set: the rows an operator ticked,
// resolved in one call, with a key that holds no incident reported rather than
// failing the whole call.
func TestResolveIncidentsByKeys(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "approval")
	for i := 0; i < 3; i++ {
		parkTask(t, ts, defKey)
	}
	rows := listIncidents(t, ts)
	if len(rows) != 3 {
		t.Fatalf("setup: %d incidents, want 3", len(rows))
	}

	body := fmt.Sprintf(`{"keys":[%d,%d,999999],"retries":2}`, rows[0].ElementInstanceKey, rows[1].ElementInstanceKey)
	res := resolveIncidents(t, ts, body)
	if res.Resolved != 2 {
		t.Errorf("resolved = %d, want 2", res.Resolved)
	}
	if res.NotFound != 1 {
		t.Errorf("notFound = %d, want 1 (the key with no incident)", res.NotFound)
	}
	if res.Remaining {
		t.Errorf("remaining = true, want false: an explicit set is its own bound")
	}
	if res.Stats.UnresolvedIncidents != 1 {
		t.Errorf("stats.unresolvedIncidents = %d, want 1", res.Stats.UnresolvedIncidents)
	}
	left := listIncidents(t, ts)
	if len(left) != 1 || left[0].ElementInstanceKey != rows[2].ElementInstanceKey {
		t.Errorf("remaining incidents = %+v, want only the unticked one", left)
	}
	// The resolved tasks are back in the inbox: resolving really re-activated them.
	code, body2 := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []json.RawMessage
	_ = json.Unmarshal(body2, &tasks)
	if code != http.StatusOK || len(tasks) != 2 {
		t.Errorf("tasks after bulk resolve: status=%d count=%d, want 2", code, len(tasks))
	}
	// Duplicate keys collapse rather than counting twice.
	dup := fmt.Sprintf(`{"keys":[%d,%d]}`, rows[2].ElementInstanceKey, rows[2].ElementInstanceKey)
	if res := resolveIncidents(t, ts, dup); res.Resolved != 1 || res.NotFound != 0 {
		t.Errorf("duplicate keys = %+v, want one resolution and no not-found", res)
	}
}

// TestResolveIncidentsByFilter is the flood case: one cause, one call, repeated
// while the response says there is more.
func TestResolveIncidentsByFilter(t *testing.T) {
	ts := newTestServer(t)
	defA := deployTaskProcess(t, ts, "alpha")
	defB := deployTaskProcess(t, ts, "beta")
	for i := 0; i < 4; i++ {
		parkTaskWithMessage(t, ts, defA, "smtp: connection refused")
	}
	parkTaskWithMessage(t, ts, defB, "smtp: connection refused")

	// A bounded batch reports that more matched than it took.
	first := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"limit":2}`, defA))
	if first.Resolved != 2 || !first.Remaining {
		t.Fatalf("first batch = %+v, want 2 resolved and remaining=true", first)
	}
	second := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"limit":50}`, defA))
	if second.Resolved != 2 || second.Remaining {
		t.Fatalf("second batch = %+v, want the last 2 and remaining=false", second)
	}
	// Beta's incident was never in scope.
	left := listIncidents(t, ts)
	if len(left) != 1 || left[0].ProcessDefKey != defB {
		t.Fatalf("after draining alpha: %+v, want only beta's incident", left)
	}

	// The narrower selectors: element, type and message, over a fresh flood.
	for i := 0; i < 2; i++ {
		parkTaskWithMessage(t, ts, defA, "smtp: no such mailbox")
	}
	byMessage := resolveIncidents(t, ts, `{"message":"no such mailbox"}`)
	if byMessage.Resolved != 2 {
		t.Errorf("by message = %+v, want the 2 mailbox failures", byMessage)
	}
	byElement := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"elementId":"review","type":"job"}`, defB))
	if byElement.Resolved != 1 {
		t.Errorf("by element+type = %+v, want beta's one", byElement)
	}
	if got := listIncidents(t, ts); len(got) != 0 {
		t.Errorf("after draining everything: %d incidents remain", len(got))
	}
	// A scope that matches nothing is an honest zero, not an error.
	if res := resolveIncidents(t, ts, `{"elementId":"nosuchelement"}`); res.Resolved != 0 || res.Remaining {
		t.Errorf("empty scope = %+v, want nothing resolved", res)
	}
	// A limit above the per-call cap is clamped to it rather than refused: an
	// operator asking for "all of them" has asked for something reasonable, and the
	// cap is the server's business — the answer says remaining when it bit.
	parkTaskWithMessage(t, ts, defA, "smtp: connection refused")
	if res := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"limit":1000000}`, defA)); res.Resolved != 1 {
		t.Errorf("limit above the cap = %+v, want it clamped and the one incident resolved", res)
	}
}

// TestResolveIncidentsRefusals covers what the bulk endpoint must not do: mix the
// two modes, and resolve everything on the server because a field was left out.
func TestResolveIncidentsRefusals(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "approval")
	parkTask(t, ts, defKey)

	for name, body := range map[string]string{
		"both modes":     fmt.Sprintf(`{"keys":[1],"processDefKey":%d}`, defKey),
		"no selector":    `{"retries":1}`,
		"empty selector": `{}`,
		"unknown type":   `{"type":"nonsense"}`,
		"bad json":       `{oops`,
		"bad retries":    fmt.Sprintf(`{"processDefKey":%d,"retries":-3}`, defKey),
		"bad limit":      fmt.Sprintf(`{"processDefKey":%d,"limit":-1}`, defKey),
	} {
		code, raw := doReq(t, ts, http.MethodPost, "/api/v1/incidents/resolve", body, "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", name, code, raw)
		}
	}
	// Nothing was resolved by any of the refusals.
	if got := listIncidents(t, ts); len(got) != 1 {
		t.Errorf("after the refusals: %d incidents, want the one still parked", len(got))
	}
	// A definition that does not exist is an empty scope, not a 404: the operator
	// asked what matches, and nothing does.
	if res := resolveIncidents(t, ts, `{"processDefKey":999999}`); res.Resolved != 0 {
		t.Errorf("unknown definition = %+v, want nothing resolved", res)
	}
}

// TestResolveIncidentsGrantsTheRetryBudget pins that the budget reaches the
// re-activated job: one attempt by default, and the granted count when asked. A
// second failure with retries left does not park the token again.
func TestResolveIncidentsGrantsTheRetryBudget(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "approval")
	parkTask(t, ts, defKey)
	rows := listIncidents(t, ts)
	if len(rows) != 1 {
		t.Fatalf("setup: %d incidents, want 1", len(rows))
	}

	if res := resolveIncidents(t, ts, fmt.Sprintf(`{"keys":[%d],"retries":3}`, rows[0].ElementInstanceKey)); res.Resolved != 1 {
		t.Fatalf("resolve = %+v, want one", res)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after resolve = %v (%s)", err, body)
	}
	// One failure with 2 left: still no incident, because the budget was granted.
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key), `{"retries":2,"message":"again"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}
	if got := listIncidents(t, ts); len(got) != 0 {
		t.Errorf("incidents after a failure with retries left = %d, want 0", len(got))
	}
}

// TestIncidentSummaryCarriesWorkerContext is why a group is worth reading rather than
// merely counting: the fix for a flood is usually a field on a worker, and the group
// says which worker — the same fact ADR-0160 put on a single incident, carried onto the
// whole cause, and resolved once per group rather than once per parked token.
func TestIncidentSummaryCarriesWorkerContext(t *testing.T) {
	ts := newTestServer(t)

	const mailBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements><atlas:mailConnector connector="Patrick Blumer" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", mailBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	// Nothing is configured under that name, so every instance parks on the same task
	// with the same cause — the flood this whole surface exists for.
	for i := 0; i < 3; i++ {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance: status=%d body=%s", code, body)
		}
	}

	s := incidentSummaryQuery(t, ts, "")
	if len(s.Groups) != 1 || s.Groups[0].Count != 3 {
		t.Fatalf("groups = %+v, want one cause of 3", s.Groups)
	}
	g := s.Groups[0]
	if g.Connector != "Patrick Blumer" || g.ConnectorKind != "mail" {
		t.Errorf("group worker = %q/%q, want the name and kind the model states", g.Connector, g.ConnectorKind)
	}
	if g.ConnectorID != "" {
		t.Errorf("connectorId = %q, want empty — nothing is configured under that name", g.ConnectorID)
	}
	if g.ElementID != "send" {
		t.Errorf("elementId = %q, want %q", g.ElementID, "send")
	}

	// Configuring the worker is the fix; the group then points at the record to open,
	// and one call clears everything it parked.
	created := `{"name":"Patrick Blumer","kind":"mail","provider":"preview","endpoint":"mx.example.ch:587","sender":"a@b.ch"}`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/connectors", created, "application/json"); code != http.StatusOK {
		t.Fatalf("create worker: status=%d body=%s", code, body)
	}
	if s := incidentSummaryQuery(t, ts, ""); len(s.Groups) != 1 || s.Groups[0].ConnectorID == "" {
		t.Errorf("after configuring: groups = %+v, want the configured record named", s.Groups)
	}
	res := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"elementId":"send"}`, deploy.Key))
	if res.Resolved != 3 {
		t.Fatalf("resolve the cause = %+v, want all 3", res)
	}
	if got := listIncidents(t, ts); len(got) != 0 {
		t.Errorf("after the fix and the bulk resolve: %d incidents remain (%+v)", len(got), got)
	}
}

// TestIncidentScopeByCompiledElementIndex covers the selector that exists for the one
// case the BPMN id cannot answer: an incident whose definition is no longer deployed
// carries no id at all, so a scope that simply left the element out would widen from
// the line an operator clicked to the whole definition. The index is what the incident
// actually stores, and it names the element precisely inside its own definition.
func TestIncidentScopeByCompiledElementIndex(t *testing.T) {
	ts := newTestServer(t)
	const twinBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="twin" isExecutable="true">
    <startEvent id="start"/>
    <parallelGateway id="fork"/>
    <userTask id="left" name="Left"/>
    <userTask id="right" name="Right"/>
    <endEvent id="end1"/>
    <endEvent id="end2"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="left"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="right"/>
    <sequenceFlow id="f3" sourceRef="left" targetRef="end1"/>
    <sequenceFlow id="f4" sourceRef="right" targetRef="end2"/>
  </process>
</definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", twinBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	// Both branches park: one instance, two elements, two causes.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 2 {
		t.Fatalf("tasks = %v (%s), want the two parallel branches", err, body)
	}
	for _, task := range tasks {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", task.Key), `{"retries":0,"message":"boom"}`, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job: status=%d body=%s", code, body)
		}
	}

	s := incidentSummaryQuery(t, ts, "")
	if len(s.Groups) != 2 {
		t.Fatalf("groups = %+v, want one per parked element", s.Groups)
	}
	var left incidentGroup
	for _, g := range s.Groups {
		if g.ElementID == "left" {
			left = g
		}
	}
	if left.ElementID != "left" {
		t.Fatalf("groups = %+v, want one on the left branch", s.Groups)
	}

	// The index scopes the listing to that one element…
	rows := listIncidentsQuery(t, ts, fmt.Sprintf("?process=%d&elementIndex=%d", deploy.Key, left.ElementIndex))
	if len(rows) != 1 || rows[0].ElementID != "left" {
		t.Errorf("?elementIndex=%d returned %+v, want the left branch's incident alone", left.ElementIndex, rows)
	}
	// …and the resolve to the same one, leaving the other branch parked.
	res := resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"elementIndex":%d}`, deploy.Key, left.ElementIndex))
	if res.Resolved != 1 {
		t.Fatalf("resolve by index = %+v, want exactly the left branch", res)
	}
	left2 := listIncidents(t, ts)
	if len(left2) != 1 || left2[0].ElementID != "right" {
		t.Errorf("remaining = %+v, want only the right branch", left2)
	}
	// Index 0 is a real element index, so it must scope rather than read as "unset".
	if rows := listIncidentsQuery(t, ts, "?elementIndex=0"); len(rows) != 0 {
		t.Errorf("?elementIndex=0 returned %+v, want the (empty) set of incidents on element #0", rows)
	}
	for _, q := range []string{"?elementIndex=nope", "?elementIndex=-2"} {
		if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents"+q, "", ""); code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/incidents%s: status=%d, want 400", q, code)
		}
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/incidents/resolve", `{"processDefKey":1,"elementIndex":-1}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("negative elementIndex on resolve: status=%d, want 400", code)
	}
}
