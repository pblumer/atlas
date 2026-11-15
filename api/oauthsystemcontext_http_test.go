package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestTheCurrentSystemGrantIsToldToAnAdministratorOnly: the route the consent page
// asks is public, because the page has to render for somebody who is not signed in
// yet. What it says about a system that is already connected — who approved it, when,
// and which projects each capability reaches — is there for the one person the page
// shows it to: the administrator about to replace that approval. Anybody else is told
// what the system asks for and that they may not decide, and nothing about the grant
// that stands.
func TestTheCurrentSystemGrantIsToldToAnAdministratorOnly(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	sys.connect(t, admin, ts.URL, nil, false)
	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")

	u := ts.URL + "/api/v1/oauth/authorize-context?" + sys.authorizeQuery(ts.URL+"/system", "").Encode()
	for _, tc := range []struct {
		name  string
		c     *http.Client
		admin bool
	}{
		{"nobody signed in", &http.Client{}, false},
		{"a person who is not an administrator", alice, false},
		{"an administrator", admin, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, ctx := csGetJSON(t, tc.c, u)
			system, _ := ctx["system"].(map[string]any)
			if status != http.StatusOK || system == nil {
				t.Fatalf("the context = %d %v, want the system's request described", status, ctx)
			}
			if may, _ := system["mayApprove"].(bool); may != tc.admin {
				t.Errorf("mayApprove = %v, want %v", may, tc.admin)
			}
			current, _ := system["current"].(map[string]any)
			if tc.admin {
				if current == nil || current["approvedBy"] != "root" {
					t.Errorf("an administrator is not shown the grant they would replace: %v", system["current"])
				}
				return
			}
			if current != nil {
				t.Errorf("the grant that stands was told: %v", current)
			}
			// Not only under that name: nothing of the grant may appear anywhere in
			// the answer.
			whole, _ := json.Marshal(ctx)
			for _, leak := range []string{"approvedBy", "approvedAt", "capabilities", "preconsent", "projects"} {
				if strings.Contains(string(whole), `"`+leak+`"`) {
					t.Errorf("the answer carries %q: %s", leak, whole)
				}
			}
		})
	}
}
