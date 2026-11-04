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

// The refusals of ADR-draft-password-set-by-its-owner: every way a password link, a
// password change or a recovery request is turned away, and the deprecated route an
// administrator could still set a password through.

// TestThePasswordPageIsStaticAndKeepsItsAddressHome: the page carries no token (that is
// in the fragment) and tells the browser to hand its address to nobody and keep no copy.
func TestThePasswordPageIsStaticAndKeepsItsAddressHome(t *testing.T) {
	srv, _ := newSystemServer(t)
	r := httptest.NewRequest(http.MethodGet, passwordPagePath, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", passwordPagePath, rec.Code)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(rec.Body.String(), "/public/password/link") {
		t.Error("the page does not ask for the link's state")
	}
}

// TestThePasswordRule: the one rule every password meets, wherever it is set.
func TestThePasswordRule(t *testing.T) {
	for pw, ok := range map[string]bool{
		"kurz":                  false,
		strings.Repeat("x", 73): false,
		"Erika":                 false,
		"einGutesPasswort1":     true,
		strings.Repeat("ä", 36): true, // 72 bytes
		strings.Repeat("ä", 37): false,
	} {
		if err := validateNewPassword(pw, "erika"); (err == nil) != ok {
			t.Errorf("validateNewPassword(%d bytes) = %v, want ok=%v", len(pw), err, ok)
		}
	}
}

// TestNoLinkWithoutItsIngredients: a link needs the vault, the external URL, a local
// enabled account — and from a mail, an account with an address that the mail goes to.
func TestNoLinkWithoutItsIngredients(t *testing.T) {
	srv := passwordServer(t)
	now := time.Now()
	local := User{ID: "usr_e00000000000000000000001", Username: "eva", Email: "eva@example.org", Source: SourceLocal, PasswordHash: mustHash(t, "evasPasswort1")}
	if passwordPurpose(local) != "reset" || passwordLinkTTL(local) != resetLinkTTL {
		t.Error("an account with a password is not reset, or not for an hour")
	}
	if _, _, err := srv.passwordToken(User{ID: "usr_x", Username: "ext", Source: SourceOIDC}, now); err == nil {
		t.Error("an identity provider's account was minted a link")
	}

	d := mailDirectory{srv}
	if _, err := d.PasswordLink(passwordLinkProcessID, "", []string{"a@example.org"}, nil, nil); err == nil {
		t.Error("an empty account name minted a link")
	}
	if _, err := d.PasswordLink(passwordLinkProcessID, "niemand", []string{"a@example.org"}, nil, nil); err == nil {
		t.Error("an unknown account minted a link")
	}
	noMail := User{ID: "usr_e00000000000000000000002", Username: "ohne", Source: SourceLocal}
	if err := srv.users.Save(noMail); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PasswordLink(passwordLinkProcessID, "ohne", []string{"a@example.org"}, nil, nil); err == nil {
		t.Error("an account without an address minted a link")
	}

	origin := srv.externalURL
	srv.externalURL = ""
	if _, _, err := srv.passwordLinkFor(local, now); err == nil {
		t.Error("a link was built without an external URL")
	}
	srv.externalURL = origin

	v := srv.vault
	srv.vault = nil
	if _, _, err := srv.passwordToken(local, now); err == nil {
		t.Error("a link was signed without a vault")
	}
	srv.vault = v
}

// TestALinkRequestThatCannotBeRead: a set without a token, with a body that is not JSON,
// or with no Authorization at all is refused before anything is read.
func TestALinkRequestThatCannotBeRead(t *testing.T) {
	srv := passwordServer(t)
	if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", "", ""); code != http.StatusNotFound {
		t.Errorf("GET without a token = %d, want 404", code)
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", "abc.def.0123", `{"password":"einGutesPasswort1"}`); code != http.StatusNotFound {
		t.Errorf("POST with a malformed token = %d, want 404", code)
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", "x.1.y", `not json`); code != http.StatusBadRequest {
		t.Errorf("POST with a body that is not JSON = %d, want 400", code)
	}
	// A token whose id is not base64, whose expiry is not a number, or whose mac is not hex.
	for _, tok := range []string{"!!!.1999999999." + strings.Repeat("0", 32), "dXNy.abc." + strings.Repeat("0", 32), "dXNy.1999999999." + strings.Repeat("z", 32)} {
		if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", tok, ""); code != http.StatusNotFound {
			t.Errorf("GET with %q = %d, want 404", tok, code)
		}
	}
}

