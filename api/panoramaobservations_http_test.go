package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// observationDoc mirrors the wire contract of the observation route. Declared here
// rather than imported so the test pins what a browser parses.
type observationDoc struct {
	ContractVersion int   `json:"contractVersion"`
	ObservedAt      int64 `json:"observedAt"`
	Observations    []struct {
		ElementID string            `json:"elementId"`
		Key       string            `json:"key"`
		Value     string            `json:"value"`
		Source    string            `json:"source"`
		State     string            `json:"state"`
		Severity  string            `json:"severity"`
		Reason    string            `json:"reason"`
		Detail    map[string]string `json:"detail"`
	} `json:"observations"`
	Summary struct {
		OK, Attention, Critical, Unknown int
	} `json:"summary"`
	Unavailable []struct {
		State string `json:"state"`
	} `json:"unavailable"`
}

func getObservations(t *testing.T, ts *httptest.Server, modelID string) observationDoc {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+modelID+"/observations", "", "")
	if code != http.StatusOK {
		t.Fatalf("observations status = %d, body = %s", code, body)
	}
	var doc observationDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode observations: %v (%s)", err, body)
	}
	return doc
}

func observedValue(t *testing.T, doc observationDoc, key, value string) (state, reason string, detail map[string]string) {
	t.Helper()
	for _, o := range doc.Observations {
		if o.Key == key && o.Value == value {
			return o.State, o.Reason, o.Detail
		}
	}
	t.Fatalf("no observation for %s=%s in %+v", key, value, doc.Observations)
	return "", "", nil
}

// observedArchiMate is one model binding an application, a process, a release and
// this server's runtime — the four kinds this build can actually observe. Each
// element carries the ArchiMate type that key is allowed on (ADR-0189 §4's
// allowedOn); a process id sits on a BusinessProcess, and putting it anywhere else
// is refused by the extractor before any of this is reached.
func observedArchiMate(appID, processID, releaseID, runtimeID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="id-model">
  <name xml:lang="en">Observed</name>
  <elements>
    <element identifier="id-app" xsi:type="ApplicationComponent">
      <name xml:lang="en">Billing</name>
      <properties><property propertyDefinitionRef="propid-app"><value xml:lang="en">` + appID + `</value></property></properties>
    </element>
    <element identifier="id-proc" xsi:type="BusinessProcess">
      <name xml:lang="en">Invoice</name>
      <properties><property propertyDefinitionRef="propid-proc"><value xml:lang="en">` + processID + `</value></property></properties>
    </element>
    <element identifier="id-rel" xsi:type="Artifact">
      <name xml:lang="en">Shipped</name>
      <properties><property propertyDefinitionRef="propid-rel"><value xml:lang="en">` + releaseID + `</value></property></properties>
    </element>
    <element identifier="id-node" xsi:type="Node">
      <name xml:lang="en">Atlas</name>
      <properties><property propertyDefinitionRef="propid-rt"><value xml:lang="en">` + runtimeID + `</value></property></properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="propid-app" type="string"><name xml:lang="en">atlas.applicationId</name></propertyDefinition>
    <propertyDefinition identifier="propid-proc" type="string"><name xml:lang="en">atlas.processId</name></propertyDefinition>
    <propertyDefinition identifier="propid-rel" type="string"><name xml:lang="en">atlas.releaseId</name></propertyDefinition>
    <propertyDefinition identifier="propid-rt" type="string"><name xml:lang="en">atlas.runtimeId</name></propertyDefinition>
  </propertyDefinitions>
</model>`
}

// TestObservationsSeeTheInstanceThroughTheModel is P4b end to end. An application
// is published as a release, the model binds to all of it, and the observation
// document says what each bound thing is currently doing — the question a drawing
// can never answer about itself.
func TestObservationsSeeTheInstanceThroughTheModel(t *testing.T) {
	ts := newTestServer(t)
	node := getNode(t, ts)
	appID, processID, releaseID, defKey := publishedApplication(t, ts)
	modelID := observedModel(t, ts, appID, appID, processID, releaseID, node.ID)

	doc := getObservations(t, ts, modelID)
	if doc.ObservedAt == 0 {
		t.Error("the document does not say when it was read")
	}
	// Nothing is out of this document's reach: it asks peers, so every one of
	// ADR-0189 §6's states is reachable in it, and the empty list is that claim.
	if len(doc.Unavailable) != 0 {
		t.Errorf("the document declares %+v unavailable", doc.Unavailable)
	}

	// The application is running and nothing is parked.
	if state, reason, detail := observedValue(t, doc, "atlas.applicationId", appID); state != "healthy" {
		t.Errorf("application state = %q (%s, detail %v), want healthy", state, reason, detail)
	}
	// The process is deployed, and its detail carries the numbers behind the word.
	state, _, detail := observedValue(t, doc, "atlas.processId", processID)
	if state != "healthy" || detail["version"] == "" {
		t.Errorf("process = %q, detail = %v", state, detail)
	}
	// This server is answering, so the runtime it binds to is healthy by the only
	// evidence a runtime has: it is here.
	if state, _, _ := observedValue(t, doc, "atlas.runtimeId", node.ID); state != "healthy" {
		t.Errorf("runtime state = %q, want healthy", state)
	}
	// The release is what is deployed.
	if state, reason, _ := observedValue(t, doc, "atlas.releaseId", releaseID); state != "healthy" {
		t.Errorf("release state = %q (%s), want healthy", state, reason)
	}

	// Parking a token moves both the process and the application off healthy — the
	// application because it aggregates what its processes report.
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey),
		"{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status = %d, body = %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("list tasks: %v (status = %d, body = %s)", err, code, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key),
		`{"retries":0,"message":"boom"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status = %d, body = %s", code, body)
	}

	doc = getObservations(t, ts, modelID)
	if state, reason, _ := observedValue(t, doc, "atlas.processId", processID); state != "degraded" {
		t.Errorf("parked process = %q (%s), want degraded", state, reason)
	}
	if state, reason, _ := observedValue(t, doc, "atlas.applicationId", appID); state != "degraded" {
		t.Errorf("application over a parked process = %q (%s), want degraded", state, reason)
	}
	if doc.Summary.Attention < 2 {
		t.Errorf("summary = %+v, want the process and its application", doc.Summary)
	}
}

