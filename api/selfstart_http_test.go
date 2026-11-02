package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// POST /api/v1/me/processes/{key}/instances (ADR-0516): a signed-in person starts
// a process an administrator granted them, through the same engine start every
// other door uses. Every request is authorized again, against the newest version,
// in the turn that submits the start; the values are checked against the process's
// start form before anything is sealed or started.

const selfStartPath = "/api/v1/me/processes/"

// activeInstances reads the engine's live instance count — the cheapest proof that a
// refused start created nothing.
func activeInstances(t *testing.T, w *grantWorld) int {
	t.Helper()
	_, body := cReq(t, w.admin, w.ts, "GET", "/api/v1/stats", "")
	var s struct {
		Active int `json:"activeProcessInstances"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode stats %s: %v", body, err)
	}
	return s.Active
}

// waitingBPMN is a process that stays active at a user task after its start, so a
// started instance is counted until somebody completes it. With a non-empty form id
// its start event carries that start form.
func waitingBPMN(id, startForm string) string {
	start := `<startEvent id="s"/>`
	if startForm != "" {
		start = `<startEvent id="s"><extensionElements><zeebe:formDefinition formId="` + startForm + `"/></extensionElements></startEvent>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas/test">
  <process id="` + id + `" name="Request ` + id + `" isExecutable="true">
    ` + start + `
    <userTask id="t" name="Handle"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`
}

// requestForm declares what the server holds a submission to: required fields, a
// number with bounds, a closed list of options, a length limit, and a disabled field
// the form only shows.
const requestForm = `{"id":"request-form","name":"Request","schema":{"type":"default","components":[
  {"type":"textfield","key":"site","label":"Site","validate":{"required":true,"maxLength":20}},
  {"type":"number","key":"amount","label":"Amount","validate":{"min":1,"max":100}},
  {"type":"select","key":"kind","label":"Kind","values":[{"label":"A","value":"a"},{"label":"B","value":"b"}]},
  {"type":"checkbox","key":"urgent","label":"Urgent"},
  {"type":"textfield","key":"approved","label":"Approved","disabled":true},
  {"type":"group","label":"Address","path":"address","components":[
    {"type":"textfield","key":"street","label":"Street","validate":{"required":true}}
  ]}
]}}`

// newSelfStartWorld is a grant world with two more processes in the field
// application: one that starts from requestForm and one that starts without a form,
// both waiting at a user task, both granted to the field worker.
func newSelfStartWorld(t *testing.T) *grantWorld {
	t.Helper()
	w := newGrantWorld(t, t.TempDir())
	t.Cleanup(w.close)
	form := strings.Replace(requestForm, `"name":"Request"`, `"name":"Request","projectId":"`+w.fieldApp+`"`, 1)
	if code, body := cReq(t, w.admin, w.ts, "POST", "/api/v1/forms", form); code != http.StatusOK {
		t.Fatalf("save form = %d %s", code, body)
	}
	for id, f := range map[string]string{"with-form": "request-form", "no-form": ""} {
		if code, body := cReqTyped(t, w.admin, w.ts, "POST", "/api/v1/deployments?projectId="+w.fieldApp, "application/xml", waitingBPMN(id, f)); code != http.StatusOK {
			t.Fatalf("deploy %s = %d %s", id, code, body)
		}
		w.grant(t, w.fieldApp, id, `{"userIds":["`+w.workerID+`"]}`)
	}
	return w
}

func startAs(t *testing.T, w *grantWorld, c *http.Client, key uint64, body string) (int, map[string]any, string) {
	t.Helper()
	code, out := cReq(t, c, w.ts, "POST", selfStartPath+jsonNum(key)+"/instances", body)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return code, m, string(out)
}

func TestSelfServiceStartStartsWhatIsGranted(t *testing.T) {
	w := newSelfStartWorld(t)
	before := activeInstances(t, w)
	key, _ := newestKey(t, w, "no-form")
	code, m, raw := startAs(t, w, w.worker, key, `{}`)
	if code != http.StatusOK || m["definitionKey"] != float64(key) {
		t.Fatalf("start = %d %s", code, raw)
	}
	if ik, _ := m["instanceKey"].(float64); ik == 0 {
		t.Fatalf("start answered no instance reference: %s", raw)
	}
	if got := activeInstances(t, w); got != before+1 {
		t.Fatalf("active instances %d -> %d, want one more", before, got)
	}
	// A body with no variables at all is the same empty start.
	if code, _, raw := startAs(t, w, w.worker, key, ``); code != http.StatusOK {
		t.Fatalf("empty body = %d %s", code, raw)
	}
	key, _ = newestKey(t, w, "with-form")
	code, _, raw = startAs(t, w, w.worker, key, `{"variables":{"site":"Basel","amount":5,"kind":"a","urgent":true,"address":{"street":"Hauptstrasse 1"}}}`)
	if code != http.StatusOK {
		t.Fatalf("valid form start = %d %s", code, raw)
	}
}

func TestSelfServiceStartChecksTheStartFormOnTheServer(t *testing.T) {
	w := newSelfStartWorld(t)
	key, _ := newestKey(t, w, "with-form")
	noForm, _ := newestKey(t, w, "no-form")
	before := activeInstances(t, w)
	cases := map[string]struct {
		key  uint64
		body string
		code int
		says string
	}{
		"required field missing":        {key, `{"variables":{"address":{"street":"x"}}}`, http.StatusBadRequest, "site"},
		"required field empty":          {key, `{"variables":{"site":"","address":{"street":"x"}}}`, http.StatusBadRequest, "site"},
		"nested required field missing": {key, `{"variables":{"site":"Basel"}}`, http.StatusBadRequest, "address.street"},
		"text where a number belongs":   {key, `{"variables":{"site":"Basel","amount":"many","address":{"street":"x"}}}`, http.StatusBadRequest, "amount"},
		"number below its minimum":      {key, `{"variables":{"site":"Basel","amount":0,"address":{"street":"x"}}}`, http.StatusBadRequest, "amount"},
		"number above its maximum":      {key, `{"variables":{"site":"Basel","amount":101,"address":{"street":"x"}}}`, http.StatusBadRequest, "amount"},
		"text above its maximum length": {key, `{"variables":{"site":"` + strings.Repeat("x", 21) + `","address":{"street":"x"}}}`, http.StatusBadRequest, "site"},
		"option not on the list":        {key, `{"variables":{"site":"Basel","kind":"z","address":{"street":"x"}}}`, http.StatusBadRequest, "kind"},
		"text where a checkbox belongs": {key, `{"variables":{"site":"Basel","urgent":"yes","address":{"street":"x"}}}`, http.StatusBadRequest, "urgent"},
		"a field the form only shows":   {key, `{"variables":{"site":"Basel","approved":true,"address":{"street":"x"}}}`, http.StatusBadRequest, "approved"},
		"a variable the form lacks":     {key, `{"variables":{"site":"Basel","role":"admin","address":{"street":"x"}}}`, http.StatusBadRequest, "role"},
		"a path beside a form field":    {key, `{"variables":{"site":"Basel","address":{"street":"x","verified":true}}}`, http.StatusBadRequest, "address.verified"},
		"values for a form-less start":  {noForm, `{"variables":{"x":1}}`, http.StatusBadRequest, "without values"},
		"unknown top-level field":       {key, `{"variables":{},"processId":"other"}`, http.StatusBadRequest, "processId"},
		"not json":                      {key, `{"variables":`, http.StatusBadRequest, "JSON"},
		"trailing data":                 {key, `{"variables":{}} {}`, http.StatusBadRequest, "trailing"},
	}
	for name, c := range cases {
		code, _, raw := startAs(t, w, w.worker, c.key, c.body)
		if code != c.code || !strings.Contains(raw, c.says) {
			t.Errorf("%s = %d %s, want %d naming %q", name, code, raw, c.code, c.says)
		}
	}
	if got := activeInstances(t, w); got != before {
		t.Fatalf("refused starts created instances: %d -> %d", before, got)
	}
}

func TestSelfServiceStartRefusesForgedAndStaleKeys(t *testing.T) {
	w := newSelfStartWorld(t)
	before := activeInstances(t, w)
	granted, _ := newestKey(t, w, "no-form")
	other, _ := newestKey(t, w, "invoice-check")          // another application's process, never granted
	ownedElsewhere, _ := newestKey(t, w, "owned-process") // the modeler's application
	notGranted, _ := newestKey(t, w, "safety-inspection") // this application, not granted

	for name, key := range map[string]uint64{
		"another application's process":   other,
		"a process of a private app":      ownedElsewhere,
		"an ungranted process of the app": notGranted,
	} {
		if code, _, raw := startAs(t, w, w.worker, key, `{}`); code != http.StatusForbidden {
			t.Errorf("%s = %d %s, want 403", name, code, raw)
		}
	}
	for name, path := range map[string]string{
		"unknown key":   selfStartPath + "999999/instances",
		"malformed key": selfStartPath + "not-a-key/instances",
	} {
		if code, body := cReq(t, w.worker, w.ts, "POST", path, `{}`); code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", name, code, body)
		}
	}
	// A superseded version cannot be used as a bypass, granted or not.
	if code, body := cReqTyped(t, w.admin, w.ts, "POST", "/api/v1/deployments?projectId="+w.fieldApp, "application/xml", strings.Replace(waitingBPMN("no-form", ""), `name="Handle"`, `name="Handle again"`, 1)); code != http.StatusOK {
		t.Fatalf("redeploy = %d %s", code, body)
	}
	newer, _ := newestKey(t, w, "no-form")
	if newer == granted {
		t.Fatal("redeploy minted no new version")
	}
	if code, _, raw := startAs(t, w, w.worker, granted, `{}`); code != http.StatusConflict {
		t.Fatalf("superseded version = %d %s, want 409", code, raw)
	}
	// Deactivated (ADR-0119): refused until reactivated.
	active := "/api/v1/processes/" + jsonNum(newer) + "/active"
	if code, body := cReq(t, w.admin, w.ts, "PUT", active, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate = %d %s", code, body)
	}
	if code, _, raw := startAs(t, w, w.worker, newer, `{}`); code != http.StatusConflict {
		t.Fatalf("deactivated = %d %s, want 409", code, raw)
	}
	if got := activeInstances(t, w); got != before {
		t.Fatalf("refused starts created instances: %d -> %d", before, got)
	}
	if code, body := cReq(t, w.admin, w.ts, "PUT", active, `{"active":true}`); code != http.StatusOK {
		t.Fatalf("reactivate = %d %s", code, body)
	}
	if code, _, raw := startAs(t, w, w.worker, newer, `{}`); code != http.StatusOK {
		t.Fatalf("reactivated = %d %s", code, raw)
	}
	// Revoked: the very next request is refused, with no listing in between.
	w.revoke(t, w.fieldApp, "no-form")
	if code, _, raw := startAs(t, w, w.worker, newer, `{}`); code != http.StatusForbidden {
		t.Fatalf("after revocation = %d %s, want 403", code, raw)
	}
}

func TestSelfServiceStartNeedsAPersonsOwnSession(t *testing.T) {
	w := newSelfStartWorld(t)
	key, _ := newestKey(t, w, "no-form")
	path := selfStartPath + jsonNum(key) + "/instances"
	if code, _ := cReq(t, newClient(t), w.ts, "POST", path, `{}`); code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", code)
	}
	code, minted := cReq(t, w.admin, w.ts, "POST", "/api/v1/api-tokens", `{"name":"ci","scope":"full","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint token = %d %s", code, minted)
	}
	tok, _ := decodeMap(t, minted)["token"].(string)
	if code, body := bearerReq(t, w.ts, "POST", path, `{}`, tok); code != http.StatusForbidden {
		t.Fatalf("machine token = %d %s, want 403", code, body)
	}
	opsID := createUserWithRoles(t, w.admin, w.ts.URL, "opsonly", `["operator"]`)
	w.grant(t, w.fieldApp, "no-form", `{"userIds":["`+w.workerID+`","`+opsID+`"]}`)
	ops := signInAs(t, w.ts.URL, "opsonly", "a-password-that-is-long")
	if code, _ := cReq(t, ops, w.ts, "POST", path, `{}`); code != http.StatusForbidden {
		t.Fatalf("account without the user role = %d, want 403", code)
	}
}

// TestOperatorStartRoutesAreUnchanged: the self-service route is a second door, not
// a widening of the first. A task user with a grant still cannot use the operator's.
func TestOperatorStartRoutesAreUnchanged(t *testing.T) {
	w := newSelfStartWorld(t)
	key, _ := newestKey(t, w, "no-form")
	if code, _ := cReq(t, w.worker, w.ts, "POST", "/api/v1/processes/"+jsonNum(key)+"/instances", `{}`); code != http.StatusForbidden {
		t.Fatalf("task user on the operator start = %d, want 403", code)
	}
	if code, _ := cReq(t, w.worker, w.ts, "POST", "/api/v1/instances", `{"processId":"no-form"}`); code != http.StatusForbidden {
		t.Fatalf("task user on the operator start by id = %d, want 403", code)
	}
	if code, body := cReq(t, w.modeler, w.ts, "POST", "/api/v1/processes/"+jsonNum(key)+"/instances", `{"variables":{"anything":1}}`); code != http.StatusOK {
		t.Fatalf("operator start = %d %s, want it to work as before", code, body)
	}
}

func TestSelfServiceStartIsNotOfferedWithoutAuthentication(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, selfStartPath+"1/instances", `{}`, "application/json")
	if code != http.StatusNotFound || !strings.Contains(string(body), "needs authentication") {
		t.Fatalf("open server = %d %s, want 404 saying it needs authentication", code, body)
	}
}

