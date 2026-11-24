package api_test

import (
	"net/http"
	"testing"
)

// TestTheStandingSystemGrantIsShownToAnAdministratorAlone: the consent screen of a
// system that is already connected says so — who approved it, when, and what it may
// reach, project by project — because approving again replaces that grant, and the
// person about to approve has to know. Only an administrator approves a system
// (ADR-0512 §2), so only an administrator is that person. The route answers without a
// session, and it used to add the grant for every caller: anybody who knew a
// connected system's client id could read who had approved it and which projects it
// reaches.
func TestTheStandingSystemGrantIsShownToAnAdministratorAlone(t *testing.T) {
	ts, admin := connectedAtlas(t)
	system := newFakeSystem(t)
	system.connect(t, admin, ts.URL, nil, false)
	contextURL := ts.URL + "/api/v1/oauth/authorize-context?" + system.authorizeQuery(ts.URL+"/system", "").Encode()

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"uma","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("creating a plain account: %d %s", code, b)
	}
	plain := newClient(t)
	if code := login(t, plain, ts, "uma", "password1"); code != http.StatusOK {
		t.Fatalf("signing the plain account in: %d", code)
	}

	for _, tc := range []struct {
		name        string
		client      *http.Client
		wantCurrent bool
	}{
		{"nobody signed in", &http.Client{}, false},
		{"an account that is no administrator", plain, false},
		{"an administrator", admin, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, ctx := csGetJSON(t, tc.client, contextURL)
			if code != http.StatusOK {
				t.Fatalf("authorize-context = %d %v", code, ctx)
			}
			sys, _ := ctx["system"].(map[string]any)
			if sys == nil {
				t.Fatalf("the answer names no system: %v", ctx)
			}
			if _, has := sys["current"]; has != tc.wantCurrent {
				t.Errorf("the standing grant is in the answer: %v, want %v (system = %v)", has, tc.wantCurrent, sys)
			}
			// What the screen needs to say that the caller may not approve stays.
			if may, _ := sys["mayApprove"].(bool); may != tc.wantCurrent {
				t.Errorf("mayApprove = %v, want %v", may, tc.wantCurrent)
			}
		})
	}
}
