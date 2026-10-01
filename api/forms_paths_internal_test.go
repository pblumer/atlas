package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// formsPathsSeed files form records directly, on the loop.
func formsPathsSeed(t *testing.T, srv *Server, recs ...form) {
	t.Helper()
	var err error
	srv.do(func() {
		for _, f := range recs {
			if err = srv.forms.Save(f); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed forms: %v", err)
	}
}

// formsPathsHas reports whether a form id is stored.
func formsPathsHas(t *testing.T, srv *Server, id string) bool {
	t.Helper()
	var ok bool
	srv.do(func() { _, ok, _ = srv.forms.Get(id) })
	return ok
}

// formsPathsSaveAs posts a save straight to the handler as the given principal.
func formsPathsSaveAs(srv *Server, p *httpapi.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/forms", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	srv.handleSaveForm(rec, req)
	return rec
}

// TestFormsRefusesABodyThatCannotBeRead: a form whose schema broke off in transit
// would be stored half-written, and the task bound to it would render a form with
// fields missing. It is refused and nothing is filed.
func TestFormsRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/forms", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Fatalf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if entries, err := os.ReadDir(srv.forms.Dir()); err != nil || len(entries) != 0 {
		t.Errorf("form store after a refused save = %v (%v), want empty", entries, err)
	}
}

// TestFormsRenameStopsOnAnOriginItCannotRead: a rename deletes the form it came
// from. When that form cannot be read, nobody can say whether it may be deleted or
// whose it was, so the save stops before writing the new id.
func TestFormsRenameStopsOnAnOriginItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	corrupt(t, srv.forms.Dir(), "old")
	rec := formsPathsSaveAs(srv, nil, `{"id":"new","from":"old","schema":{"components":[]}}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "read form") {
		t.Fatalf("unreadable origin: %d (%s), want 500 'read form'", rec.Code, rec.Body)
	}
	if formsPathsHas(t, srv, "new") {
		t.Error("the renamed form was written although its origin could not be read")
	}
}

// TestFormsRefusesARenameOrAFilingTheCallerMayNotMake: renaming somebody else's
// personal form would delete it, and filing a form into a project the caller
// cannot see would put it where they have no say. Both are refused the way a
// listing hides them — as not there — and nothing is written or removed.
func TestFormsRefusesARenameOrAFilingTheCallerMayNotMake(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	formsPathsSeed(t, srv, form{ID: "anns-form", OwnerID: "usr_ann", Schema: `{"components":[]}`})
	var err error
	srv.do(func() {
		err = srv.projects.Save(project{ID: "anns-project", Name: "Ann's", OwnerID: "usr_ann", Visibility: VisibilityPrivate})
	})
	if err != nil {
		t.Fatal(err)
	}
	bob := &httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleModeler}}

	for name, body := range map[string]string{
		"rename": `{"id":"bobs-form","from":"anns-form","schema":{"components":[]}}`,
		"filing": `{"id":"bobs-form","projectId":"anns-project","schema":{"components":[]}}`,
	} {
		rec := formsPathsSaveAs(srv, bob, body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d (%s), want 404", name, rec.Code, rec.Body)
		}
	}
	if !formsPathsHas(t, srv, "anns-form") || formsPathsHas(t, srv, "bobs-form") {
		t.Error("a refused save still moved or created a form")
	}
}

// TestFormsListNarrowsToOneProject: ?projectId= is how the Modeler lists one
// application's forms, and another application's form in that list would be offered
// as a binding the deploy then files under the wrong application.
func TestFormsListNarrowsToOneProject(t *testing.T) {
	srv := newServerForErrors(t)
	formsPathsSeed(t, srv,
		form{ID: "mine", ProjectID: "p-a", Schema: `{}`},
		form{ID: "theirs", ProjectID: "p-b", Schema: `{}`})
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/forms?projectId=p-a", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []formMeta
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 || rows[0].ID != "mine" {
		t.Errorf("rows = %+v, want only p-a's form", rows)
	}
}

// TestFormsDeleteReportsAStoreThatCannotBeWritten: a delete that did not happen must
// not answer as one that did — the Modeler would drop the form from its list while
// it is still bound and still served.
func TestFormsDeleteReportsAStoreThatCannotBeWritten(t *testing.T) {
	srv := newServerForErrors(t)
	if err := os.RemoveAll(srv.forms.Dir()); err != nil {
		t.Fatal(err)
	}
	code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/forms/gone", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "delete form") {
		t.Errorf("unwritable store: %d (%s), want 500 'delete form'", code, body)
	}
}
