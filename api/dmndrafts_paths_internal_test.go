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

// dmnDraftsPathsXML is a decision model body; the save does not compile it, so its
// content only has to be there.
const dmnDraftsPathsXML = `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" name="Draft"/>`

// dmnDraftsPathsCount is how many decision drafts are stored.
func dmnDraftsPathsCount(t *testing.T, srv *Server) int {
	t.Helper()
	entries, err := os.ReadDir(srv.dmnDrafts.Dir())
	if err != nil {
		t.Fatalf("read drafts: %v", err)
	}
	return len(entries)
}

// dmnDraftsPathsAs calls a decision-draft handler directly as a principal.
func dmnDraftsPathsAs(h http.HandlerFunc, method, path, body string, p *httpapi.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// TestDmnDraftsRefusesABodyThatCannotBeRead: a draft is somebody's unsaved work, and
// half of it stored over the last good save would lose the rest. A body that broke
// off is refused and nothing is written.
func TestDmnDraftsRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/dmn-drafts", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Fatalf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if n := dmnDraftsPathsCount(t, srv); n != 0 {
		t.Errorf("%d draft(s) stored after a refused save", n)
	}
}

// TestDmnDraftsStopsOnAReferenceItCannotRead: a draft on an existing decision is filed
// where that decision is, and needs editor on it. With the decision's reference
// unreadable neither can be decided, so the save stops rather than file the draft as
// Ungrouped — out of the application it belongs to.
func TestDmnDraftsStopsOnAReferenceItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	corrupt(t, srv.dmnrefs.Dir(), "ref-1")
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/dmn-drafts",
		`{"refId":"ref-1","xml":`+strconvQuoteForDmnDraftsPaths(dmnDraftsPathsXML)+`}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read dmn reference") {
		t.Fatalf("unreadable reference: %d (%s), want 500", code, body)
	}
	if n := dmnDraftsPathsCount(t, srv); n != 0 {
		t.Errorf("%d draft(s) stored although the reference could not be read", n)
	}
}

// TestDmnDraftsKeepsSomebodyElsesDecisionTheirs: a draft on a decision is an edit of
// it, so somebody who may not edit the decision may not draft on it either — and a
// personal decision of somebody else's is not even there for them. The same rule
// keeps another person's personal drafts out of the listing.
func TestDmnDraftsKeepsSomebodyElsesDecisionTheirs(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	var err error
	srv.do(func() {
		if err = srv.dmnrefs.Save(dmnRef{ID: "anns-ref", Name: "Ann's", ModelRef: "anns-model", OwnerID: "usr_ann"}); err != nil {
			return
		}
		err = srv.dmnDrafts.Save(dmnDraft{ID: "draft-ann", Name: "Ann's draft", OwnerID: "usr_ann", XML: dmnDraftsPathsXML})
	})
	if err != nil {
		t.Fatal(err)
	}
	bob := &httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleModeler}}
	ann := &httpapi.Principal{UserID: "usr_ann", Username: "ann", Roles: []string{RoleModeler}}

	rec := dmnDraftsPathsAs(srv.handleSaveDmnDraft, http.MethodPost, "/api/v1/dmn-drafts",
		`{"refId":"anns-ref","xml":`+strconvQuoteForDmnDraftsPaths(dmnDraftsPathsXML)+`}`, bob)
	if rec.Code != http.StatusNotFound {
		t.Errorf("bob drafting on ann's decision: %d (%s), want 404", rec.Code, rec.Body)
	}
	if n := dmnDraftsPathsCount(t, srv); n != 1 {
		t.Errorf("%d drafts stored, want only ann's", n)
	}

	for who, want := range map[*httpapi.Principal]int{bob: 0, ann: 1} {
		rec := dmnDraftsPathsAs(srv.handleListDmnDrafts, http.MethodGet, "/api/v1/dmn-drafts", "", who)
		var rows []dmnDraftResp
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != want {
			t.Errorf("%s lists %d (%d %s), want %d", who.Username, len(rows), rec.Code, rec.Body, want)
		}
	}
}

// strconvQuoteForDmnDraftsPaths renders a string as a JSON string literal.
func strconvQuoteForDmnDraftsPaths(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
