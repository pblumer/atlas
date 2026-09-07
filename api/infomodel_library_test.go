package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A library information model belongs to no process application, and every
// application resolves against it (ADR-draft-shared-information-models). The rule
// that governs one is therefore not an application's sharing scope but the area's own
// role, which is wired here rather than in the infomodel package — so it is pinned
// here, through the routes a modeler actually calls.

const sharedCustomer = `{"classes":[{"id":"c1","name":"Customer","stereotype":"businessObject",
  "identity":["nr"],
  "attributes":[{"name":"nr","type":"string","multiplicity":"1"},
                {"name":"name","type":"string","multiplicity":"1"}]}]}`

// TestLibraryModelIsAuthoredWithoutAnApplication walks the whole of it: created with
// no application, filled, listed as belonging to none, and resolved against by an
// application that never mentioned it.
func TestLibraryModelIsAuthoredWithoutAnApplication(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		`{"name":"Shared vocabulary"}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create library model: status=%d body=%s", code, body)
	}
	var model struct {
		ID            string `json:"id"`
		ApplicationID string `json:"applicationId"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if model.ApplicationID != "" {
		t.Errorf("applicationId = %q, want none: a library model owns no application", model.ApplicationID)
	}
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+model.ID,
		sharedCustomer, "application/json"); code != http.StatusOK {
		t.Fatalf("save classes: status=%d body=%s", code, b)
	}

	// An application that has never heard of this model resolves Customer against it,
	// which is the whole point: the attributes are typed once and read everywhere.
	app := newApplicationWithOrderClass(t, ts)
	code, body = doReq(t, ts, http.MethodPost,
		"/api/v1/validate?applicationId="+app, libraryBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate: status=%d body=%s", code, body)
	}
	var resp validateResult
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	// An unresolved type is a Problems-panel finding (ADR-0230 §3), so the proof that
	// it resolved is the absence of one about Customer.
	for _, p := range resp.Problems {
		if strings.Contains(p.Message, "Customer") {
			t.Errorf("Customer did not resolve against the library model: %s", p.Message)
		}
	}
}

// libraryBPMN names a type only the library model defines.
const libraryBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="sales" isExecutable="true">
    <dataObject id="DO_c" name="customer" itemSubjectRef="Customer"/>
    <dataObjectReference id="Ref_c" name="customer" dataObjectRef="DO_c"/>
    <startEvent id="Start_1"/>
    <task id="Task_1" name="Greet"/>
    <endEvent id="End_1"/>
    <sequenceFlow id="f1" sourceRef="Start_1" targetRef="Task_1"/>
    <sequenceFlow id="f2" sourceRef="Task_1" targetRef="End_1"/>
  </process>
</definitions>`

// A name may be defined by a library model or by an application's own, not both. The
// refusal names the class and the model on the other side, because "conflict" alone
// is not something a modeler can act on.
func TestANameCannotBeDefinedOnBothSidesOfTheLibrary(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		`{"name":"Shared vocabulary"}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create library model: status=%d body=%s", code, body)
	}
	var library struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &library); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+library.ID,
		sharedCustomer, "application/json"); code != http.StatusOK {
		t.Fatalf("save library classes: status=%d body=%s", code, b)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Sales"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Sales data"}`, app.ID), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create application model: status=%d body=%s", code, body)
	}
	var own struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &own); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+own.ID,
		sharedCustomer, "application/json")
	if code != http.StatusConflict {
		t.Fatalf("redefining a library class: status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "Customer") || !strings.Contains(string(body), "Shared vocabulary") {
		t.Errorf("the refusal names neither the class nor the model that defines it: %s", body)
	}
}

// With authentication on, the library has no application scope to inherit, so the
// rule is the area's own role: a modeler may author a library model, and only an
// administrator may delete one — deleting reaches diagrams its author never saw,
// where editing shows up in the Problems panel of everything it touches
// (ADR-draft-shared-information-models).
func TestLibraryModelRolesUnderAuth(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"mira","password":"password1","roles":["modeler","operator","user"]}`)
	mira := newClient(t)
	if login(t, mira, ts, "mira", "password1") != http.StatusOK {
		t.Fatal("mira login")
	}

	// A modeler creates and fills one: the shared vocabulary is not an administrator's
	// document, or a team could not maintain the thing it depends on.
	code, body := cReq(t, mira, ts, "POST", "/api/v1/infomodel/models", `{"name":"Shared vocabulary"}`)
	if code != http.StatusCreated {
		t.Fatalf("modeler creating a library model: status=%d body=%s", code, body)
	}
	id := idOf(t, body)
	if code, b := cReq(t, mira, ts, "PUT", "/api/v1/infomodel/models/"+id, sharedCustomer); code != http.StatusOK {
		t.Fatalf("modeler editing a library model: status=%d body=%s", code, b)
	}

	// Deleting is where it stops.
	code, body = cReq(t, mira, ts, "DELETE", "/api/v1/infomodel/models/"+id, "")
	if code != http.StatusForbidden {
		t.Fatalf("modeler deleting a library model: status=%d body=%s, want 403", code, body)
	}
	if !strings.Contains(string(body), "administrator") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/infomodel/models/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("administrator deleting a library model: status=%d body=%s", code, b)
	}
}