// TestReleaseObservationReportsDriftFromWhatShipped is the comparison ADR-0189 §6
// names in its own words, and the one finding neither half can make alone: the
// model does not know what is deployed, and the release record does not know it
// has been superseded. Redeploying the same process moves the release off healthy.
func TestReleaseObservationReportsDriftFromWhatShipped(t *testing.T) {
	ts := newTestServer(t)
	node := getNode(t, ts)
	appID, processID, releaseID, _ := publishedApplication(t, ts)
	modelID := observedModel(t, ts, appID, appID, processID, releaseID, node.ID)

	if state, _, _ := observedValue(t, getObservations(t, ts, modelID),
		"atlas.releaseId", releaseID); state != "healthy" {
		t.Fatalf("a freshly published release = %q, want healthy", state)
	}

	// Deploying the same process again supersedes what the release shipped. Nothing
	// about the model or the release record changed; the instance moved.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+appID,
		incidentUserTaskBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("redeploy: status = %d, body = %s", code, body)
	}

	doc := getObservations(t, ts, modelID)
	state, reason, detail := observedValue(t, doc, "atlas.releaseId", releaseID)
	if state != "degraded" {
		t.Fatalf("a superseded release = %q (%s), want degraded", state, reason)
	}
	if detail["superseded"] != "1" {
		t.Errorf("detail = %v, want it to count what moved on", detail)
	}
	if !strings.Contains(reason, "moved on") {
		t.Errorf("reason = %q, want it to say the instance moved on", reason)
	}
	// Superseded is not an outage: a newer version running is normal, and the
	// finding is that this release is no longer what an element bound to it says.
	if state == "not-ready" {
		t.Error("a superseded release is reported as unable to work")
	}
}

// TestObservationsHonourTheSharingScope: the projection reads the same resources
// binding resolution does, so it must honour the same scope. Otherwise it becomes
// a way to learn that a resource outside your access is unhealthy.
func TestObservationsDoNotLeakAcrossASharingScope(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	// Owned by an application the caller can read, but binding four ids this server
	// does not hold — which is indistinguishable, from here, from four ids held by
	// somebody whose scope withholds them. That indistinguishability is the point:
	// it is exactly what this caller is allowed to be unable to tell apart.
	modelID := observedModel(t, ts, app.ID,
		"app-nobody-has", "process-nobody-has", "rel-nobody-has", "rt-elsewhere")

	doc := getObservations(t, ts, modelID)
	// Every binding names something this server does not have, so every observation
	// is unbound — and each says so rather than being dropped, or the model would
	// look like one with nothing wrong.
	if len(doc.Observations) != 4 {
		t.Fatalf("%d observations for 4 bindings: %+v", len(doc.Observations), doc.Observations)
	}
	for _, o := range doc.Observations {
		if o.State != "unbound" {
			t.Errorf("%s=%s observed as %q, want unbound", o.Key, o.Value, o.State)
		}
		if o.Severity != "unknown" {
			t.Errorf("%s=%s carries severity %q; unbound is neutral", o.Key, o.Value, o.Severity)
		}
	}
	if doc.Summary.Unknown != 4 {
		t.Errorf("summary = %+v, want four unknowns", doc.Summary)
	}
}

