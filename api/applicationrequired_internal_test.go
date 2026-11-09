package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The rule that every process, decision and form belongs to an application
// (ADR-0544), and the adoption of what was
// written before it.

// filingBPMN is a model with nothing to execute but a start and an end, under the
// given process id.
func filingBPMN(id string) string {
	return fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id=%q name="Filing %s" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`, id, id)
}

// filingApp creates an application on srv and returns its id.
func filingApp(t *testing.T, srv *Server, name string) string {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/applications", `{"name":"`+name+`"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application = %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil || app.ID == "" {
		t.Fatalf("create application: %s (%v)", body, err)
	}
	return app.ID
}

// mustServe asks srv and fails the test unless it answers want.
func mustServe(t *testing.T, srv *Server, want int, method, path, body, contentType string) []byte {
	t.Helper()
	code, out := serveInternal(t, srv, method, path, body, contentType)
	if code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, code, out, want)
	}
	return out
}

// TestAServerRequiresAnApplicationByDefault holds the value every server outside
// this package's tests is built with: TestMain lowers the default for the tests
// that are not about filing, and must never lower it for a server.
func TestAServerRequiresAnApplicationByDefault(t *testing.T) {
	if !applicationRequiredInProduction {
		t.Fatal("applicationRequiredInProduction = false, want true")
	}
	if srv := newServerWith(t, withApplicationRequired()); !srv.applicationRequired {
		t.Fatal("withApplicationRequired built a server that does not require an application")
	}
}

// TestNothingNewIsCreatedOutsideAnApplication: every door a process, decision or
// form comes in through refuses one that names no application, and writes nothing.
func TestNothingNewIsCreatedOutsideAnApplication(t *testing.T) {
	srv := newServerWith(t, withApplicationRequired())
	refused := []struct {
		name, method, path, body, ct string
	}{
		{"deploy", http.MethodPost, "/api/v1/deployments", filingBPMN("loose"), "application/xml"},
		{"deploy, explicitly unfiled", http.MethodPost, "/api/v1/deployments?projectId=", filingBPMN("loose"), "application/xml"},
		{"draft", http.MethodPost, "/api/v1/drafts", filingBPMN("loose"), "application/xml"},
		{"form", http.MethodPost, "/api/v1/forms", `{"id":"loose-form","schema":{"type":"default","components":[]}}`, "application/json"},
		{"decision reference", http.MethodPost, "/api/v1/dmnrefs", `{"name":"Loose","modelRef":"loose"}`, "application/json"},
		{"decision draft", http.MethodPost, "/api/v1/dmn-drafts", fmt.Sprintf(`{"xml":%q}`, discountDMN), "application/json"},
		{"decision deployment", http.MethodPost, "/api/v1/decision-deployments", discountDMN, "application/xml"},
	}
	for _, c := range refused {
		code, body := serveInternal(t, srv, c.method, c.path, c.body, c.ct)
		if code != http.StatusBadRequest || !strings.Contains(string(body), "must belong to an application") {
			t.Errorf("%s = %d %s, want 400 naming the application rule", c.name, code, body)
		}
	}
	var listed unassignedResp
	if err := json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodGet, "/api/v1/unassigned", "", ""), &listed); err != nil {
		t.Fatal(err)
	}
	if n := len(listed.Processes) + len(listed.Drafts) + len(listed.Forms) + len(listed.DecisionReferences) +
		len(listed.DecisionDrafts) + len(listed.DecisionDeployments); n != 0 {
		t.Fatalf("a refused request left %d unassigned artifacts behind: %+v", n, listed)
	}

	// The same requests go through once they name an application.
	app := filingApp(t, srv, "Filed")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId="+app, filingBPMN("filed"), "application/xml")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+app, filingBPMN("filed-draft"), "application/xml")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"filed-form","projectId":"`+app+`","schema":{"type":"default","components":[]}}`, "application/json")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmnrefs", `{"name":"Filed","modelRef":"filed","projectId":"`+app+`"}`, "application/json")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmn-drafts", fmt.Sprintf(`{"xml":%q,"projectId":%q}`, discountDMN, app), "application/json")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/decision-deployments?projectId="+app, discountDMN, "application/xml")
}

