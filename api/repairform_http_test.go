package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A mail task that parks (no connector is configured) and reads one process variable
// through an input mapping — so all the sources a repair form can come from are in play
// on one element, which is what makes the precedence testable end to end.
const repairResolveBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements>
        <atlas:mailConnector connector="Postbox" to="=recipient" subject="hi" body="hi"/>
        <zeebe:ioMapping><zeebe:input source="=recipient" target="to"/></zeebe:ioMapping>
      </extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`

// repairFormView is the resolve endpoint's answer as these tests read it.
type repairFormView struct {
	Source string `json:"source"`
	FormID string `json:"formId"`
	Name   string `json:"name"`
	Schema struct {
		Components []struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		} `json:"components"`
	} `json:"schema"`
}

// parkOneMailTask deploys the model above, starts it so the mail task parks behind an
// incident, and returns that incident's element instance key.
func parkOneMailTask(t *testing.T, ts *httptest.Server, xml string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	inc := listIncidents(t, ts)
	if len(inc) != 1 {
		t.Fatalf("incidents = %+v, want the parked mail task", inc)
	}
	return inc[0].ElementInstanceKey
}

func readRepairForm(t *testing.T, ts *httptest.Server, elKey uint64) (int, repairFormView) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/incidents/%d/repair-form", elKey), "", "")
	var v repairFormView
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("decode repair form: %v (%s)", err, body)
		}
	}
	return code, v
}

// saveForm stores a form so a binding has something real to resolve to.
func saveForm(t *testing.T, ts *httptest.Server, id, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"name":%q,"schema":{"type":"default","components":[{"type":"textfield","key":"recipient","label":"Recipient"}]}}`, id, name)
	if code, resp := doReq(t, ts, http.MethodPost, "/api/v1/forms", body, "application/json"); code != http.StatusOK {
		t.Fatalf("save form: status=%d body=%s", code, resp)
	}
}

// TestRepairFormFallsBackToTheDerivedOne is the half of the record that needs no
// authoring at all: nobody bound anything, and the incident still offers named fields —
// the process variables the task's input mappings read.
func TestRepairFormFallsBackToTheDerivedOne(t *testing.T) {
	ts := newTestServer(t)
	elKey := parkOneMailTask(t, ts, repairResolveBPMN)

	code, v := readRepairForm(t, ts, elKey)
	if code != http.StatusOK {
		t.Fatalf("repair form: status=%d", code)
	}
	if v.Source != "derived" {
		t.Errorf("source = %q, want %q", v.Source, "derived")
	}
	if v.FormID != "" {
		t.Errorf("formId = %q, want empty — a derived form has no stored record", v.FormID)
	}
	var keys []string
	for _, c := range v.Schema.Components {
		if c.Key != "" {
			keys = append(keys, c.Key)
		}
	}
	if len(keys) != 1 || keys[0] != "recipient" {
		t.Errorf("derived fields = %v, want [recipient] — the variable the task reads", keys)
	}
	// It says what it is, so an operator reads a derived form with the confidence it
	// deserves rather than the confidence an authored one would.
	if v.Name == "" {
		t.Error("a derived form has no name; the dialog has nothing to label it with")
	}
}

// TestConnectorKindFormBeatsTheDerivedOne is the other half: authored once for "how a
// mail task fails", it applies to every mail task in every model — and it knows more than
// a derivation, so it must win over one.
func TestConnectorKindFormBeatsTheDerivedOne(t *testing.T) {
	ts := newTestServer(t)
	elKey := parkOneMailTask(t, ts, repairResolveBPMN)
	saveForm(t, ts, "mail-repair", "Fix the mail")

	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"mail-repair"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("bind kind: status=%d body=%s", code, body)
	}

	code, v := readRepairForm(t, ts, elKey)
	if code != http.StatusOK {
		t.Fatalf("repair form: status=%d", code)
	}
	if v.Source != "connector" || v.FormID != "mail-repair" {
		t.Errorf("source=%q formId=%q, want the connector kind's form", v.Source, v.FormID)
	}
	if v.Name != "Fix the mail" {
		t.Errorf("name = %q, want the form's own name", v.Name)
	}
}

