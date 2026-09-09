package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// A small MIM/FIM XOML workflow: an approval branch and a resource write-back.
const mimXOML = `<SequentialWorkflow>
  <IfElseActivity Description="Approve?">
    <IfElseBranchActivity Description="Needs approval">
      <Condition>//WorkflowData["Dept"] = "Finance"</Condition>
      <ApprovalActivity Description="Manager approval"/>
    </IfElseBranchActivity>
    <IfElseBranchActivity Description="Auto">
      <NotificationActivity Description="Notify"/>
    </IfElseBranchActivity>
  </IfElseActivity>
  <UpdateResourceActivity Description="Write back"/>
</SequentialWorkflow>`

// TestImportMIMCreatesDraft drives the UI-facing import endpoint: an XOML upload
// must convert, land as a reopenable draft, and return a per-node report.
func TestImportMIMCreatesDraft(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	var res struct {
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		Report    struct {
			Native       int `json:"native"`
			Preserved    int `json:"preserved"`
			ManualReview int `json:"manualReview"`
			Notes        []struct {
				NodeID string `json:"nodeId"`
				Status string `json:"status"`
			} `json:"notes"`
		} `json:"report"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if res.ProcessID != "Onboarding" {
		t.Fatalf("processId = %q, want Onboarding", res.ProcessID)
	}
	if len(res.Report.Notes) == 0 {
		t.Fatal("expected a non-empty conversion report")
	}
	if res.Report.Native == 0 || res.Report.ManualReview == 0 {
		t.Errorf("report counts look wrong: %+v", res.Report)
	}

	// The import must have created a reopenable draft holding the converted BPMN.
	code, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/"+res.ProcessID+"/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("draft xml status=%d body=%s", code, xml)
	}
	if !strings.Contains(string(xml), "<userTask") || !strings.Contains(string(xml), "atlas:mimSource") {
		t.Errorf("draft BPMN missing expected content:\n%s", xml)
	}
}

func TestImportMIMRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim", "", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("empty body status=%d, want 400", code)
	}
}

func TestImportMIMRejectsUnknownProject(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?projectId=does-not-exist", mimXOML, "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("unknown project status=%d, want 400", code)
	}
}

func TestImportMIMRejectsNonXOML(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim", "this is not xml <<<", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("non-XOML status=%d body=%s, want 400", code, body)
	}
}

// draftPlacement lists the drafts a client can see, as process id → application id.
// It is what the Modeler's own artifact list is built from, so a test asking "where
// did the import land" and a person asking it get the same answer.
func draftPlacement(t *testing.T, c *http.Client, ts *httptest.Server) map[string]string {
	t.Helper()
	code, body := cReq(t, c, ts, http.MethodGet, "/api/v1/drafts", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts status=%d body=%s", code, body)
	}
	var list []struct {
		ProcessID string `json:"processId"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode drafts: %v (%s)", err, body)
	}
	out := map[string]string{}
	for _, d := range list {
		out[d.ProcessID] = d.ProjectID
	}
	return out
}

// newApplication creates an application and returns its id.
func newApplication(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"`+name+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	return app.ID
}

// TestImportMIMFilesTheDraftIntoTheNamedApplication is the placement contract the
// Modeler relies on: an import started inside an application says so with
// ?projectId=, and the draft is in that application afterwards — not somewhere the
// author would have to go looking for it.
func TestImportMIMFilesTheDraftIntoTheNamedApplication(t *testing.T) {
	ts := newTestServer(t)
	app := newApplication(t, ts, "Sven's Stuff")

	code, body := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding&projectId="+app, mimXOML, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	if got := draftPlacement(t, http.DefaultClient, ts)["Onboarding"]; got != app {
		t.Fatalf("draft filed under %q, want %q", got, app)
	}
}

// TestImportMIMLeavesAnExistingDraftInItsApplication covers the re-import: a
// corrected export of a workflow already held is imported again, and the request
// names no application because it was started where no application is known. That
// must not be read as "file it under none" — the draft is somebody's, in a folder
// they put it in, and a re-import is not a request to move it out of there.
func TestImportMIMLeavesAnExistingDraftInItsApplication(t *testing.T) {
	ts := newTestServer(t)
	app := newApplication(t, ts, "Sven's Stuff")

	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding&projectId="+app, mimXOML, "application/xml"); code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml"); code != http.StatusOK {
		t.Fatalf("re-import status=%d body=%s", code, b)
	}
	if got := draftPlacement(t, http.DefaultClient, ts)["Onboarding"]; got != app {
		t.Fatalf("re-import moved the draft to %q, want it left in %q", got, app)
	}
}