// TestForgotIgnoresWhatItCannotUse: an empty or absurdly long input and a malformed body
// are answered without anything being looked up, and an instance that cannot mail a
// link says so to the login screen.
func TestForgotIgnoresWhatItCannotUse(t *testing.T) {
	srv := passwordServer(t)
	for _, body := range []string{`{"login":""}`, `{"login":"` + strings.Repeat("a", 400) + `"}`} {
		if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", body); code != http.StatusAccepted {
			t.Errorf("forgot %d bytes = %d, want 202", len(body), code)
		}
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `nope`); code != http.StatusBadRequest {
		t.Errorf("forgot with a body that is not JSON = %d, want 400", code)
	}
	if msgs, _ := srv.mailOutbox.Messages(0); len(msgs) != 0 {
		t.Errorf("unusable requests sent %d mails", len(msgs))
	}

	bare, _ := newSystemServer(t)
	if bare.passwordRecoveryReady() {
		t.Error("an instance without an external URL or the system process offers recovery")
	}
	if _, err := bare.startPasswordLinkProcess(User{Username: "x"}, passwordAnlassForgot); err == nil {
		t.Error("a link process was started on an instance that has none deployed")
	}
}

// TestAnAdministratorsLinkRefusals: no account, an account of an identity provider, a
// disabled one, and sending to an account without an address or from an instance that
// cannot build links are each refused with what to do instead.
func TestAnAdministratorsLinkRefusals(t *testing.T) {
	srv := passwordServer(t)
	for _, u := range []User{
		{ID: "usr_f00000000000000000000001", Username: "oidc", Email: "o@example.org", Source: SourceOIDC},
		{ID: "usr_f00000000000000000000002", Username: "aus", Email: "aus@example.org", Source: SourceLocal, Disabled: true},
		{ID: "usr_f00000000000000000000003", Username: "ohne", Source: SourceLocal},
	} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	link := func(id, body string) int {
		code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/users/"+id+"/password-link", body, "application/json")
		return code
	}
	for name, c := range map[string]struct {
		id, body string
		want     int
	}{
		"no such account":     {"usr_f0000000000000000000000f", `{}`, http.StatusNotFound},
		"identity provider":   {"usr_f00000000000000000000001", `{}`, http.StatusConflict},
		"disabled":            {"usr_f00000000000000000000002", `{}`, http.StatusConflict},
		"send without a mail": {"usr_f00000000000000000000003", `{"send":true}`, http.StatusConflict},
		"not JSON":            {"usr_f00000000000000000000003", `nope`, http.StatusBadRequest},
	} {
		if got := link(c.id, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	// Without an external URL the link is handed over as a path, and not sent.
	srv.externalURL = ""
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/users/usr_f00000000000000000000003/password-link", `{}`, "application/json")
	if code != http.StatusOK || strings.Contains(string(raw), `"url"`) || !strings.Contains(string(raw), `"path":"/public/password#`) {
		t.Errorf("hand-over without an external URL = %d %s", code, raw)
	}
	_ = srv.users.Save(User{ID: "usr_f00000000000000000000004", Username: "mitmail", Email: "m@example.org", Source: SourceLocal})
	if got := link("usr_f00000000000000000000004", `{"send":true}`); got != http.StatusConflict {
		t.Errorf("send without an external URL = %d, want 409", got)
	}
}

// TestChangingAPasswordNeedsAPersonWithOne: a machine credential, an account of an
// identity provider and an unchanged password are refused.
func TestChangingAPasswordNeedsAPersonWithOne(t *testing.T) {
	srv, _ := newSystemServer(t, WithAuth())
	_ = srv.users.Save(User{ID: "usr_g00000000000000000000001", Username: "gina", Source: SourceLocal, PasswordHash: mustHash(t, "ginasPasswort1")})
	_ = srv.users.Save(User{ID: "usr_g00000000000000000000002", Username: "fed", Source: SourceOIDC})

	change := func(p *httpapi.Principal, body string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
		r = r.WithContext(httpapi.WithPrincipal(context.Background(), p))
		rec := httptest.NewRecorder()
		srv.handleChangeOwnPassword(rec, r)
		return rec.Code
	}
	const body = `{"currentPassword":"ginasPasswort1","newPassword":"ginasPasswort1"}`
	if got := change(&httpapi.Principal{UserID: "tok_1", Username: "ci"}, body); got != http.StatusForbidden {
		t.Errorf("a machine credential: %d, want 403", got)
	}
	if got := change(nil, body); got != http.StatusForbidden {
		t.Errorf("nobody: %d, want 403", got)
	}
	if got := change(&httpapi.Principal{UserID: "usr_g00000000000000000000002", Username: "fed", Session: true}, body); got != http.StatusConflict {
		t.Errorf("an identity provider's account: %d, want 409", got)
	}
	gina := &httpapi.Principal{UserID: "usr_g00000000000000000000001", Username: "gina", Session: true}
	if got := change(gina, body); got != http.StatusBadRequest {
		t.Errorf("the same password again: %d, want 400", got)
	}
	if got := change(gina, `nope`); got != http.StatusBadRequest {
		t.Errorf("a body that is not JSON: %d, want 400", got)
	}
}

// TestTheDeprecatedSetPasswordRoute: an administrator can still set a password through
// it, held to the same rule, and every session of the account ends.
func TestTheDeprecatedSetPasswordRoute(t *testing.T) {
	srv, _ := newSystemServer(t)
	u := User{ID: "usr_h00000000000000000000001", Username: "hans", Source: SourceLocal, Roles: []string{RoleUser}}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, _ := srv.sessions.create(u, nil)
	set := func(id, body string) int {
		code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/users/"+id+"/password", body, "application/json")
		return code
	}
	if got := set(u.ID, `{"password":"kurz"}`); got != http.StatusBadRequest {
		t.Errorf("a short password: %d, want 400", got)
	}
	if got := set("usr_h0000000000000000000000f", `{"password":"einGutesPasswort1"}`); got != http.StatusNotFound {
		t.Errorf("no such account: %d, want 404", got)
	}
	if got := set(u.ID, `{"password":"einGutesPasswort1"}`); got != http.StatusOK {
		t.Fatalf("set: %d", got)
	}
	if _, ok := srv.sessions.lookup(tok); ok {
		t.Error("a session of the account survived an administrator setting its password")
	}

	// Creating an account with a password of its own is held to the same rule.
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/users", `{"username":"ida","password":"kurz"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("create with a short password: %d %s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/users", `{"username":"ida","password":"idasPasswort1"}`, "application/json")
	if code != http.StatusCreated || strings.Contains(string(raw), "awaitingActivation") {
		t.Errorf("create with a password: %d %s", code, raw)
	}
}

// TestOnlyAnUnauthenticatedServerOrItsOwnWorkerLeasesALink: without authentication every
// caller is everybody; and a job that is not a mail, or does not exist, carries no link.
func TestOnlyAnUnauthenticatedServerOrItsOwnWorkerLeasesALink(t *testing.T) {
	srv, _ := newSystemServer(t)
	if !srv.ownWorker(httptest.NewRequest(http.MethodPost, "/api/v1/jobs/activate", nil)) {
		t.Error("a server without authentication refused its only caller")
	}
	var carries bool
	srv.do(func() { carries = srv.carriesPasswordLink(424242) })
	if carries {
		t.Error("a job that does not exist carries a password link")
	}
}