// TestADeployInheritsItsApplication: a deploy that names no application files where
// the process's draft is, and failing that where its earlier versions are, so an API
// redeploy does not have to repeat what the server already knows.
func TestADeployInheritsItsApplication(t *testing.T) {
	srv := newServerWith(t, withApplicationRequired())
	app := filingApp(t, srv, "Home")

	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+app, filingBPMN("drafted"), "application/xml")
	var dep deployResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments", filingBPMN("drafted"), "application/xml"), &dep)
	if got := srv.deploymentProject(t, dep.Key); got != app {
		t.Fatalf("a deploy of a filed draft's process is filed under %q, want %q", got, app)
	}

	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId="+app, filingBPMN("versioned"), "application/xml")
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments", filingBPMN("versioned"), "application/xml"), &dep)
	if dep.Version != 2 {
		t.Fatalf("redeploy version = %d, want 2", dep.Version)
	}
	if got := srv.deploymentProject(t, dep.Key); got != app {
		t.Fatalf("a redeploy without a draft is filed under %q, want its earlier version's %q", got, app)
	}
}

// deploymentProject is the application the deployed version key is filed under.
func (s *Server) deploymentProject(t *testing.T, key uint64) string {
	t.Helper()
	var (
		pid string
		ok  bool
	)
	s.do(func() {
		if d := s.deployments[key]; d != nil {
			pid, ok = d.ProjectID, true
		}
	})
	if !ok {
		t.Fatalf("no deployment %d", key)
	}
	return pid
}

// TestNothingIsTakenOutOfItsApplication: the move routes no longer accept "no
// application" as a destination, and a form saved without naming one stays where
// it is instead of falling out.
func TestNothingIsTakenOutOfItsApplication(t *testing.T) {
	srv := newServerWith(t, withApplicationRequired())
	app := filingApp(t, srv, "Keeper")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+app, filingBPMN("kept"), "application/xml")
	var dep deployResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId="+app, filingBPMN("kept"), "application/xml"), &dep)
	var ref dmnRefResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Kept","modelRef":"kept","projectId":"`+app+`"}`, "application/json"), &ref)

	for _, c := range []struct{ name, path string }{
		{"draft", "/api/v1/drafts/kept"},
		{"process", fmt.Sprintf("/api/v1/processes/%d", dep.Key)},
		{"decision reference", "/api/v1/dmnrefs/" + ref.ID},
	} {
		code, body := serveInternal(t, srv, http.MethodPatch, c.path, `{"projectId":""}`, "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("moving a %s out of its application = %d %s, want 400", c.name, code, body)
		}
	}
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/drafts?projectId=", filingBPMN("kept"), "application/xml")
	if code != http.StatusBadRequest {
		t.Errorf("saving a filed draft as unfiled = %d %s, want 400", code, body)
	}
	if got := srv.deploymentProject(t, dep.Key); got != app {
		t.Errorf("a refused move changed the process's application to %q", got)
	}

	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"kept-form","projectId":"`+app+`","schema":{"type":"default","components":[]}}`, "application/json")
	var saved formMeta
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"kept-form","schema":{"type":"default","components":[{"type":"text","text":"edited"}]}}`, "application/json"), &saved)
	if saved.ProjectID != app {
		t.Errorf("a form saved without naming an application is filed under %q, want it to stay in %q", saved.ProjectID, app)
	}
}

// TestAnApplicationThatStillHoldsArtifactsIsNotDeleted: a plain delete would leave
// what the application holds belonging to none, so it is refused until the
// application is empty; a purge still takes everything with it.
func TestAnApplicationThatStillHoldsArtifactsIsNotDeleted(t *testing.T) {
	srv := newServerWith(t, withApplicationRequired())
	app := filingApp(t, srv, "Holder")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+app, filingBPMN("held"), "application/xml")

	code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/applications/"+app, "", "")
	if code != http.StatusConflict || !strings.Contains(string(body), "still holds 1") {
		t.Fatalf("deleting an application that holds a draft = %d %s, want 409 counting it", code, body)
	}
	mustServe(t, srv, http.StatusNoContent, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", "")

	empty := filingApp(t, srv, "Empty")
	mustServe(t, srv, http.StatusNoContent, http.MethodDelete, "/api/v1/applications/"+empty, "", "")
}

