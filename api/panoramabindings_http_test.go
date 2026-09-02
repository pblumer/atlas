package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The binding routes against a real server, which is what exercises the catalog
// collector: the panorama package can test resolution against a hand-built catalog,
// but only a running server proves the catalog is assembled from the right stores
// with the right scopes.

type bindingValue struct {
	Value  string `json:"value"`
	Status string `json:"status"`
	Name   string `json:"name"`
}

type bindingResolution struct {
	ContractVersion int `json:"contractVersion"`
	Unresolved      int `json:"unresolved"`
	Bindings        []struct {
		ElementID string         `json:"elementId"`
		Key       string         `json:"key"`
		Values    []bindingValue `json:"values"`
	} `json:"bindings"`
}

// boundArchiMate binds an Application Component to an application id the test
// substitutes, plus one id nothing on the server has.
func boundArchiMate(applicationID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m-bound">
  <name xml:lang="en">Bound</name>
  <elements>
    <element identifier="app-c" xsi:type="ApplicationComponent">
      <name xml:lang="en">Order Service</name>
      <properties>
        <property propertyDefinitionRef="p-app"><value>` + applicationID + `</value></property>
        <property propertyDefinitionRef="p-app"><value>proj-gone</value></property>
      </properties>
    </element>
    <element identifier="node-1" xsi:type="Node">
      <name xml:lang="en">Runtime</name>
      <properties>
        <property propertyDefinitionRef="p-rt"><value>rt-1</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-app" type="string"><name>atlas.applicationId</name></propertyDefinition>
    <propertyDefinition identifier="p-rt" type="string"><name>atlas.runtimeId</name></propertyDefinition>
  </propertyDefinitions>
</model>`
}

// TestPanoramaBindingsResolveAgainstRealStores is the end-to-end proof of the
// catalog: an application the server actually holds resolves to its name, an id
// nothing holds is missing, and a kind with no catalog at all is unsupported rather
// than missing — the distinction that keeps the resolver from claiming it looked.
func TestPanoramaBindingsResolveAgainstRealStores(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		`{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &appRec); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": appRec.ID, "name": "Bound", "xml": boundArchiMate(appRec.ID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, body)
	}
	var res bindingResolution
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode bindings: %v (%s)", err, body)
	}

	statuses := map[string]string{}
	names := map[string]string{}
	for _, binding := range res.Bindings {
		for _, value := range binding.Values {
			statuses[value.Value] = value.Status
			names[value.Value] = value.Name
		}
	}
	if statuses[appRec.ID] != "resolved" || names[appRec.ID] != "Billing" {
		t.Errorf("bound application = %q/%q, want resolved as Billing", statuses[appRec.ID], names[appRec.ID])
	}
	if statuses["proj-gone"] != "missing" {
		t.Errorf("absent application status = %q, want missing", statuses["proj-gone"])
	}
	// Runtimes became answerable with the node descriptor (ADR-0189 §6): the server
	// knows one runtime for certain, itself, so a binding to some *other* node is
	// now missing rather than unsupported. That is a strictly better answer and a
	// different claim — the server looked this time.
	if statuses["rt-1"] != "missing" {
		t.Errorf("runtime status = %q, want missing now that the node descriptor exists", statuses["rt-1"])
	}
	if res.Unresolved != 2 {
		t.Errorf("unresolved = %d, want the absent application and the absent runtime", res.Unresolved)
	}
}

