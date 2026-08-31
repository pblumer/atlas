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