// TestAdoptFilesWhatBelongsToNoApplication walks the clean-up: what was written
// unfiled — before the rule, or left behind by an application deleted before it —
// is listed, adopted in one request, and then belongs to the application like
// anything created there.
func TestAdoptFilesWhatBelongsToNoApplication(t *testing.T) {
	// The estate is written by a server without the rule, as it was before it.
	srv := newServerWith(t)
	if srv.applicationRequired {
		t.Fatal("this package's tests build servers without the rule; the estate below needs one")
	}
	var dep1, dep2 deployResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments", filingBPMN("legacy"), "application/xml"), &dep1)
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments", filingBPMN("legacy"), "application/xml"), &dep2)
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts", filingBPMN("legacy"), "application/xml")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms", `{"id":"legacy-form","schema":{"type":"default","components":[]}}`, "application/json")
	var ref dmnRefResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmnrefs", `{"name":"Legacy","modelRef":"legacy"}`, "application/json"), &ref)
	var dd dmnDraftResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmn-drafts",
		fmt.Sprintf(`{"refId":%q,"xml":%q}`, ref.ID, discountDMN), "application/json"), &dd)
	var dec deployDecisionResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/decision-deployments", discountDMN, "application/xml"), &dec)
	// A draft left behind by an application deleted before the rule.
	gone := filingApp(t, srv, "Gone")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+gone, filingBPMN("orphan"), "application/xml")
	mustServe(t, srv, http.StatusNoContent, http.MethodDelete, "/api/v1/applications/"+gone, "", "")

	var listed unassignedResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodGet, "/api/v1/unassigned", "", ""), &listed)
	if len(listed.Processes) != 1 || len(listed.Processes[0].Versions) != 2 || len(listed.Drafts) != 2 ||
		len(listed.Forms) != 1 || len(listed.DecisionReferences) != 1 || len(listed.DecisionDrafts) != 1 ||
		len(listed.DecisionDeployments) != 1 {
		t.Fatalf("unassigned = %+v, want the estate written above", listed)
	}

	// The rule is on from here: adopting is what it leaves open.
	srv.applicationRequired = true
	home := filingApp(t, srv, "Home")

	// One artifact that cannot be adopted refuses the whole request.
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/applications/"+home+"/adopt",
		`{"drafts":["legacy","no-such-draft"]}`, "application/json")
	if code != http.StatusConflict || !strings.Contains(string(body), "no-such-draft") {
		t.Fatalf("adopt with an unknown draft = %d %s, want 409 naming it", code, body)
	}
	var still unassignedResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodGet, "/api/v1/unassigned", "", ""), &still)
	if len(still.Drafts) != 2 {
		t.Fatalf("a refused adopt moved something: %+v", still)
	}

	req := fmt.Sprintf(`{"processes":["legacy"],"drafts":["legacy","orphan"],"forms":["legacy-form"],
		"decisionReferences":[%q],"decisionDeployments":[%d]}`, ref.ID, dec.Key)
	var adopted adoptResp
	if err := json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/applications/"+home+"/adopt", req, "application/json"), &adopted); err != nil {
		t.Fatal(err)
	}
	if len(adopted.Processes) != 2 || len(adopted.Drafts) != 2 || len(adopted.Forms) != 1 ||
		len(adopted.DecisionReferences) != 1 || len(adopted.DecisionDrafts) != 1 || len(adopted.DecisionDeployments) != 1 {
		t.Fatalf("adopted = %+v, want both versions, both drafts, the form, the reference with its draft and the decision", adopted)
	}
	for _, key := range []uint64{dep1.Key, dep2.Key} {
		if got := srv.deploymentProject(t, key); got != home {
			t.Errorf("version %d is filed under %q, want %q", key, got, home)
		}
	}
	var after unassignedResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodGet, "/api/v1/unassigned", "", ""), &after)
	if n := len(after.Processes) + len(after.Drafts) + len(after.Forms) + len(after.DecisionReferences) +
		len(after.DecisionDrafts) + len(after.DecisionDeployments); n != 0 {
		t.Fatalf("after the adopt %d artifacts still belong to no application: %+v", n, after)
	}

	// Adopted is filed: adopt is not the move route, and a filed artifact is refused.
	code, body = serveInternal(t, srv, http.MethodPost, "/api/v1/applications/"+filingApp(t, srv, "Other")+"/adopt",
		`{"forms":["legacy-form"]}`, "application/json")
	if code != http.StatusConflict || !strings.Contains(string(body), "already belongs to an application") {
		t.Fatalf("adopting a filed form = %d %s, want 409", code, body)
	}
	// And a new version deployed without naming the application now lands in it.
	var dep3 deployResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments", filingBPMN("legacy"), "application/xml"), &dep3)
	if got := srv.deploymentProject(t, dep3.Key); got != home {
		t.Errorf("a redeploy of an adopted process is filed under %q, want %q", got, home)
	}
}