// publishedApplication creates an application whose one draft — a process with a
// user task, so a token can be parked in it — is published as release 1. Publishing
// is what mints a release with members at all: a release records what shipped from
// the application's drafts, so a process deployed straight to the engine belongs to
// no release and there would be nothing to compare against.
func publishedApplication(t *testing.T, ts *httptest.Server) (appID, processID, releaseID string, defKey uint64) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	if code, body = doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app.ID,
		incidentUserTaskBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: status = %d, body = %s", code, body)
	}
	code, published := publishApp(t, ts, app.ID, "first release")
	if code != http.StatusOK || published.Release == nil || len(published.Release.Members) == 0 {
		t.Fatalf("publish: status = %d, result = %+v", code, published)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	var procs []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(body, &procs); err != nil || len(procs) == 0 {
		t.Fatalf("list processes: %v (status = %d, body = %s)", err, code, body)
	}
	return app.ID, published.Release.Members[0].Ref, published.Release.ID, procs[0].Key
}

// observedModel imports a model binding the four kinds this build observes. owner
// is the application the model belongs to, which decides who may read it; the
// bound ids are separate arguments because a model may perfectly well describe
// resources other than its own owner's — and a test of what happens when it names
// resources that are not here needs those two to differ.
func observedModel(t *testing.T, ts *httptest.Server, owner, boundApp, processID, releaseID, runtimeID string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": owner, "name": "Observed",
		"xml": observedArchiMate(boundApp, processID, releaseID, runtimeID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model: status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	return model.ID
}

// widerArchiMate binds the two kinds observedArchiMate leaves out — a configured
// worker and a deployment target — so the projection's remaining local sources are
// exercised against a real server rather than against a hand-built fact map.
func widerArchiMate(appID, workerID, targetID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="id-model">
  <name xml:lang="en">Wider</name>
  <elements>
    <element identifier="id-app" xsi:type="ApplicationComponent">
      <name xml:lang="en">Billing</name>
      <properties><property propertyDefinitionRef="propid-app"><value xml:lang="en">` + appID + `</value></property></properties>
    </element>
    <element identifier="id-worker" xsi:type="ApplicationService">
      <name xml:lang="en">Mail</name>
      <properties><property propertyDefinitionRef="propid-conn"><value xml:lang="en">` + workerID + `</value></property></properties>
    </element>
    <element identifier="id-target" xsi:type="Node">
      <name xml:lang="en">Production</name>
      <properties><property propertyDefinitionRef="propid-tgt"><value xml:lang="en">` + targetID + `</value></property></properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="propid-app" type="string"><name xml:lang="en">atlas.applicationId</name></propertyDefinition>
    <propertyDefinition identifier="propid-conn" type="string"><name xml:lang="en">atlas.connectorId</name></propertyDefinition>
    <propertyDefinition identifier="propid-tgt" type="string"><name xml:lang="en">atlas.deploymentTargetId</name></propertyDefinition>
  </propertyDefinitions>
</model>`
}

// TestObservationsCoverWorkersTargetsAndAnEmptyApplication pins the three answers
// the happy path does not reach, and each is a different kind of honest:
//
//   - a worker reports the same state the landscape mesh gives it, because two
//     surfaces answering one question differently is worse than either alone;
//   - a deployment target reports *unbound*, because its readiness is only knowable
//     by asking it and this build does not — inventing a verdict would be worse
//     than admitting the gap;
//   - an application with nothing deployed is *not ready* rather than healthy: the
//     model says it exists and the instance has nothing of it running, which is the
//     desired-versus-observed gap the whole projection is for.
func TestObservationsCoverWorkersTargetsAndAnEmptyApplication(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"ops-mail","kind":"mail","endpoint":"smtp://relay.test:25","sender":"ops@example.test"}`,
		"application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker: status = %d, body = %s", code, body)
	}
	var worker struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &worker); err != nil {
		t.Fatalf("decode worker: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/targets",
		`{"name":"Production","baseUrl":"https://atlas.example.test"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create target: status = %d, body = %s", code, body)
	}
	var target struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &target); err != nil {
		t.Fatalf("decode target: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": app.ID, "name": "Wider",
		"xml": widerArchiMate(app.ID, worker.ID, target.ID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model: status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	doc := getObservations(t, ts, model.ID)

	state, reason, detail := observedValue(t, doc, "atlas.connectorId", worker.ID)
	if state != "healthy" {
		t.Errorf("a usable worker = %q (%s)", state, reason)
	}
	if detail["name"] != "ops-mail" || detail["workerType"] != "mail" {
		t.Errorf("worker detail = %v, want its name and type", detail)
	}
	// Never its endpoint or its credential reference: this document is opened by
	// anyone with modeler access.
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/observations", "", "")
	if code != http.StatusOK {
		t.Fatalf("observations status = %d", code)
	}
	for _, leak := range []string{"relay.test", "smtp://", "atlas.example.test"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the document leaks %q: %s", leak, raw)
		}
	}

	// The target is asked now, and there is nothing at that address, so the honest
	// answer is unreachable rather than a blank row. Unreachable is *attention*,
	// not critical (ADR-0211 §4): "I could not reach it" and "it is broken" are
	// different findings.
	state, reason, _ = observedValue(t, doc, "atlas.deploymentTargetId", target.ID)
	if state != "unreachable" {
		t.Errorf("a target that does not answer = %q (%s), want unreachable", state, reason)
	}
	// And the reason says what kind of failure it was without repeating the address
	// back. The base URL is this operator's infrastructure map, and this document is
	// opened by anyone with modeler access — the leak check above is the assertion,
	// this is the readability half of it.
	if reason == "" || strings.Contains(reason, "http") {
		t.Errorf("target reason = %q, want a category rather than a URL", reason)
	}

	state, reason, detail = observedValue(t, doc, "atlas.applicationId", app.ID)
	if state != "not-ready" {
		t.Fatalf("an application with nothing deployed = %q (%s), want not-ready", state, reason)
	}
	if detail["processes"] != "0" {
		t.Errorf("detail = %v, want it to say nothing is deployed", detail)
	}
	if doc.Summary.Critical < 1 {
		t.Errorf("summary = %+v, want the empty application counted as critical", doc.Summary)
	}
}

// TestReleaseObservationReportsAProcessThatIsGone is the third release answer.
// Undeploying what a release shipped leaves the release describing something this
// server cannot run — absent, not merely superseded, and the two are fixed in
// different places.
func TestReleaseObservationReportsAProcessThatIsGone(t *testing.T) {
	ts := newTestServer(t)
	node := getNode(t, ts)
	appID, processID, releaseID, defKey := publishedApplication(t, ts)
	modelID := observedModel(t, ts, appID, appID, processID, releaseID, node.ID)

	if code, body := doReq(t, ts, http.MethodDelete,
		fmt.Sprintf("/api/v1/processes/%d", defKey), "", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("undeploy: status = %d, body = %s", code, body)
	}

	doc := getObservations(t, ts, modelID)
	state, reason, detail := observedValue(t, doc, "atlas.releaseId", releaseID)
	if state != "not-ready" {
		t.Fatalf("a release whose process is gone = %q (%s), want not-ready", state, reason)
	}
	if detail["absent"] != "1" {
		t.Errorf("detail = %v, want it to count what is missing", detail)
	}
	// The application went with it: nothing of it is deployed any more.
	if state, _, _ := observedValue(t, doc, "atlas.applicationId", appID); state != "not-ready" {
		t.Errorf("application after undeploy = %q, want not-ready", state)
	}
}

// TestObservationsReachARealPeer closes the loop P4a opened. That slice built the
// descriptor a peer asks for; this one is the asking, and the two halves are the
// same code talking to itself — which is the only way to know the contract holds.
//
// It is a second Atlas, not a stub: a stub would prove this server can parse what
// this test wrote, and nothing about whether the descriptor route actually answers
// what the reader expects.
func TestObservationsReachARealPeer(t *testing.T) {
	peer := newTestServer(t)
	peerNode := getNode(t, peer)
	if code, body := doReq(t, peer, http.MethodPut, "/api/v1/node",
		`{"name":"Geneva standby","environment":"staging"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("name the peer: status = %d, body = %s", code, body)
	}

	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/targets",
		mustJSON(t, map[string]any{"name": "Geneva", "baseUrl": peer.URL}), "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create target: status = %d, body = %s", code, body)
	}
	var target struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &target); err != nil {
		t.Fatalf("decode target: %v", err)
	}

	// The model binds the target *and* the peer's own runtime id — the second is
	// only knowable because the peer publishes a stable one, which is what the
	// descriptor is for.
	modelID := observedModel(t, ts, app.ID, app.ID, "no-such-process", "no-such-release", peerNode.ID)
	code, body = doReq(t, ts, http.MethodPut, "/api/v1/panorama/models/"+modelID, mustJSON(t, map[string]any{
		"expectedRevision": 1, "name": "Observed",
		"xml": widerArchiMate(app.ID, "no-such-worker", target.ID),
	}), "application/json")
	if code != http.StatusOK {
		t.Fatalf("rebind the model to the target: status = %d, body = %s", code, body)
	}

	doc := getObservations(t, ts, modelID)
	state, reason, detail := observedValue(t, doc, "atlas.deploymentTargetId", target.ID)
	if state != "healthy" {
		t.Fatalf("a peer that is up = %q (%s)", state, reason)
	}
	// What the peer calls itself, not what this server calls the target: the
	// descriptor exists so a landscape can name a runtime the way its own operator
	// does.
	if detail["node"] != "Geneva standby (staging)" {
		t.Errorf("detail = %v, want the peer's own name", detail)
	}
	if detail["runtimeId"] != peerNode.ID {
		t.Errorf("detail = %v, want the peer's stable id", detail)
	}
	// Never the peer's address: this document is opened by anyone with modeler
	// access, and where a peer lives is this operator's infrastructure map.
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+modelID+"/observations", "", "")
	if code != http.StatusOK {
		t.Fatalf("observations status = %d", code)
	}
	if strings.Contains(string(raw), peer.URL) || strings.Contains(string(raw), "127.0.0.1") {
		t.Errorf("the document carries the peer's address: %s", raw)
	}
}

