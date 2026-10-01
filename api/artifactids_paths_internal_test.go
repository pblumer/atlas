package api

import (
	"net/http"
	"strings"
	"testing"
)

// artifactIDsPathsKinds are the two id checks: the Modeler's Process ID field and the
// form editor's ID field, each with the store its ids live in.
var artifactIDsPathsKinds = []struct {
	name, route, says string
	dir               func(*Server) string
	seed              func(*Server) error
}{
	{"draft", "/api/v1/drafts/taken/availability", "read draft",
		func(s *Server) string { return s.drafts.Dir() },
		func(s *Server) error { return s.drafts.Save(draft{ProcessID: "taken", Name: "Taken"}) }},
	{"form", "/api/v1/forms/taken/availability", "read form",
		func(s *Server) string { return s.forms.Dir() },
		func(s *Server) error { return s.forms.Save(form{ID: "taken", Name: "Taken", Schema: `{}`}) }},
}

// TestArtifactIDsCannotCallAnIDFreeFromARecordItCannotRead: "available" is a promise
// that saving under the id will not overwrite anything. A record under that id that
// cannot be read may be exactly what would be overwritten, so the check fails
// instead of answering.
func TestArtifactIDsCannotCallAnIDFreeFromARecordItCannotRead(t *testing.T) {
	for _, k := range artifactIDsPathsKinds {
		t.Run(k.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			corrupt(t, k.dir(srv), "taken")
			code, body := serveInternal(t, srv, http.MethodGet, k.route, "", "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), k.says) {
				t.Errorf("unreadable record: %d (%s), want 500 %q", code, body, k.says)
			}
		})
	}
}

// TestArtifactIDsCannotSayWhoHoldsAnIDWithoutTheApplications: a taken id names its
// holder only to somebody who may see it, and that is a question about the
// application it is filed in. With the applications unreadable it cannot be
// answered either way — naming the holder could leak it, and leaving it out would
// read as "hidden from you".
func TestArtifactIDsCannotSayWhoHoldsAnIDWithoutTheApplications(t *testing.T) {
	for _, k := range artifactIDsPathsKinds {
		t.Run(k.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			var err error
			srv.do(func() { err = k.seed(srv) })
			if err != nil {
				t.Fatal(err)
			}
			approvalsPathsDirAsFile(t, srv.projects.Dir())
			code, body := serveInternal(t, srv, http.MethodGet, k.route, "", "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), k.says) {
				t.Errorf("unreadable applications: %d (%s), want 500 %q", code, body, k.says)
			}
		})
	}
}