// TestAdoptRefusesTheSystemApplication: the platform-managed application is not
// a destination (ADR-0122).
func TestAdoptRefusesTheSystemApplication(t *testing.T) {
	srv := newServerWith(t)
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts", filingBPMN("loose"), "application/xml")
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/applications/"+systemProjectID+"/adopt",
		`{"drafts":["loose"]}`, "application/json")
	if code != http.StatusForbidden {
		t.Fatalf("adopt into the system application = %d %s, want 403", code, body)
	}
}

// TestAdoptRefusesWhatItCannotFile walks every way an adoption is refused: a body
// that is not one, one that names nothing, an application that does not exist, and,
// for each kind, an id that names nothing and an artifact that already belongs to an
// application. Every refusal is the whole request's: nothing moves.
func TestAdoptRefusesWhatItCannotFile(t *testing.T) {
	srv := newServerWith(t)
	home := filingApp(t, srv, "Home")
	other := filingApp(t, srv, "Other")
	adopt := "/api/v1/applications/" + home + "/adopt"

	for _, c := range []struct{ name, path, body string }{
		{"a body that is not JSON", adopt, `{`},
		{"a request that names nothing", adopt, `{}`},
		{"an application that does not exist", "/api/v1/applications/no-such-app/adopt", `{"drafts":["x"]}`},
	} {
		if code, body := serveInternal(t, srv, http.MethodPost, c.path, c.body, "application/json"); code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", c.name, code, body)
		}
	}

	// One of each kind filed under Other, and a process whose versions are split
	// between Other and no application.
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts?projectId="+other, filingBPMN("filed"), "application/xml")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"filed-form","projectId":"`+other+`","schema":{"type":"default","components":[]}}`, "application/json")
	var ref dmnRefResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Filed","modelRef":"filed","projectId":"`+other+`"}`, "application/json"), &ref)
	var dd dmnDraftResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmn-drafts",
		fmt.Sprintf(`{"xml":%q,"projectId":%q}`, discountDMN, other), "application/json"), &dd)
	var dec deployDecisionResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/decision-deployments?projectId="+other,
		discountDMN, "application/xml"), &dec)
	var filedVersion deployResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId="+other,
		filingBPMN("split"), "application/xml"), &filedVersion)
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId=", filingBPMN("split"), "application/xml")

	var listed unassignedResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodGet, "/api/v1/unassigned", "", ""), &listed)
	if len(listed.Processes) != 1 || listed.Processes[0].FiledVersions != 1 {
		t.Fatalf("unassigned processes = %+v, want the split process with one version filed elsewhere", listed.Processes)
	}

	filed := fmt.Sprintf(`{"processes":["split"],"drafts":["filed"],"forms":["filed-form"],"decisionReferences":[%q],
		"decisionDrafts":[%q],"decisionDeployments":[%d]}`, ref.ID, dd.ID, dec.Key)
	missing := `{"processes":["none"],"drafts":["none"],"forms":["none"],"decisionReferences":["none"],
		"decisionDrafts":["none"],"decisionDeployments":[999999]}`
	for _, c := range []struct{ name, body, want string }{
		{"artifacts filed elsewhere", filed, "already belongs to an application"},
		{"ids that name nothing", missing, "no "},
	} {
		code, body := serveInternal(t, srv, http.MethodPost, adopt, c.body, "application/json")
		var refused struct {
			Problems []adoptProblem `json:"problems"`
		}
		_ = json.Unmarshal(body, &refused)
		if code != http.StatusConflict || len(refused.Problems) != 6 {
			t.Fatalf("%s = %d %s, want 409 naming all six", c.name, code, body)
		}
		for _, p := range refused.Problems {
			if !strings.Contains(p.Problem, c.want) {
				t.Errorf("%s: %s %s is refused for %q, want %q", c.name, p.Kind, p.ID, p.Problem, c.want)
			}
		}
	}
	if got := srv.deploymentProject(t, filedVersion.Key); got != other {
		t.Errorf("a refused adoption moved the filed version to %q", got)
	}
}

