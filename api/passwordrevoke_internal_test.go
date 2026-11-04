package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// Revoking a password before its owner is sent a link (ADR-0526, amendment of
// 2026-11-04): for a password somebody else knows, a link alone would leave it valid
// until the owner uses the link.

// revokeAs serves POST /api/v1/users/{id}/password-link as the administrator admin.
func revokeAs(t *testing.T, srv *Server, admin *httpapi.Principal, id, body string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+id+"/password-link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", id)
	r = r.WithContext(httpapi.WithPrincipal(context.Background(), admin))
	rec := httptest.NewRecorder()
	srv.handleUserPasswordLink(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// TestARevokedPasswordStopsWorkingAtOnce: the known password no longer signs in, every
// session ends, and the owner gets an activation link — handed over, or mailed.
func TestARevokedPasswordStopsWorkingAtOnce(t *testing.T) {
	srv := passwordServer(t)
	admin := &httpapi.Principal{UserID: "usr_r00000000000000000000000", Username: "root", Roles: []string{RoleAdmin}, Session: true}
	victim := User{ID: "usr_r00000000000000000000001", Username: "sven", Email: "sven@example.org", Source: SourceLocal,
		Roles: []string{RoleAdmin}, PasswordHash: mustHash(t, "geleaktesPasswort1")}
	other := User{ID: admin.UserID, Username: "root", Source: SourceLocal, Roles: []string{RoleAdmin}, PasswordHash: mustHash(t, "rootPasswort123")}
	for _, u := range []User{victim, other} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	tok, _ := srv.sessions.create(victim, nil)

	code, raw := revokeAs(t, srv, admin, victim.ID, `{"revoke":true}`)
	if code != http.StatusOK {
		t.Fatalf("revoke and hand over: %d %s", code, raw)
	}
	var resp struct {
		Path    string `json:"path"`
		Purpose string `json:"purpose"`
		Revoked bool   `json:"revoked"`
	}
	_ = json.Unmarshal(raw, &resp)
	if !resp.Revoked || resp.Purpose != "activate" {
		t.Errorf("answer = %+v, want a revoked account and an activation link", resp)
	}
	cur, _, _ := srv.users.Get(victim.ID)
	if checkPassword(cur.PasswordHash, "geleaktesPasswort1") || !cur.toPublic().AwaitingActivation {
		t.Fatal("the known password still signs in")
	}
	if _, ok := srv.sessions.lookup(tok); ok {
		t.Error("a session of the account survived the revocation")
	}
	// The link handed over sets the new password.
	link := strings.TrimPrefix(resp.Path, passwordPagePath+"#")
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", link, `{"password":"neuesPasswort22"}`); code != http.StatusOK {
		t.Fatalf("the activation link does not set the password: %d", code)
	}

	// Revoking and sending mails the link through the system process.
	code, raw = revokeAs(t, srv, admin, victim.ID, `{"revoke":true,"send":true}`)
	if code != http.StatusAccepted || !strings.Contains(string(raw), `"revoked":true`) {
		t.Fatalf("revoke and send: %d %s", code, raw)
	}
	if msgs := mailsTo(srv, "sven@example.org"); len(msgs) != 1 {
		t.Errorf("the owner was mailed %d times, want once", len(msgs))
	}
	if cur, _, _ := srv.users.Get(victim.ID); cur.PasswordHash != "" {
		t.Error("revoke and send left the password in place")
	}
}

// TestRevokingIsRefusedWhereItWouldLockSomebodyOut: not the caller's own account, not
// the last administrator who can sign in, and nothing is revoked when the link cannot
// be sent afterwards.
func TestRevokingIsRefusedWhereItWouldLockSomebodyOut(t *testing.T) {
	srv := passwordServer(t)
	root := User{ID: "usr_s00000000000000000000001", Username: "root", Email: "root@example.org", Source: SourceLocal, Roles: []string{RoleAdmin}, PasswordHash: mustHash(t, "rootPasswort123")}
	ohne := User{ID: "usr_s00000000000000000000002", Username: "ohne", Source: SourceLocal, Roles: []string{RoleUser}, PasswordHash: mustHash(t, "ohnesPasswort12")}
	for _, u := range []User{root, ohne} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	self := &httpapi.Principal{UserID: root.ID, Username: "root", Roles: []string{RoleAdmin}, Session: true}
	if code, _ := revokeAs(t, srv, self, root.ID, `{"revoke":true}`); code != http.StatusConflict {
		t.Errorf("revoking one's own password: %d, want 409", code)
	}
	someoneElse := &httpapi.Principal{UserID: "tok_x", Username: "ci", Roles: []string{RoleAdmin}}
	if code, raw := revokeAs(t, srv, someoneElse, root.ID, `{"revoke":true}`); code != http.StatusConflict || !strings.Contains(string(raw), "last administrator") {
		t.Errorf("revoking the last administrator who can sign in: %d %s, want 409", code, raw)
	}
	// Sending to an account without an address is refused before anything is revoked.
	if code, _ := revokeAs(t, srv, self, ohne.ID, `{"revoke":true,"send":true}`); code != http.StatusConflict {
		t.Errorf("revoke and send without an address: %d, want 409", code)
	}
	if cur, _, _ := srv.users.Get(ohne.ID); !checkPassword(cur.PasswordHash, "ohnesPasswort12") {
		t.Error("a refused request revoked the password anyway")
	}
	// So is any request on a server without a vault, which could sign no link.
	srv.vault = nil
	if code, _ := revokeAs(t, srv, self, ohne.ID, `{"revoke":true}`); code != http.StatusConflict {
		t.Errorf("revoke without a vault: %d, want 409", code)
	}
	if cur, _, _ := srv.users.Get(ohne.ID); cur.PasswordHash == "" {
		t.Error("a request that could sign no link revoked the password")
	}
}

// TestRevokeAndSendNeedsTheSystemProcess: without proc_passwort_link nothing is revoked,
// because no link would follow.
func TestRevokeAndSendNeedsTheSystemProcess(t *testing.T) {
	srv, _ := newSystemServer(t, WithExternalURL(passwordTestOrigin))
	u := User{ID: "usr_t00000000000000000000001", Username: "tina", Email: "tina@example.org", Source: SourceLocal, PasswordHash: mustHash(t, "tinasPasswort1")}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}, Session: true}
	if code, _ := revokeAs(t, srv, admin, u.ID, `{"revoke":true,"send":true}`); code != http.StatusConflict {
		t.Errorf("revoke and send without the system process: %d, want 409", code)
	}
	if cur, _, _ := srv.users.Get(u.ID); cur.PasswordHash == "" {
		t.Error("the password was revoked although no link could be sent")
	}
}
