package api

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The day an account last signed in (ADR-draft-last-login-day): what the roster
// shows, when it is written, and what it never becomes.

// lateEveningInZurich is half past midnight on 24 October in Zurich, which is still
// 23 October in UTC. Tests sign in at this instant so that every one of them also
// says which calendar the day is counted in.
var lateEveningInZurich = time.Date(2026, 10, 24, 0, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

// loginDayServer is an auth-enforcing server whose sessions open at a fixed instant,
// with one ordinary account besides the bootstrap administrator.
func loginDayServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	srv, _ := newOffLoopServer(t, WithAuth())
	srv.sessions.now = func() time.Time { return lateEveningInZurich }
	saveAda(t, srv)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// saveAda writes the ordinary account the tests sign in as. Its UpdatedAt is a value
// no handler would write, so a test can tell whether anything rewrote it.
func saveAda(t *testing.T, srv *Server) {
	t.Helper()
	hash, err := hashPassword("ada-password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	var saveErr error
	srv.do(func() {
		saveErr = srv.users.Save(User{
			ID: "u-ada", Username: "ada", Source: SourceLocal, Roles: []string{RoleUser},
			PasswordHash: hash, CreatedAt: 1, UpdatedAt: 7, RolesUpgradedAt: 1,
		})
	})
	if saveErr != nil {
		t.Fatalf("save ada: %v", saveErr)
	}
}

// signIn posts a password login from a fresh browser and returns the status and
// the browser, which then carries the session.
func signIn(t *testing.T, ts *httptest.Server, username, password string) (int, *http.Client) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := &http.Client{Jar: jar}
	resp, err := c.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode, c
}

// storedUser reads an account as the store holds it now.
func storedUser(t *testing.T, srv *Server, id string) (User, bool) {
	t.Helper()
	var (
		u   User
		ok  bool
		err error
	)
	srv.do(func() { u, ok, err = srv.users.Get(id) })
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return u, ok
}

// TestALoginRecordsTheDayAndNothingElse is the feature: after somebody signs in, the
// roster says on which day, and that is the only thing the login changed about the
// account.
func TestALoginRecordsTheDayAndNothingElse(t *testing.T) {
	srv, ts := loginDayServer(t)

	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("ada login = %d, want 200", code)
	}
	srv.flushLoginDates()

	u, ok := storedUser(t, srv, "u-ada")
	if !ok {
		t.Fatal("ada's account is gone")
	}
	if u.LastLoginDate != "2026-10-23" {
		t.Errorf("LastLoginDate = %q, want 2026-10-23 — the UTC day of 00:30 in Zurich on the 24th", u.LastLoginDate)
	}
	if u.UpdatedAt != 7 {
		t.Errorf("UpdatedAt = %d, want 7: signing in is not changing the account", u.UpdatedAt)
	}

	// The administrator's roster carries it, beside presence.
	code, admin := signIn(t, ts, "root", "rootpassword12")
	if code != http.StatusOK {
		t.Fatalf("root login = %d, want 200", code)
	}
	resp, err := admin.Get(ts.URL + "/api/v1/users")
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	defer resp.Body.Close()
	var roster []struct {
		ID            string `json:"id"`
		LastLoginDate string `json:"lastLoginDate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&roster); err != nil {
		t.Fatalf("decode roster: %v", err)
	}
	found := false
	for _, r := range roster {
		if r.ID == "u-ada" {
			found = true
			if r.LastLoginDate != "2026-10-23" {
				t.Errorf("roster lastLoginDate for ada = %q, want 2026-10-23", r.LastLoginDate)
			}
		}
	}
	if !found {
		t.Fatal("ada is missing from the roster")
	}
}

// TestARefusedLoginRecordsNothing: a wrong password is not somebody using the
// account, and a day written for it would make a guessed-at account look alive.
func TestARefusedLoginRecordsNothing(t *testing.T) {
	srv, ts := loginDayServer(t)

	if code, _ := signIn(t, ts, "ada", "not-her-password"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", code)
	}
	srv.flushLoginDates()

	u, _ := storedUser(t, srv, "u-ada")
	if u.LastLoginDate != "" {
		t.Errorf("LastLoginDate = %q after a refused login, want none", u.LastLoginDate)
	}
}

// TestALoginDayIsWrittenAfterTheLoopIsFree: the login does not wait for the run
// loop (ADR-0265), and the day it leaves behind is not lost because of that — it
// lands on the loop's next turn.
func TestALoginDayIsWrittenAfterTheLoopIsFree(t *testing.T) {
	srv, ts := loginDayServer(t)

	held, release := make(chan struct{}), make(chan struct{})
	go srv.do(func() { close(held); <-release })
	<-held

	done := make(chan int, 1)
	go func() { code, _ := signIn(t, ts, "ada", "ada-password"); done <- code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("login = %d, want 200", code)
		}
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the login waited for the run loop to record its day")
	}

	close(release)
	srv.flushLoginDates()
	if u, _ := storedUser(t, srv, "u-ada"); u.LastLoginDate != "2026-10-23" {
		t.Errorf("LastLoginDate = %q once the loop was free, want 2026-10-23", u.LastLoginDate)
	}
}

// TestALoginTheSameDayAsksForNoWrite: a person who signs in five times a day is one
// write, not five. The record already says today, so there is nothing to note.
func TestALoginTheSameDayAsksForNoWrite(t *testing.T) {
	// No writer goroutine here, so whatever is noted stays in the map to be looked at.
	s := &Server{sessions: newSessionStore(time.Hour), loginDates: newLoginDates()}
	s.sessions.now = func() time.Time { return lateEveningInZurich }

	s.noteLogin(User{ID: "u-ada", LastLoginDate: "2026-10-23"})
	if got := s.loginDates.take(); len(got) != 0 {
		t.Errorf("a second login on the same day noted %v, want nothing", got)
	}

	s.noteLogin(User{ID: "u-ada", LastLoginDate: "2026-10-22"})
	if got := s.loginDates.take(); got["u-ada"] != "2026-10-23" {
		t.Errorf("the first login of a new day noted %v, want u-ada → 2026-10-23", got)
	}
}

// TestALoginDayNeverMovesBack: the stored day is the latest one, whichever write
// arrives last. Two logins either side of midnight, or a clock corrected backwards,
// must not make an account look less recently used than it is.
func TestALoginDayNeverMovesBack(t *testing.T) {
	l := newLoginDates()
	l.note("u-ada", "2026-10-23")
	l.note("u-ada", "2026-10-22")
	if got := l.take()["u-ada"]; got != "2026-10-23" {
		t.Errorf("pending after an older note = %q, want 2026-10-23", got)
	}

	srv, _ := loginDayServer(t)
	srv.do(func() { srv.recordLoginDates(map[string]string{"u-ada": "2026-10-23"}) })
	srv.do(func() { srv.recordLoginDates(map[string]string{"u-ada": "2026-10-20"}) })
	if u, _ := storedUser(t, srv, "u-ada"); u.LastLoginDate != "2026-10-23" {
		t.Errorf("stored after an older write = %q, want 2026-10-23", u.LastLoginDate)
	}
}

// TestALoginDayDoesNotBringBackADeletedAccount: an administrator may delete an
// account between its login and the write of that login's day. The write must find
// nothing to do — a day is not a reason for a record to exist.
func TestALoginDayDoesNotBringBackADeletedAccount(t *testing.T) {
	srv, _ := loginDayServer(t)

	var delErr error
	srv.do(func() { delErr = srv.users.Delete("u-ada") })
	if delErr != nil {
		t.Fatalf("delete: %v", delErr)
	}
	srv.do(func() { srv.recordLoginDates(map[string]string{"u-ada": "2026-10-23"}) })
	if _, ok := storedUser(t, srv, "u-ada"); ok {
		t.Error("recording a login day recreated a deleted account")
	}
}

// TestALoginDayKeepsAnEditMadeAfterTheLogin: the login read the account before an
// administrator disabled it; the day written afterwards must not carry that older
// copy back over the edit.
func TestALoginDayKeepsAnEditMadeAfterTheLogin(t *testing.T) {
	srv, _ := loginDayServer(t)

	before, _ := storedUser(t, srv, "u-ada") // what the login read
	srv.loginDates.note(before.ID, "2026-10-23")

	// The edit, in its own turn, before the day is written.
	var saveErr error
	srv.do(func() {
		u, _, _ := srv.users.Get("u-ada")
		u.Disabled = true
		saveErr = srv.users.Save(u)
	})
	if saveErr != nil {
		t.Fatalf("disable: %v", saveErr)
	}
	srv.flushLoginDates()

	u, _ := storedUser(t, srv, "u-ada")
	if !u.Disabled {
		t.Error("writing the login day re-enabled an account an administrator had disabled")
	}
	if u.LastLoginDate != "2026-10-23" {
		t.Errorf("LastLoginDate = %q, want 2026-10-23", u.LastLoginDate)
	}
}

// TestAnUnreadableAccountDoesNotStopTheOthers: one record that cannot be read is
// that record's problem. The days of every other account in the same write still
// land, and the login that noted it has long since succeeded.
func TestAnUnreadableAccountDoesNotStopTheOthers(t *testing.T) {
	srv, _ := loginDayServer(t)
	if err := os.WriteFile(srv.users.FileFor("u-broken"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}

	srv.do(func() {
		srv.recordLoginDates(map[string]string{"u-broken": "2026-10-23", "u-ada": "2026-10-23"})
	})
	if u, _ := storedUser(t, srv, "u-ada"); u.LastLoginDate != "2026-10-23" {
		t.Errorf("ada's day = %q beside an unreadable record, want 2026-10-23", u.LastLoginDate)
	}
}

// TestALoginDaySurvivesARestart: unlike presence, the day is on the account record,
// and a login right before the server stops is not lost with the process — Close
// writes what the writer had not reached yet.
func TestALoginDaySurvivesARestart(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	dir := t.TempDir()

	open := func() (*Server, func()) {
		log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		store, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		proc := engine.New(1, log, store, nil)
		if err := proc.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		srv, err := New(proc, store, dir, WithAuth())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return srv, func() { srv.Close(); _ = store.Close(); _ = log.Close() }
	}

	srv, stop := open()
	srv.sessions.now = func() time.Time { return lateEveningInZurich }
	saveAda(t, srv)
	ts := httptest.NewServer(srv.Handler())
	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("login = %d, want 200", code)
	}
	ts.Close()
	stop() // no flush: whatever the writer has not done, Close must

	srv, stop = open()
	defer stop()
	if u, _ := storedUser(t, srv, "u-ada"); u.LastLoginDate != "2026-10-23" {
		t.Errorf("LastLoginDate after a restart = %q, want 2026-10-23", u.LastLoginDate)
	}
}

// TestAFederatedLoginRecordsTheDayToo: whatever opens a session is a login, and an
// account that only ever signs in through the provider must not read as unused.
func TestAFederatedLoginRecordsTheDayToo(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	srv.sessions.now = func() time.Time { return lateEveningInZurich }

	resp := federate(t, ts, idp, noRedirects(t))
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want 302", resp.StatusCode)
	}
	srv.flushLoginDates()

	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("the federated login created no account")
	}
	if u.LastLoginDate != "2026-10-23" {
		t.Errorf("LastLoginDate = %q after a federated login, want 2026-10-23", u.LastLoginDate)
	}
}

// TestADisabledAccountsFederatedAttemptRecordsNothing: the provider vouching for a
// person is not a login here when the account is disabled, and must not make the
// account look in use.
func TestADisabledAccountsFederatedAttemptRecordsNothing(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	srv.flushLoginDates()
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("first login created no account")
	}
	u.Disabled = true
	u.LastLoginDate = "2026-01-01"
	var saveErr error
	srv.do(func() { saveErr = srv.users.Save(u) })
	if saveErr != nil {
		t.Fatalf("disable: %v", saveErr)
	}

	srv.sessions.now = func() time.Time { return lateEveningInZurich }
	second := federate(t, ts, idp, noRedirects(t))
	second.Body.Close()
	srv.flushLoginDates()
	if u, _ := accountBySubject(t, srv, "subject-1"); u.LastLoginDate != "2026-01-01" {
		t.Errorf("LastLoginDate = %q after a refused federated login, want it left at 2026-01-01", u.LastLoginDate)
	}
}