// TestTaskFormBeatsTheConnectorKindForm: a form somebody wrote for *this* task knows more
// than one written for the whole kind, so authoring one must be enough to make it win
// without unbinding anything.
func TestTaskFormBeatsTheConnectorKindForm(t *testing.T) {
	ts := newTestServer(t)
	const withTaskForm = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements>
        <atlas:mailConnector connector="Postbox" to="=recipient" subject="hi" body="hi"/>
        <zeebe:formDefinition formId="this-task-only"/>
        <zeebe:ioMapping><zeebe:input source="=recipient" target="to"/></zeebe:ioMapping>
      </extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`
	elKey := parkOneMailTask(t, ts, withTaskForm)
	saveForm(t, ts, "this-task-only", "For this task")
	saveForm(t, ts, "mail-repair", "Fix the mail")
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"mail-repair"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("bind kind: status=%d body=%s", code, body)
	}

	code, v := readRepairForm(t, ts, elKey)
	if code != http.StatusOK {
		t.Fatalf("repair form: status=%d", code)
	}
	if v.Source != "task" || v.FormID != "this-task-only" {
		t.Errorf("source=%q formId=%q, want the task's own form to win over the kind's", v.Source, v.FormID)
	}
}

// TestRepairFormIs404WhenNothingApplies: a task with no binding and no input mappings has
// no form, and saying so plainly is a complete answer — the surface falls back to the raw
// editor exactly as it did before any of this existed.
func TestRepairFormIs404WhenNothingApplies(t *testing.T) {
	ts := newTestServer(t)
	const plain = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements><atlas:mailConnector connector="Postbox" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`
	elKey := parkOneMailTask(t, ts, plain)
	if code, _ := readRepairForm(t, ts, elKey); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no source applies", code)
	}
	// An element instance that does not exist answers the same way rather than erroring.
	if code, _ := readRepairForm(t, ts, 999_999); code != http.StatusNotFound {
		t.Errorf("unknown element instance: status = %d, want 404", code)
	}
}

// TestStaleConnectorKindBindingFallsThrough: a binding whose form was deleted must not
// offer something that cannot be opened. It reports no form, so the operator gets the raw
// editor rather than a dialog that fails on open.
func TestStaleConnectorKindBindingFallsThrough(t *testing.T) {
	ts := newTestServer(t)
	const noMappings = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements><atlas:mailConnector connector="Postbox" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`
	elKey := parkOneMailTask(t, ts, noMappings)
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"never-existed"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("bind kind: status=%d body=%s", code, body)
	}
	if code, _ := readRepairForm(t, ts, elKey); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — a binding to a deleted form is stale, not openable", code)
	}
}

// TestRepairFormSettingsRoundTrip: the stored table is what the endpoint reports back,
// and an empty id unsets rather than storing a binding to nothing — which the resolver
// would otherwise have to tell apart from a real one.
func TestRepairFormSettingsRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	read := func() map[string]string {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/repair-forms", "", "")
		if code != http.StatusOK {
			t.Fatalf("get repair forms: status=%d body=%s", code, body)
		}
		var resp struct {
			ByKind map[string]string `json:"byKind"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return resp.ByKind
	}
	// Nothing configured is an empty table, not an error — the state every install starts in.
	if got := read(); len(got) != 0 {
		t.Errorf("initial bindings = %v, want none", got)
	}

	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"mail-repair","rest":""}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", code, body)
	}
	got := read()
	if got["mail"] != "mail-repair" {
		t.Errorf("mail binding = %q, want %q", got["mail"], "mail-repair")
	}
	if _, ok := got["rest"]; ok {
		t.Errorf("an empty id was stored as a binding: %v — unsetting is a delete", got)
	}
}