// TestAdoptTakesADecisionDraftOnItsOwn: a decision draft that belongs to no decision
// reference is adopted by its own id.
func TestAdoptTakesADecisionDraftOnItsOwn(t *testing.T) {
	srv := newServerWith(t)
	var dd dmnDraftResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/dmn-drafts",
		fmt.Sprintf(`{"xml":%q}`, discountDMN), "application/json"), &dd)
	home := filingApp(t, srv, "Home")
	var adopted adoptResp
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/applications/"+home+"/adopt",
		`{"decisionDrafts":["`+dd.ID+`"]}`, "application/json"), &adopted)
	if len(adopted.DecisionDrafts) != 1 || adopted.DecisionDrafts[0] != dd.ID {
		t.Fatalf("adopted = %+v, want the decision draft", adopted)
	}
}

// TestADeployDoesNotInheritAnApplicationThatIsGone: the application a process's
// earlier version names is inherited only while it exists; one deleted before the
// rule leaves nothing to inherit, and the deploy has to name one.
func TestADeployDoesNotInheritAnApplicationThatIsGone(t *testing.T) {
	srv := newServerWith(t)
	gone := filingApp(t, srv, "Gone")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/deployments?projectId="+gone, filingBPMN("left"), "application/xml")
	mustServe(t, srv, http.StatusNoContent, http.MethodDelete, "/api/v1/applications/"+gone, "", "")
	srv.applicationRequired = true
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", filingBPMN("left"), "application/xml"); code != http.StatusBadRequest {
		t.Fatalf("redeploy of a process whose application is gone = %d %s, want 400", code, body)
	}
}

// TestAnImportAndARenamedFormStayInsideAnApplication: a MIM import names the
// application its drafts go to; a form renamed while it is filed keeps its
// application; and a form is not filed under an application that does not exist.
func TestAnImportAndARenamedFormStayInsideAnApplication(t *testing.T) {
	srv := newServerWith(t, withApplicationRequired())
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/imports/mim", mimImportHTTPPathsXOML, "application/xml"); code != http.StatusBadRequest ||
		!strings.Contains(string(body), "must belong to an application") {
		t.Fatalf("a MIM import without an application = %d %s, want 400", code, body)
	}
	app := filingApp(t, srv, "Forms")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"before","projectId":"`+app+`","schema":{"type":"default","components":[]}}`, "application/json")
	var renamed formMeta
	_ = json.Unmarshal(mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms",
		`{"id":"after","from":"before","schema":{"type":"default","components":[]}}`, "application/json"), &renamed)
	if renamed.ProjectID != app {
		t.Errorf("a renamed form is filed under %q, want it to stay in %q", renamed.ProjectID, app)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/forms",
		`{"id":"after","projectId":"no-such-app","schema":{"type":"default","components":[]}}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("a form moved under an application that does not exist = %d %s, want 400", code, body)
	}
}

