package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The budgets and the failure paths of the password routes
// (ADR-0526): every public route answers 429 once its address
// has spent its budget, a password change is throttled like a login, and a link that
// cannot be mailed is reported rather than claimed.

// TestThePasswordRoutesAreRateLimited: page, link state, set and forgot each refuse an
// address that has spent the public budget, before anything is read.
func TestThePasswordRoutesAreRateLimited(t *testing.T) {
	srv := passwordServer(t)
	srv.publicRate = newRateLimiter(0, 0) // nothing left to spend
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, passwordPagePath, ""},
		{http.MethodGet, passwordPagePath + "/link", ""},
		{http.MethodPost, passwordPagePath + "/link", `{"password":"einGutesPasswort1"}`},
		{http.MethodPost, passwordPagePath + "/forgot", `{"login":"anna"}`},
	} {
		if code, _ := bearer(t, srv, c.method, c.path, "x.1.y", c.body); code != http.StatusTooManyRequests {
			t.Errorf("%s %s with no budget left = %d, want 429", c.method, c.path, code)
		}
	}
}

// TestChangingAPasswordIsThrottledLikeALogin: guessing the current password through the
// change route costs the same budget as guessing it at the login.
func TestChangingAPasswordIsThrottledLikeALogin(t *testing.T) {
	srv, _ := newSystemServer(t, WithAuth())
	u := User{ID: "usr_u00000000000000000000001", Username: "uwe", Source: SourceLocal, PasswordHash: mustHash(t, "uwesPasswort12")}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	p := &httpapi.Principal{UserID: u.ID, Username: u.Username, Session: true}
	throttled := false
	for range 10 {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password",
			strings.NewReader(`{"currentPassword":"geraten","newPassword":"neuesPasswort1"}`))
		r = r.WithContext(httpapi.WithPrincipal(context.Background(), p))
		rec := httptest.NewRecorder()
		srv.handleChangeOwnPassword(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("ten wrong current passwords in a row were all checked")
	}
}

// TestALinkThatCannotBeMailedIsNotClaimed: where the system process is missing, forgot
// still answers alike and an administrator's send is refused, and neither starts
// anything.
func TestALinkThatCannotBeMailedIsNotClaimed(t *testing.T) {
	srv, _ := newSystemServer(t, WithExternalURL(passwordTestOrigin))
	u := User{ID: "usr_v00000000000000000000001", Username: "vera", Email: "vera@example.org", Source: SourceLocal, PasswordHash: mustHash(t, "verasPasswort1")}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `{"login":"vera"}`); code != http.StatusAccepted {
		t.Errorf("forgot without the system process = %d, want 202 like every answer", code)
	}
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/users/"+u.ID+"/password-link", `{"send":true}`, "application/json")
	if code != http.StatusConflict || !strings.Contains(string(raw), passwordLinkProcessID) {
		t.Errorf("send without the system process = %d %s, want 409 naming it", code, raw)
	}
}

// TestALinkDiesWithTheAccountsState: a link minted while the account was enabled opens
// nothing once it is disabled, and revoking an account that no longer exists is a 404.
func TestALinkDiesWithTheAccountsState(t *testing.T) {
	srv := passwordServer(t)
	u := User{ID: "usr_w00000000000000000000001", Username: "walter12345", Email: "w@example.org", Source: SourceLocal}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, _, err := srv.passwordToken(u, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", tok, ""); code != http.StatusNotFound {
		t.Errorf("a link of an account disabled since = %d, want 404", code)
	}
	if err := validateNewPassword("Walter12345", "walter12345"); err == nil {
		t.Error("the username, differently cased, is accepted as a password")
	}

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(httpapi.WithPrincipal(context.Background(), &httpapi.Principal{UserID: "usr_admin"}))
	ghost := User{ID: "usr_w0000000000000000000000f", Username: "ghost", Source: SourceLocal}
	if _, status, err := srv.revokePassword(r, ghost); err == nil || status != http.StatusNotFound {
		t.Errorf("revoking an account that does not exist = %d %v, want 404", status, err)
	}
}
