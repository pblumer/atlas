package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestDraftSessionAccess exercises the session scope gate (ADR-0140/0071): a
// session inherits the sharing scope of the draft's project — at least viewer to
// watch, editor/owner to co-edit — while an Ungrouped draft stays open.
func TestDraftSessionAccess(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())

	proj := project{
		ID: "proj1", OwnerID: "usr_owner", Visibility: VisibilityShared,
		Members: []projectMember{
			{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_editor"}, Role: ScopeRoleEditor},
			{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_viewer"}, Role: ScopeRoleViewer},
		},
	}
	srv.do(func() {
		if err := srv.projects.Save(proj); err != nil {
			t.Fatalf("save project: %v", err)
		}
		if err := srv.drafts.Save(draft{ProcessID: "d_scoped", Name: "Scoped", ProjectID: "proj1", XML: "<x/>"}); err != nil {
			t.Fatalf("save scoped draft: %v", err)
		}
		if err := srv.drafts.Save(draft{ProcessID: "d_free", Name: "Free", XML: "<x/>"}); err != nil {
			t.Fatalf("save ungrouped draft: %v", err)
		}
	})

	reqAs := func(p *httpapi.Principal) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if p != nil {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		}
		return r
	}
	user := func(id string) *httpapi.Principal { return &httpapi.Principal{UserID: id, Roles: []string{RoleUser}} }

	cases := []struct {
		name        string
		draftID     string
		pr          *httpapi.Principal
		wantCanEdit bool
		wantStatus  int
	}{
		{"owner edits scoped", "d_scoped", user("usr_owner"), true, 0},
		{"editor edits scoped", "d_scoped", user("usr_editor"), true, 0},
		{"viewer watches scoped read-only", "d_scoped", user("usr_viewer"), false, 0},
		{"stranger hidden from scoped", "d_scoped", user("usr_stranger"), false, http.StatusNotFound},
		{"nil principal hidden from scoped", "d_scoped", nil, false, http.StatusNotFound},
		{"ungrouped draft is open", "d_free", user("usr_stranger"), true, 0},
		{"missing draft 404", "nope", user("usr_owner"), false, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canEdit, status, _ := srv.draftSessionAccess(reqAs(tc.pr), tc.draftID)
			if canEdit != tc.wantCanEdit || status != tc.wantStatus {
				t.Fatalf("draftSessionAccess(%q) = (canEdit=%v, status=%d), want (canEdit=%v, status=%d)",
					tc.draftID, canEdit, status, tc.wantCanEdit, tc.wantStatus)
			}
		})
	}
}