// TestSelfServiceRefusesAnApplicationActingForThePerson: an application a person
// connected over OAuth carries their id and roles, so a grant to them would match it.
// It is still not them, and the self-service routes are the person's own session
// alone (ADR-0516) — the gate, not the grant, is what refuses it here.
func TestSelfServiceRefusesAnApplicationActingForThePerson(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	workerID := createUserWithRoles(t, admin, ts.URL, "fieldworker", `["user"]`)
	app := appNamed(t, admin, ts, "Field Operations")
	publishProcessInto(t, admin, ts, app, "safety-inspection")
	if code, body := cReq(t, admin, ts, "PUT", grantPath(app, "safety-inspection"), `{"userIds":["`+workerID+`"],"expectedRevision":0}`); code != http.StatusOK {
		t.Fatalf("grant = %d %s", code, body)
	}
	person := signInAs(t, ts.URL, "fieldworker", "a-password-that-is-long")
	_, list := cReq(t, person, ts, "GET", "/api/v1/me/startable-processes", "")
	var page startablePage
	if err := json.Unmarshal(list, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("the person's own list = %s", list)
	}
	access := grantFor(t, person, ts.URL, clientID, clientSecret)
	if code, body := bearerReq(t, ts, "GET", "/api/v1/me/startable-processes", "", access); code != http.StatusForbidden {
		t.Fatalf("connected application listing = %d %s, want 403", code, body)
	}
	if code, body := bearerReq(t, ts, "POST", selfStartPath+jsonNum(page.Items[0].Key)+"/instances", `{}`, access); code != http.StatusForbidden {
		t.Fatalf("connected application starting = %d %s, want 403", code, body)
	}
	if code, body := cReq(t, person, ts, "POST", selfStartPath+jsonNum(page.Items[0].Key)+"/instances", `{}`); code != http.StatusOK {
		t.Fatalf("the person's own start = %d %s", code, body)
	}
}