// TestPanoramaRuntimeBindingResolvesToThisNode closes the gap P3 shipped with. A
// model that binds an ArchiMate node to the Atlas runtime it actually runs on now
// gets a name back, which is the point of the binding: an architect looking at the
// element learns which server it means, not that the question is unanswerable.
func TestPanoramaRuntimeBindingResolvesToThisNode(t *testing.T) {
	ts := newTestServer(t)
	node := getNode(t, ts)

	code, body := doReq(t, ts, http.MethodPut, "/api/v1/node",
		`{"name":"Zurich primary","environment":"production"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("name the node: status = %d, body = %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &appRec); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": appRec.ID, "name": "Runtime bound", "xml": runtimeBoundArchiMate(node.ID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, body)
	}
	var res bindingResolution
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode bindings: %v (%s)", err, body)
	}

	var found bool
	for _, binding := range res.Bindings {
		for _, value := range binding.Values {
			if value.Value != node.ID {
				continue
			}
			found = true
			if value.Status != "resolved" {
				t.Errorf("this node's own id = %q, want resolved", value.Status)
			}
			// The operator's name, qualified by the environment: what somebody
			// reading the model needs in order to recognise the server.
			if value.Name != "Zurich primary (production)" {
				t.Errorf("runtime name = %q, want the operator's name and environment", value.Name)
			}
		}
	}
	if !found {
		t.Fatalf("no binding for this node's id %q in %+v", node.ID, res.Bindings)
	}
	if res.Unresolved != 0 {
		t.Errorf("unresolved = %d, want none", res.Unresolved)
	}
}

// runtimeBoundArchiMate is one Node element bound to an Atlas runtime id — the
// shape ADR-0189 §4 defines for atlas.runtimeId.
func runtimeBoundArchiMate(runtimeID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="id-model">
  <name xml:lang="en">Runtime bound</name>
  <elements>
    <element identifier="id-node" xsi:type="Node" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
      <name xml:lang="en">Atlas</name>
      <properties>
        <property propertyDefinitionRef="propid-runtime"><value xml:lang="en">` + runtimeID + `</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="propid-runtime" type="string">
      <name xml:lang="en">atlas.runtimeId</name>
    </propertyDefinition>
  </propertyDefinitions>
</model>`
}

// A connector's endpoint reaches the catalog's source but must never reach a
// binding payload — the same bound the landscape mesh keeps.
func TestPanoramaBindingCandidatesCarryNoEndpoint(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/connectors",
		`{"name":"ops-mail","kind":"mail","endpoint":"smtp://internal-relay.corp.example:587","sender":"ops@example.test"}`,
		"application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create connector status = %d, body = %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"EA"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &appRec)

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": appRec.ID, "name": "Bound", "xml": boundArchiMate(appRec.ID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &model)

	code, raw := doReq(t, ts, http.MethodGet,
		"/api/v1/panorama/models/"+model.ID+"/bindings/candidates?key=atlas.connectorId", "", "")
	if code != http.StatusOK {
		t.Fatalf("candidates status = %d, body = %s", code, raw)
	}
	if !strings.Contains(string(raw), "ops-mail") {
		t.Errorf("candidates = %s, want the configured worker", raw)
	}
	for _, leak := range []string{"internal-relay", "corp.example", "smtp://", "587"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("candidates leak %q: %s", leak, raw)
		}
	}
}

// Setting a binding through the API stores it in the document, and the round trip
// proves the writer and the reader agree on the wire as well as in the package.
func TestPanoramaSetBindingRoundTripsThroughTheAPI(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"EA"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &appRec)

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": appRec.ID, "name": "Bound", "xml": boundArchiMate(appRec.ID),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	_ = json.Unmarshal(body, &model)

	code, body = doReq(t, ts, http.MethodPut, "/api/v1/panorama/models/"+model.ID+"/bindings",
		mustJSON(t, map[string]any{
			"expectedRevision": model.Revision, "elementId": "app-c",
			"key": "atlas.applicationId", "values": []string{appRec.ID},
		}), "application/json")
	if code != http.StatusOK {
		t.Fatalf("set binding status = %d, body = %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, body)
	}
	var res bindingResolution
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, binding := range res.Bindings {
		if binding.ElementID != "app-c" || binding.Key != "atlas.applicationId" {
			continue
		}
		found = true
		// The replacement replaced: the second, absent id is gone.
		if len(binding.Values) != 1 || binding.Values[0].Value != appRec.ID {
			t.Errorf("values = %#v, want only the replacement", binding.Values)
		}
	}
	if !found {
		t.Errorf("binding missing after write: %#v", res.Bindings)
	}
}

