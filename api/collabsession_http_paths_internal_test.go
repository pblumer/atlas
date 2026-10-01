package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestCollabSessionHTTPWillNotOpenASessionOnADraftItCannotRead: who may join, and
// whether they may edit, is read from the draft and its application. A draft that
// cannot be read is a server fault, and opening a session on it anyway would give
// everybody edit rights on something nobody can check the scope of.
func TestCollabSessionHTTPWillNotOpenASessionOnADraftItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name, path, says string
		dir              func(*Server) string
	}{
		{"process draft", "/api/v1/drafts/wip/session/join", "read draft",
			func(s *Server) string { return s.drafts.Dir() }},
		{"decision draft", "/api/v1/dmn-drafts/wip/session/join", "read decision draft",
			func(s *Server) string { return s.dmnDrafts.Dir() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			corrupt(t, tc.dir(srv), "wip")
			code, body := serveInternal(t, srv, http.MethodPost, tc.path, "", "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), tc.says) {
				t.Errorf("join over an unreadable draft: %d (%s), want 500 %q", code, body, tc.says)
			}
		})
	}
}