// TestImportMIMRefusesToReplaceADraftUnasked is ADR-0222 on this endpoint: picking a
// file must never silently replace the draft somebody already had. ?from= says this
// import is a new draft, so the id it lands on has to be free; the deliberate
// replacement omits it, which is what the "Replace it?" answer sends.
func TestImportMIMRefusesToReplaceADraftUnasked(t *testing.T) {
	ts := newTestServer(t)
	app := newApplication(t, ts, "Sven's Stuff")
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding&projectId="+app, mimXOML, "application/xml"); code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", code, b)
	}

	// A second import that says it is a new draft, and would file it elsewhere.
	code, body := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding&from=&projectId=", mimXOML, "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("import onto a taken id status=%d body=%s, want 409", code, body)
	}
	// A refused import writes nothing at all — including the move it asked for.
	if got := draftPlacement(t, http.DefaultClient, ts)["Onboarding"]; got != app {
		t.Fatalf("refused import still moved the draft to %q, want %q", got, app)
	}

	// Confirming the replacement omits ?from=, and then it lands.
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml"); code != http.StatusOK {
		t.Fatalf("confirmed replacement status=%d body=%s", code, b)
	}
}

// TestImportMIMScopesTheTargetApplication: filing an artifact into an application is
// a write to that application, so it needs the same editor role every other write to
// it needs (ADR-0071). Without this the import was the one door into a private
// application that asked nothing.
func TestImportMIMScopesTheTargetApplication(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["modeler","operator","user"]}`)
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["modeler","operator","user"]}`)
	alice, bob := newClient(t), newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK || login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("user login")
	}
	_, pbody := cReq(t, alice, ts, "POST", "/api/v1/applications", `{"name":"Secret"}`)
	app := decodeProject(t, pbody).ID

	// A non-member gets 404, the same answer every other door gives: the application
	// is not theirs to see, so its existence is not confirmed either.
	if code, b := cReq(t, bob, ts, "POST", "/api/v1/imports/mim?name=Sneak&projectId="+app, mimXOML); code != http.StatusNotFound {
		t.Fatalf("bob import into alice's application = %d %s, want 404", code, b)
	}
	if _, ok := draftPlacement(t, alice, ts)["Sneak"]; ok {
		t.Fatal("a refused import must not have created a draft")
	}
	// The owner's own import is unaffected.
	if code, b := cReq(t, alice, ts, "POST", "/api/v1/imports/mim?name=Onboarding&projectId="+app, mimXOML); code != http.StatusOK {
		t.Fatalf("alice import into her own application = %d %s", code, b)
	}
}

// TestImportMIMStampsTheImporterAsOwner: an ungrouped artifact is its creator's
// personal space (ADR-0071), and that rests entirely on the creator being recorded.
// An import that left OwnerID empty produced an artifact the whole server could
// read and write, which is the "legacy, no recorded creator" case — true of drafts
// that predate ownership, and not true of one created a second ago.
func TestImportMIMStampsTheImporterAsOwner(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["modeler","operator","user"]}`)
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["modeler","operator","user"]}`)
	alice, bob := newClient(t), newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK || login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("user login")
	}

	if code, b := cReq(t, alice, ts, "POST", "/api/v1/imports/mim?name=Onboarding", mimXOML); code != http.StatusOK {
		t.Fatalf("alice import = %d %s", code, b)
	}
	if _, ok := draftPlacement(t, alice, ts)["Onboarding"]; !ok {
		t.Fatal("alice should see the draft she imported")
	}
	if _, ok := draftPlacement(t, bob, ts)["Onboarding"]; ok {
		t.Fatal("bob must not see alice's personal ungrouped import")
	}
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/drafts/Onboarding/xml", ""); code != http.StatusNotFound {
		t.Fatalf("bob read of alice's import = %d, want 404", code)
	}
}

// TestImportMIMRefusesToOverwriteASystemDraft: the protected system application is
// platform-managed (ADR-0122) and no design-time write may reach its content. The
// import checked only where it was filing *to*, so a workflow whose process id
// happened to match a platform process replaced it — and, naming no application,
// carried it out of the protected one on the way.
func TestImportMIMRefusesToOverwriteASystemDraft(t *testing.T) {
	ts := newTestServerWith(t, api.WithSystemProcesses())
	before := draftPlacement(t, http.DefaultClient, ts)
	if before["proc_benutzer_review"] != "system" {
		t.Fatalf("expected the platform draft in the system application, got %q", before["proc_benutzer_review"])
	}

	code, body := doReq(t, ts, http.MethodPost,
		"/api/v1/imports/mim?name=proc_benutzer_review", mimXOML, "application/xml")
	if code != http.StatusForbidden {
		t.Fatalf("import over a system draft status=%d body=%s, want 403", code, body)
	}
	if got := draftPlacement(t, http.DefaultClient, ts)["proc_benutzer_review"]; got != "system" {
		t.Fatalf("the platform draft moved to %q", got)
	}
}
