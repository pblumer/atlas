package panorama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bindingRequest builds a request with the {id} path value set, the way the router
// supplies it. The shared request helper does not, and every binding handler reads
// it, so the tests would otherwise all be testing the not-found path.
func bindingRequest(t *testing.T, handler http.HandlerFunc, method string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/panorama/models/" + testModelID + "/bindings"
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetPathValue("id", testModelID)
	return invoke(t, handler, req, status)
}

// boundModelXML has one bound element and one unbound one, so a single fixture
// covers a resolved binding and an element with none.
const boundModelXML = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Bound</name>
  <elements>
    <element identifier="app-c" xsi:type="ApplicationComponent">
      <name xml:lang="en">Order Service</name>
      <properties>
        <property propertyDefinitionRef="p-app"><value>proj-abc</value></property>
      </properties>
    </element>
    <element identifier="bp-1" xsi:type="BusinessProcess"><name xml:lang="en">Fulfil</name></element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-app" type="string"><name>atlas.applicationId</name></propertyDefinition>
  </propertyDefinitions>
</model>`

// seedBound stores boundModelXML under the fixture's model id.
func seedBound(t *testing.T, fx *serviceFixture, applicationID string) {
	t.Helper()
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: applicationID, Name: "Bound",
		Notation: NotationArchiMate32, Revision: 1, XML: boundModelXML,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestHandleBindingsResolvesAgainstTheCatalog(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	fx.catalog = Catalog{Applications: map[string]ResourceRef{
		"proj-abc": {ID: "proj-abc", Name: "Billing", CanView: true},
	}}

	rec := bindingRequest(t, fx.service.HandleBindings, http.MethodGet, nil, http.StatusOK)

	var res Resolution
	decodeResponse(t, rec, &res)
	if len(res.Bindings) != 1 || res.Bindings[0].Values[0].Name != "Billing" {
		t.Fatalf("resolution = %#v", res)
	}
	if res.ContractVersion != BindingContractVersion {
		t.Errorf("contract version = %d", res.ContractVersion)
	}
}

// A model the caller may not see is a 404 on this route as on every other, so an
// application scope they cannot see is not disclosed by a different status.
func TestHandleBindingsHidesAModelTheCallerCannotSee(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "hidden")

	bindingRequest(t, fx.service.HandleBindings, http.MethodGet, nil, http.StatusNotFound)
}

func TestHandleSetBindingWritesAndBumpsTheRevision(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	rec := bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, map[string]any{
		"expectedRevision": 1,
		"elementId":        "bp-1",
		"key":              KeyProcessID,
		"values":           []string{"order-fulfilment"},
	}, http.StatusOK)

	var summary Summary
	decodeResponse(t, rec, &summary)
	if summary.Revision != 2 {
		t.Errorf("revision = %d, want 2", summary.Revision)
	}
	stored, _, err := fx.store.Get(testModelID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	set, err := ExtractBindings([]byte(stored.XML))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != "order-fulfilment" {
		t.Errorf("stored binding = %v", got)
	}
}

// Writing needs edit rights, not merely view rights: a viewer who could bind a
// model to resources would be authoring it.
func TestHandleSetBindingRefusesAViewer(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "viewer")

	bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, map[string]any{
		"expectedRevision": 1, "elementId": "bp-1", "key": KeyProcessID, "values": []string{"x"},
	}, http.StatusForbidden)
}

// The same optimistic revision check every other write on this resource uses: two
// browser sessions must not be able to overwrite each other silently.
func TestHandleSetBindingDetectsARevisionConflict(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, map[string]any{
		"expectedRevision": 99, "elementId": "bp-1", "key": KeyProcessID, "values": []string{"x"},
	}, http.StatusConflict)
}

// A contract violation is the caller's error, and the message names what is wrong
// rather than failing as an opaque 500 from deep inside the writer.
func TestHandleSetBindingRefusesAKeyOnTheWrongElement(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	rec := bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, map[string]any{
		"expectedRevision": 1, "elementId": "bp-1", "key": KeyApplicationID, "values": []string{"proj-abc"},
	}, http.StatusBadRequest)

	if body := rec.Body.String(); !strings.Contains(body, "BusinessProcess") {
		t.Errorf("refusal = %s, want it to name the element type", body)
	}
}

// Every way a caller can get the request wrong has its own refusal, and each names
// what is wrong. A single generic 400 would leave a client guessing which field it
// got wrong, and a 500 would blame the server for the caller's mistake.
func TestHandleSetBindingRefusesBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{"missing revision", map[string]any{
			"elementId": "bp-1", "key": KeyProcessID, "values": []string{"x"}}, "expectedRevision"},
		{"missing element", map[string]any{
			"expectedRevision": 1, "key": KeyProcessID, "values": []string{"x"}}, "elementId"},
		{"unknown key", map[string]any{
			"expectedRevision": 1, "elementId": "bp-1", "key": "atlas.credentialRef",
			"values": []string{"vault://x"}}, "atlas.credentialRef"},
		{"unknown element", map[string]any{
			"expectedRevision": 1, "elementId": "nope", "key": KeyProcessID,
			"values": []string{"x"}}, "nope"},
		{"blank value", map[string]any{
			"expectedRevision": 1, "elementId": "bp-1", "key": KeyProcessID,
			"values": []string{"  "}}, "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newServiceFixture(t)
			seedBound(t, fx, "app-1")
			rec := bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, tc.body, http.StatusBadRequest)
			if body := rec.Body.String(); !strings.Contains(body, tc.want) {
				t.Errorf("refusal = %s, want it to name %q", body, tc.want)
			}
		})
	}
}

// The refusal for an unknown key must not echo the value: it is shaped like a
// credential precisely because somebody tried to use it as one.
func TestHandleSetBindingDoesNotEchoARejectedSecret(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	rec := bindingRequest(t, fx.service.HandleSetBinding, http.MethodPut, map[string]any{
		"expectedRevision": 1, "elementId": "app-c", "key": "atlas.credentialRef",
		"values": []string{"vault://prod/super-secret"},
	}, http.StatusBadRequest)

	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Errorf("refusal echoes the rejected value: %s", rec.Body)
	}
}

// A catalog the server cannot build is a server fault, and must not be answered
// with an empty catalog: every binding would then resolve as missing, which reads
// as a broken model rather than as a failed read.
func TestHandleBindingsReportsACatalogFailure(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	fx.catalogErr = errStub

	rec := bindingRequest(t, fx.service.HandleBindings, http.MethodGet, nil, http.StatusInternalServerError)
	if !strings.Contains(rec.Body.String(), "catalog is on fire") {
		t.Errorf("body = %s, want the underlying cause", rec.Body)
	}
}

// A stored document that no longer parses is a server-side fault too, not an empty
// binding list: "this model declares nothing" is a different answer from "this
// model could not be read".
func TestHandleBindingsReportsAnUnreadableDocument(t *testing.T) {
	fx := newServiceFixture(t)
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: "app-1", Name: "Broken",
		Notation: NotationArchiMate32, Revision: 1, XML: "<model><elements>",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bindingRequest(t, fx.service.HandleBindings, http.MethodGet, nil, http.StatusInternalServerError)
}

// The same runloop.Do guard the landscape mesh carries: on a closing loop the
// closure never runs, and resolving against the resulting empty catalog would
// report every binding as missing.
func TestHandleBindingsRefusesWhenTheLoopIsClosing(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	fx.service.loop = stoppedLoop()

	bindingRequest(t, fx.service.HandleBindings, http.MethodGet, nil, http.StatusServiceUnavailable)
}

// forKey must answer for every key the contract defines, including the two whose
// catalogs nothing supplies yet — those return nil, which is what the resolver
// reads as unsupported.
func TestCatalogForKeyCoversTheWholeContract(t *testing.T) {
	full := Catalog{
		Applications: map[string]ResourceRef{}, Processes: map[string]ResourceRef{},
		Connectors: map[string]ResourceRef{}, JobTypes: map[string]ResourceRef{},
		Runtimes: map[string]ResourceRef{}, Targets: map[string]ResourceRef{},
		Releases: map[string]ResourceRef{},
	}
	for _, key := range BindingKeys() {
		if full.forKey(key) == nil {
			t.Errorf("forKey(%q) = nil for a fully supplied catalog", key)
		}
	}
	var empty Catalog
	if empty.forKey("atlas.nonsense") != nil {
		t.Error("forKey answered for a key outside the contract")
	}
}
