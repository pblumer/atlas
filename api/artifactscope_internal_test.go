package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestUngroupedRole exercises every branch of the ungrouped access rule
// (ADR-0071): auth off is open, admins are owner-equivalent, an ownerless legacy
// artifact stays open, and otherwise only the owner has access.
func TestUngroupedRole(t *testing.T) {
	mk := func(authEnabled bool, p *httpapi.Principal) (*Server, *http.Request) {
		s := &Server{authEnabled: authEnabled}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if p != nil {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		}
		return s, r
	}
	owner := &httpapi.Principal{UserID: "usr_owner", Roles: []string{RoleUser}}
	stranger := &httpapi.Principal{UserID: "usr_stranger", Roles: []string{RoleUser}}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}}

	cases := []struct {
		name        string
		authEnabled bool
		pr          *httpapi.Principal
		ownerID     string
		want        string
	}{
		{"auth off is open", false, nil, "usr_owner", ScopeRoleOwner},
		{"nil principal under auth", true, nil, "usr_owner", ""},
		{"admin is owner", true, admin, "usr_owner", ScopeRoleOwner},
		{"owner match", true, owner, "usr_owner", ScopeRoleOwner},
		{"stranger gets nothing", true, stranger, "usr_owner", ""},
		{"legacy ownerless is open", true, stranger, "", ScopeRoleOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, r := mk(tc.authEnabled, tc.pr)
			if got := s.ungroupedRole(r, tc.ownerID); got != tc.want {
				t.Fatalf("ungroupedRole = %q, want %q", got, tc.want)
			}
		})
	}
}