// TestTheCleanUpReportsAStoreItCannotRead: listing and adopting read every store an
// artifact can sit in, and one that cannot answer is a 500 — never read as "nothing
// unassigned there", which would hide part of the clean-up — and nothing is moved.
// So does the check that keeps an application from being deleted while it holds
// anything.
func TestTheCleanUpReportsAStoreItCannotRead(t *testing.T) {
	for _, store := range []string{"projects", "drafts", "forms", "dmnrefs", "dmn-drafts", "decisions"} {
		t.Run(store, func(t *testing.T) {
			dir := t.TempDir()
			st := bootDecisionStack(t, dir)
			defer st.shutdown()
			srv := st.srv
			app := filingApp(t, srv, "Holder")
			srv.applicationRequired = true
			breakStore(t, dir, store)

			if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/unassigned", "", ""); code != http.StatusInternalServerError {
				t.Errorf("unassigned = %d %s, want 500", code, body)
			}
			code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/applications/"+app+"/adopt",
				`{"drafts":["d"],"forms":["f"],"decisionReferences":["r"],"decisionDrafts":["dd"],"decisionDeployments":[1]}`,
				"application/json")
			if code != http.StatusInternalServerError && !(store == "projects" && code == http.StatusBadRequest) {
				t.Errorf("adopt = %d %s, want 500", code, body)
			}
			if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/applications/"+app, "", ""); code != http.StatusInternalServerError {
				t.Errorf("delete = %d %s, want 500", code, body)
			}
		})
	}
}

// TestAdoptWritesOnlyWhatIsStillUnfiled drives the write turn of an adoption the
// way a concurrent change would leave it: an artifact deleted since the plan is
// skipped, one filed since is left where it is, and a store that refuses the write
// is reported — with what was written before it kept.
func TestAdoptWritesOnlyWhatIsStillUnfiled(t *testing.T) {
	dir := t.TempDir()
	st := bootDecisionStack(t, dir)
	defer st.shutdown()
	srv := st.srv
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/drafts", filingBPMN("planned"), "application/xml")
	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms", `{"id":"planned-form","schema":{"type":"default","components":[]}}`, "application/json")
	home := filingApp(t, srv, "Home")
	plan := adoptPlan{
		drafts:    []draft{{ProcessID: "planned"}, {ProcessID: "deleted-since"}},
		forms:     []form{{ID: "planned-form"}},
		decisions: []persistedDecision{{Key: 424242}},
	}

	var (
		resp adoptResp
		err  error
	)
	srv.do(func() { resp, err = srv.adoptOnLoop(plan, home) })
	if err != nil || len(resp.Drafts) != 1 || len(resp.Forms) != 1 || len(resp.DecisionDeployments) != 0 {
		t.Fatalf("first write = %+v, %v; want the draft and the form, and nothing for what is gone", resp, err)
	}
	// The same plan again: everything it names is filed now, so nothing moves.
	other := filingApp(t, srv, "Other")
	srv.do(func() { resp, err = srv.adoptOnLoop(plan, other) })
	if err != nil || len(resp.Drafts)+len(resp.Forms) != 0 {
		t.Fatalf("second write = %+v, %v; want nothing taken from Home", resp, err)
	}

	mustServe(t, srv, http.StatusOK, http.MethodPost, "/api/v1/forms", `{"id":"late-form","schema":{"type":"default","components":[]}}`, "application/json")
	breakStore(t, dir, "drafts")
	plan = adoptPlan{drafts: []draft{{ProcessID: "planned"}}, forms: []form{{ID: "late-form"}}}
	srv.do(func() { resp, err = srv.adoptOnLoop(plan, home) })
	if err == nil {
		t.Fatalf("a write to a broken store = %+v, want its error", resp)
	}
}