// mustJSON marshals a request body, failing the test rather than the request.
func mustJSON(t *testing.T, body any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// A release binding resolves to the application and version it shipped. That
// display name is composed by the server rather than stored anywhere, so it needs a
// real release rather than a hand-built catalog — this publishes one.
func TestPanoramaReleaseBindingResolves(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Billing")

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app,
		releasableBPMN("onboard"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status = %d, body = %s", code, body)
	}
	code, published := publishApp(t, ts, app, "first cut")
	if code != http.StatusOK || published.Release == nil {
		t.Fatalf("publish status = %d, release = %+v", code, published.Release)
	}

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m-rel">
  <name xml:lang="en">Releases</name>
  <elements>
    <element identifier="art-1" xsi:type="Artifact">
      <name xml:lang="en">Shipped bundle</name>
      <properties>
        <property propertyDefinitionRef="p-rel"><value>` + published.Release.ID + `</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-rel" type="string"><name>atlas.releaseId</name></propertyDefinition>
  </propertyDefinitions>
</model>`

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": app, "name": "Releases", "xml": xml,
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, body)
	}
	var res bindingResolution
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(res.Bindings) != 1 || len(res.Bindings[0].Values) != 1 {
		t.Fatalf("bindings = %#v", res.Bindings)
	}
	value := res.Bindings[0].Values[0]
	// The name is the application and the version it shipped, composed on read.
	if value.Status != "resolved" || value.Name != "Billing v1" {
		t.Errorf("release binding = %+v, want resolved as \"Billing v1\"", value)
	}
}

// A deployment target binding resolves and never carries the target's base URL. A
// target is org-wide infrastructure with no sharing scope of its own, so there is
// nothing to filter on — which makes it the one kind where the disclosure bound has
// to be carried by the catalog's shape rather than by an access check.
func TestPanoramaDeploymentTargetBindingResolvesWithoutItsURL(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "EA")

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/targets",
		`{"name":"Production","baseUrl":"https://prod-internal.corp.example","kind":"prod"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Skipf("this server refuses target creation here (status %d): %s", code, body)
	}
	var target struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &target); err != nil {
		t.Fatalf("decode target: %v (%s)", err, body)
	}

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m-t">
  <name xml:lang="en">Targets</name>
  <elements>
    <element identifier="node-1" xsi:type="Node">
      <name xml:lang="en">Production node</name>
      <properties>
        <property propertyDefinitionRef="p-t"><value>` + target.ID + `</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-t" type="string"><name>atlas.deploymentTargetId</name></propertyDefinition>
  </propertyDefinitions>
</model>`

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": app, "name": "Targets", "xml": xml,
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, raw)
	}
	var res bindingResolution
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Bindings) != 1 || res.Bindings[0].Values[0].Name != "Production" {
		t.Fatalf("binding = %#v, want the target resolved by name", res.Bindings)
	}
	for _, leak := range []string{"prod-internal", "corp.example", "https://"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("binding payload leaks the target's base URL (%q): %s", leak, raw)
		}
	}
}

// TestPanoramaJobTypeBindingResolvesAgainstTheEngineTable is the last kind the
// catalog could not answer.
//
// The reason it gave was wrong rather than merely provisional: it said a job type is
// authored in a model rather than registered as a resource, and the engine has kept
// an engine-wide table of them since ADR-0007 — because a job's index on disk has to
// mean the same thing to every definition. So "does this server know that job type"
// was always a lookup, and the Workers view has listed the whole table for as long as
// it has existed.
//
// The consequence is the assertion below: an unknown job type now comes back
// *missing*, which is a claim about this server and a real finding, instead of
// *unsupported*, which was an admission about the resolver.
func TestPanoramaJobTypeBindingResolvesAgainstTheEngineTable(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &appRec); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", mustJSON(t, map[string]any{
		"applicationId": appRec.ID, "name": "Job types", "xml": jobTypeBoundArchiMate(),
	}), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model status = %d, body = %s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/bindings", "", "")
	if code != http.StatusOK {
		t.Fatalf("bindings status = %d, body = %s", code, body)
	}
	var res bindingResolution
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode bindings: %v (%s)", err, body)
	}

	got := map[string]bindingValue{}
	for _, binding := range res.Bindings {
		for _, value := range binding.Values {
			got[value.Value] = value
		}
	}

	// A job type Atlas ships with resolves on any server, before anything is
	// deployed on it: those indices are compile-time constants every build reserves.
	known := got["io.atlas.dmn"]
	if known.Status != "resolved" {
		t.Errorf("io.atlas.dmn = %q, want resolved", known.Status)
	}
	// Named by what it is rather than by repeating the id: a job type's id *is* its
	// name, and a panel printing one string twice tells nobody anything.
	if known.Name != "Built-in job type" {
		t.Errorf("io.atlas.dmn name = %q, want it to say what sort of job type this is", known.Name)
	}

	// And one nothing here has ever seen. Missing rather than unsupported — the
	// resolver looked, so this is a fact about the server and something a model can
	// act on, not an admission that the question was never asked.
	unknown := got["nobody.serves.this"]
	if unknown.Status != "missing" {
		t.Errorf("an unknown job type = %q, want missing", unknown.Status)
	}
	if res.Unresolved != 1 {
		t.Errorf("unresolved = %d, want exactly the unknown one", res.Unresolved)
	}
}

// jobTypeBoundArchiMate is one Application Service bound to two job types — one the
// engine reserves, one nothing has ever registered.
func jobTypeBoundArchiMate() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="id-model">
  <name xml:lang="en">Job types</name>
  <elements>
    <element identifier="id-svc" xsi:type="ApplicationService">
      <name xml:lang="en">Decisioning</name>
      <properties>
        <property propertyDefinitionRef="propid-jobtype"><value xml:lang="en">io.atlas.dmn</value></property>
        <property propertyDefinitionRef="propid-jobtype"><value xml:lang="en">nobody.serves.this</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="propid-jobtype" type="string">
      <name xml:lang="en">atlas.jobType</name>
    </propertyDefinition>
  </propertyDefinitions>
</model>`
}