// TestRepairFormBindingIsAdminGated: binding a form to a connector kind changes what every
// operator is shown on every incident of that kind, so writing it is admin-only when auth
// is on — like the theme and the registration process it is stored beside. Reading it is
// not privileged beyond being signed in: it says which integrations have guidance, not
// what any instance holds.
func TestRepairFormBindingIsAdminGated(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	// Signed out, the auth middleware answers before any handler does.
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"x"}}`, "application/json"); code != http.StatusUnauthorized {
		t.Errorf("anon PUT: status=%d, want 401", code)
	}

	// An admin may write, and may read back what was stored.
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("admin login: status=%d", code)
	}
	if code, body := cReq(t, admin, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"mail-repair"}}`); code != http.StatusOK {
		t.Fatalf("admin PUT: status=%d body=%s", code, body)
	}
	if code, _ := cReq(t, admin, ts, http.MethodGet, "/api/v1/settings/repair-forms", ""); code != http.StatusOK {
		t.Errorf("admin GET: status=%d, want 200", code)
	}

	// A signed-in non-admin may read, but not rebind — the write changes what every
	// operator sees on every incident of that kind.
	if code, body := cReq(t, admin, ts, http.MethodPost, "/api/v1/users",
		`{"username":"olive","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create user: status=%d body=%s", code, body)
	}
	plain := newClient(t)
	if code := login(t, plain, ts, "olive", "password1"); code != http.StatusOK {
		t.Fatalf("user login: status=%d", code)
	}
	if code, _ := cReq(t, plain, ts, http.MethodGet, "/api/v1/settings/repair-forms", ""); code != http.StatusOK {
		t.Errorf("non-admin GET: status=%d, want 200", code)
	}
	if code, _ := cReq(t, plain, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"something-else"}}`); code != http.StatusForbidden {
		t.Errorf("non-admin PUT: status=%d, want 403", code)
	}
}

// TestRepairFormBindingRejectsAMalformedBody: a body that is not the expected shape is a
// client error, reported as one rather than silently storing an empty table — which would
// quietly unbind every kind.
func TestRepairFormBindingRejectsAMalformedBody(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind": "not an object"}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("status=%d body=%s, want 400", code, body)
	}
	// And nothing was stored.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/repair-forms", "", "")
	if code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", code, body)
	}
	var resp struct {
		ByKind map[string]string `json:"byKind"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.ByKind) != 0 {
		t.Errorf("bindings = %v after a rejected write, want none", resp.ByKind)
	}
}

// TestRepairFormRejectsABadElementKey: the path segment is an element instance key, and a
// value that is not one is the caller's error rather than a lookup that happens to miss.
func TestRepairFormRejectsABadElementKey(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents/not-a-key/repair-form", "", ""); code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", code)
	}
}

// TestRepairFormsReportTheKindsThatExist: the Console renders a row per kind, and a list
// hardcoded in the browser would silently omit every kind added after it was written — so
// the newest integration would be the one nobody could give guidance for.
func TestRepairFormsReportTheKindsThatExist(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/repair-forms", "", "")
	if code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", code, body)
	}
	var resp struct {
		Kinds []string `json:"kinds"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Kinds) == 0 {
		t.Fatal("no connector kinds reported; the Console would render an empty panel")
	}
	// "mail" is the case the record was written for, so its absence would mean the list
	// is not the one the resolver matches against.
	found := false
	for _, k := range resp.Kinds {
		if k == "mail" {
			found = true
		}
	}
	if !found {
		t.Errorf("kinds = %v, want the list to include mail", resp.Kinds)
	}
}

// TestRepairFormIs404ForAnUnknownElement: an element instance key nobody knows resolves
// to nothing, not to an error. An incidents view left open while retention swept the
// instance away asks this question routinely, and the surface's answer to a 404 — offer
// the raw editor — is the right one here too.
func TestRepairFormIs404ForAnUnknownElement(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := readRepairForm(t, ts, 999_999); code != http.StatusNotFound {
		t.Errorf("status=%d, want 404 for an element instance that does not exist", code)
	}
}

// TestAnUnnamedFormIsLabelledByItsID: the dialog shows which form it is rendering, so a
// stale binding is tellable from a wrong one. A form saved without a name would leave
// that label blank — the one place the operator has to check what they are looking at.
func TestAnUnnamedFormIsLabelledByItsID(t *testing.T) {
	ts := newTestServer(t)
	elKey := parkOneMailTask(t, ts, repairResolveBPMN)
	saveForm(t, ts, "nameless", "")

	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/repair-forms",
		`{"byKind":{"mail":"nameless"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("bind kind: status=%d body=%s", code, body)
	}
	code, v := readRepairForm(t, ts, elKey)
	if code != http.StatusOK {
		t.Fatalf("repair form: status=%d", code)
	}
	if v.Name != "nameless" {
		t.Errorf("name = %q, want the form's id as its label", v.Name)
	}
}