// scopeBPMN is a minimal draft with the given process id, so handleSaveDraft can
// derive that id from the body.
func scopeBPMN(id string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="` + id + `"><bpmn:startEvent id="s"/></bpmn:process>
</bpmn:definitions>`
}

// TestArtifactScopeStoreErrors drives the 500 branches the scope-inheritance
// wiring adds to the artifact handlers: a broken projects store during list
// filtering, a project record that errors on read (governing an artifact), and a
// broken artifact store on the load-before-authorize step. Auth is off, so the
// authorization itself is a no-op — these exercise only the store-error paths.
func TestArtifactScopeStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// A) projectsByID error → every scope-filtered list handler 500s. A listing that
	// cannot work out who may see what must not answer with the entries it happened
	// to read: an empty list and a hidden one look the same to the reader.
	realProjects := srv.projects
	srv.projects = brokenStore(newProjectStore(filepath.Join(t.TempDir(), "gone")))
	for _, p := range []string{"/api/v1/drafts", "/api/v1/dmnrefs", "/api/v1/forms", "/api/v1/decisions",
		"/api/v1/playground/scenarios"} {
		if got := do(http.MethodGet, p, ""); got != http.StatusInternalServerError {
			t.Fatalf("GET %s with broken projects = %d, want 500", p, got)
		}
	}
	srv.projects = realProjects

	// The artifact store itself failing is the same answer: a scenario listing is
	// read by somebody about to run one, and "there are none" is the wrong thing to
	// tell them when the truth is "they could not be read".
	realScenarios := srv.playgroundScenarios
	srv.playgroundScenarios = brokenStore(newPlaygroundScenarioStore(filepath.Join(t.TempDir(), "gone")))
	if got := do(http.MethodGet, "/api/v1/playground/scenarios", ""); got != http.StatusInternalServerError {
		t.Fatalf("GET scenarios with a broken store = %d, want 500", got)
	}
	srv.playgroundScenarios = realScenarios

	// B) A project whose record is a directory errors on read; an artifact filed
	// under it makes every authorize step 500.
	if err := os.MkdirAll(srv.projects.FileFor("pdir"), 0o755); err != nil {
		t.Fatalf("mk pdir: %v", err)
	}
	must := func(e error) {
		if e != nil {
			t.Fatalf("seed: %v", e)
		}
	}
	must(srv.drafts.Save(draft{ProcessID: "d1", ProjectID: "pdir", XML: "<x/>", SavedAt: 1}))
	must(srv.dmnrefs.Save(dmnRef{ID: "r1", Name: "R", ModelRef: "m", ProjectID: "pdir", CreatedAt: 1}))
	must(srv.forms.Save(form{ID: "f1", Name: "F", ProjectID: "pdir", Schema: "{}", SavedAt: 1}))
	must(srv.drafts.Save(draft{ProcessID: "d2", XML: "<x/>", SavedAt: 1})) // ungrouped, for a move target test
	must(srv.playgroundScenarios.Save(playgroundScenario{
		ID: "s1", Name: "S", ProcessID: "d1", ProjectID: "pdir", SavedAt: 1,
		Spec: `{"open":{},"run":{}}`,
	}))

	cases := []struct {
		name, method, path, body string
	}{
		{"draft xml (authorizeArtifact)", "GET", "/api/v1/drafts/d1/xml", ""},
		{"dmnref graph (authorizeArtifact)", "GET", "/api/v1/dmnrefs/r1/graph", ""},
		{"dmnref validate (authorizeArtifact)", "POST", "/api/v1/dmnrefs/r1/validate", ""},
		{"delete draft (authorizeArtifact)", "DELETE", "/api/v1/drafts/d1", ""},
		{"delete dmnref (authorizeArtifact)", "DELETE", "/api/v1/dmnrefs/r1", ""},
		{"delete form (authorizeArtifact)", "DELETE", "/api/v1/forms/f1", ""},
		{"overwrite form (authorizeArtifact source)", "POST", "/api/v1/forms", `{"id":"f1","schema":{"a":1}}`},
		{"overwrite draft (authorizeArtifact source)", "POST", "/api/v1/drafts", scopeBPMN("d1")},
		{"move draft (authorizeArtifact source)", "PATCH", "/api/v1/drafts/d1", `{"projectId":""}`},
		{"update dmnref (authorizeArtifact source)", "PATCH", "/api/v1/dmnrefs/r1", `{"name":"X"}`},
		{"create dmnref (authorizeTargetProject)", "POST", "/api/v1/dmnrefs", `{"name":"N","modelRef":"m","projectId":"pdir"}`},
		{"create draft (authorizeTargetProject)", "POST", "/api/v1/drafts?projectId=pdir", scopeBPMN("newp")},
		{"move draft into target (authorizeTargetProject)", "PATCH", "/api/v1/drafts/d2", `{"projectId":"pdir"}`},
		// Opening a Playground sandbox on a draft reads that draft, so it goes through
		// the same authorization — and fails the same way when the project cannot be
		// read (ADR-draft-modeler-playground).
		{"playground on a draft (authorizeArtifact)", "POST", "/api/v1/playground/sessions", `{"source":"draft","ref":"d1"}`},
		// A saved Playground scenario is a design-time artifact like the rest, so it
		// inherits its project's scope on every door into it: reading one, keeping a
		// baseline on it, overwriting it, deleting it, and filing a new one into a
		// project (ADR-draft-modeler-playground, ADR-0071).
		{"read scenario (authorizeArtifact)", "GET", "/api/v1/playground/scenarios/s1", ""},
		{"scenario baseline (authorizeArtifact)", "PUT", "/api/v1/playground/scenarios/s1/baseline", `{"cases":1}`},
		{"delete scenario (authorizeArtifact)", "DELETE", "/api/v1/playground/scenarios/s1", ""},
		{"overwrite scenario (authorizeArtifact source)", "POST", "/api/v1/playground/scenarios",
			`{"id":"s1","processId":"d1","spec":{"open":{},"run":{}}}`},
		{"create scenario (authorizeTargetProject)", "POST", "/api/v1/playground/scenarios",
			`{"id":"s2","processId":"d1","projectId":"pdir","spec":{"open":{},"run":{}}}`},
	}
	for _, tc := range cases {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Fatalf("%s = %d, want 500", tc.name, got)
		}
	}

	// C) A broken artifact store errors on the load-before-authorize step.
	must(os.MkdirAll(srv.drafts.FileFor("ddir"), 0o755))
	must(os.MkdirAll(srv.dmnrefs.FileFor("rdir"), 0o755))
	must(os.MkdirAll(srv.forms.FileFor("fdir"), 0o755))
	must(os.MkdirAll(srv.playgroundScenarios.FileFor("sdir"), 0o755))
	storeErr := []struct {
		name, method, path, body string
	}{
		{"delete draft get error", "DELETE", "/api/v1/drafts/ddir", ""},
		{"save draft existing-read error", "POST", "/api/v1/drafts", scopeBPMN("ddir")},
		{"move draft get error", "PATCH", "/api/v1/drafts/ddir", `{"projectId":""}`},
		{"delete dmnref get error", "DELETE", "/api/v1/dmnrefs/rdir", ""},
		{"update dmnref get error", "PATCH", "/api/v1/dmnrefs/rdir", `{"name":"x"}`},
		{"delete form get error", "DELETE", "/api/v1/forms/fdir", ""},
		{"save form existing-read error", "POST", "/api/v1/forms", `{"id":"fdir","schema":{}}`},
		{"playground draft read error", "POST", "/api/v1/playground/sessions", `{"source":"draft","ref":"ddir"}`},
		{"read scenario get error", "GET", "/api/v1/playground/scenarios/sdir", ""},
		{"delete scenario get error", "DELETE", "/api/v1/playground/scenarios/sdir", ""},
		{"save scenario existing-read error", "POST", "/api/v1/playground/scenarios",
			`{"id":"sdir","processId":"d1","spec":{"open":{},"run":{}}}`},
	}
	for _, tc := range storeErr {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Fatalf("%s = %d, want 500", tc.name, got)
		}
	}
}