// TestJobTypeObservationSaysWhatItCanAndCannotSee. A job type is a name for work,
// not a thing that can be well or unwell, so the observation answers the question
// the engine can actually answer — "is this kind of work getting done here" — and
// stops where the evidence stops.
//
// The pair below is the whole point. One type the engine runs itself is healthy on
// knowledge: it built the handler, so there is nothing outside this process to ask.
// One nothing has been seen doing is *unbound* rather than broken, because the
// worker registry is emptied by a restart — a mapping that read an empty registry as
// "nobody serves this" would mark every worker-served kind broken on every restart.
func TestJobTypeObservationSaysWhatItCanAndCannotSee(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": app.ID, "name": "Job types", "xml": jobTypeBoundArchiMate(),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model: status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	doc := getObservations(t, ts, model.ID)

	// The DMN job type is served in this process on any server.
	state, reason, detail := observedValue(t, doc, "atlas.jobType", "io.atlas.dmn")
	if state != "healthy" {
		t.Errorf("a job type the engine runs itself = %q (%s), want healthy", state, reason)
	}
	if !strings.Contains(reason, "runs this job type itself") {
		t.Errorf("reason = %q, want it to say the engine serves this kind", reason)
	}
	if detail["origin"] != "Built-in job type" {
		t.Errorf("detail = %v, want it to say where the type comes from", detail)
	}
	if detail["usedBy"] != "0" {
		t.Errorf("usedBy = %q, want 0 — nothing is deployed on this server", detail["usedBy"])
	}

	// And one this server has never heard of. The binding does not resolve, so the
	// observation is about a resource that is not here — which is drift, reported by
	// the binding resolver, and never a runtime failure invented by this document.
	state, reason, _ = observedValue(t, doc, "atlas.jobType", "nobody.serves.this")
	if state != "unbound" {
		t.Errorf("an unknown job type = %q (%s), want unbound", state, reason)
	}
	if !strings.Contains(reason, "nothing to observe") {
		t.Errorf("reason = %q, want it to say there is nothing here to observe", reason)
	}

	// The document no longer declares that it cannot observe this kind at all: it
	// can, and saying otherwise would be the old excuse outliving its cause.
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/observations", "", "")
	if code != http.StatusOK {
		t.Fatalf("observations status = %d", code)
	}
	if strings.Contains(string(raw), "Nothing on this server observes atlas.jobType") {
		t.Errorf("the document still says it cannot observe job types: %s", raw)
	}
}
