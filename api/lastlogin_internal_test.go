package api

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The day an account last signed in (ADR-0477): what the roster
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

// saveAda writes the ordinary account the tests sign in as.
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

// recordedDay reads an account's login day as the store holds it.
func recordedDay(t *testing.T, srv *Server, id string) string {
	t.Helper()
	day, err := srv.lastLogins.get(id)
	if err != nil {
		t.Fatalf("read login day of %s: %v", id, err)
	}
	return day
}

// rosterDays reads the administrator's roster and returns user id → lastLoginDate.
func rosterDays(t *testing.T, ts *httptest.Server) map[string]string {
	t.Helper()
	code, admin := signIn(t, ts, "root", "rootpassword12")
	if code != http.StatusOK {
		t.Fatalf("root login = %d, want 200", code)
	}
	resp, err := admin.Get(ts.URL + "/api/v1/users")
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("roster = %d, want 200", resp.StatusCode)
	}
	var roster []struct {
		ID            string `json:"id"`
		LastLoginDate string `json:"lastLoginDate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&roster); err != nil {
		t.Fatalf("decode roster: %v", err)
	}
	out := map[string]string{}
	for _, r := range roster {
		out[r.ID] = r.LastLoginDate
	}
	return out
}

// TestALoginRecordsTheDayBeforeItAnswers is the feature: once the login has
// answered, its day is on disk and the roster says it — nothing is left for later
// that a restore, an edit or a check could run into.
func TestALoginRecordsTheDayBeforeItAnswers(t *testing.T) {
	srv, ts := loginDayServer(t)

	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("ada login = %d, want 200", code)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-23" {
		t.Errorf("recorded day = %q, want 2026-10-23 — the UTC day of 00:30 in Zurich on the 24th", got)
	}
	if got := rosterDays(t, ts)["u-ada"]; got != "2026-10-23" {
		t.Errorf("roster lastLoginDate for ada = %q, want 2026-10-23", got)
	}
}

// TestALoginLeavesTheAccountRecordAlone: the day is beside the account, not on it.
// Signing in is using an account, not changing it, and a login that rewrote the
// record would race every other writer of it.
func TestALoginLeavesTheAccountRecordAlone(t *testing.T) {
	srv, ts := loginDayServer(t)
	before, err := os.ReadFile(srv.users.FileFor("u-ada"))
	if err != nil {
		t.Fatalf("read account: %v", err)
	}

	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("login = %d, want 200", code)
	}
	after, err := os.ReadFile(srv.users.FileFor("u-ada"))
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the login rewrote the account record:\nbefore %s\nafter  %s", before, after)
	}
}

// TestARefusedLoginRecordsNothing: a wrong password is not somebody using the
// account, and a day written for it would make a guessed-at account look alive.
func TestARefusedLoginRecordsNothing(t *testing.T) {
	srv, ts := loginDayServer(t)

	if code, _ := signIn(t, ts, "ada", "not-her-password"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", code)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "" {
		t.Errorf("recorded day = %q after a refused login, want none", got)
	}
}

// TestRecordingTheDayDoesNotNeedTheRunLoop: the login does not wait for the loop
// (ADR-0265), and neither does the day it records — it is on disk while the loop is
// still held.
func TestRecordingTheDayDoesNotNeedTheRunLoop(t *testing.T) {
	srv, ts := loginDayServer(t)

	held, release := make(chan struct{}), make(chan struct{})
	go srv.do(func() { close(held); <-release })
	<-held
	defer close(release)

	done := make(chan int, 1)
	go func() { code, _ := signIn(t, ts, "ada", "ada-password"); done <- code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("login = %d, want 200", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the login waited for the run loop")
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-23" {
		t.Errorf("recorded day while the loop is held = %q, want 2026-10-23", got)
	}
}

// TestALoginDayNeverMovesBack: the stored day is the latest one, whichever write
// arrives last. Two logins either side of midnight, or a clock corrected backwards,
// must not make an account look less recently used than it is.
func TestALoginDayNeverMovesBack(t *testing.T) {
	srv, _ := loginDayServer(t)

	for _, day := range []string{"2026-10-23", "2026-10-20", "2026-10-23"} {
		if err := srv.lastLogins.record("u-ada", day); err != nil {
			t.Fatalf("record %s: %v", day, err)
		}
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-23" {
		t.Errorf("recorded day after an older write = %q, want 2026-10-23", got)
	}
	if err := srv.lastLogins.record("u-ada", "2026-10-24"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-24" {
		t.Errorf("recorded day after a later write = %q, want 2026-10-24", got)
	}
}

// TestTwoBrowsersSigningInAtOnceLeaveOneWholeRecord: the same person in two tabs is
// two concurrent writes of one key, and the record they leave must still be one that
// reads.
func TestTwoBrowsersSigningInAtOnceLeaveOneWholeRecord(t *testing.T) {
	srv, _ := loginDayServer(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Alternate between two days so that both the skip and the write are taken.
			day := "2026-10-23"
			if i%2 == 0 {
				day = "2026-10-24"
			}
			if err := srv.lastLogins.record("u-ada", day); err != nil {
				t.Errorf("record: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-24" {
		t.Errorf("recorded day = %q, want the later 2026-10-24", got)
	}
}

// TestADeletedAccountKeepsNoDay: deleting an account deletes its login day with it,
// and a login that read the account before the delete writes nothing after it.
func TestADeletedAccountKeepsNoDay(t *testing.T) {
	srv, ts := loginDayServer(t)
	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("login = %d, want 200", code)
	}

	code, admin := signIn(t, ts, "root", "rootpassword12")
	if code != http.StatusOK {
		t.Fatalf("root login = %d, want 200", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/users/u-ada", nil)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
	if _, err := os.Stat(srv.lastLogins.store.FileFor("u-ada")); !os.IsNotExist(err) {
		t.Errorf("the deleted account's login day is still on disk (stat err %v)", err)
	}

	// The login that read ada just before the delete now gets to write its day.
	if err := srv.lastLogins.record("u-ada", "2026-10-25"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "" {
		t.Errorf("a login racing the delete recorded %q for an account that is gone", got)
	}
}

// TestADayLeftBehindIsPrunedAtStartup: whatever left a day for an account that no
// longer exists, the next start removes it, and keeps the days of accounts that do.
func TestADayLeftBehindIsPrunedAtStartup(t *testing.T) {
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
	saveAda(t, srv)
	for _, rec := range []lastLogin{{UserID: "u-ada", Date: "2026-10-23"}, {UserID: "u-gone", Date: "2026-03-01"}} {
		if err := srv.lastLogins.store.Save(rec); err != nil {
			t.Fatalf("save %s: %v", rec.UserID, err)
		}
	}
	stop()

	srv, stop = open()
	defer stop()
	if got := recordedDay(t, srv, "u-gone"); got != "" {
		t.Errorf("an account that does not exist still has the day %q after a restart", got)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-23" {
		t.Errorf("ada's day after a restart = %q, want 2026-10-23 — pruning took a live account's day", got)
	}
}

// TestAnUnreadableDayDoesNotTakeTheRosterDown: the roster is the page an
// administrator fixes things from. One login day that cannot be read leaves that row
// without one; every other row, and the page, stay.
func TestAnUnreadableDayDoesNotTakeTheRosterDown(t *testing.T) {
	srv, ts := loginDayServer(t)
	if err := os.WriteFile(srv.lastLogins.store.FileFor("u-ada"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}

	days := rosterDays(t, ts) // root signs in here, so root has a day
	if days["u-ada"] != "" {
		t.Errorf("ada's unreadable day rendered as %q, want none", days["u-ada"])
	}
	rootHasDay := false
	for id, day := range days {
		if id != "u-ada" && day == "2026-10-23" {
			rootHasDay = true
		}
	}
	if !rootHasDay {
		t.Errorf("roster days = %v, want the administrator's own login beside ada's unreadable one", days)
	}

	// And the next login replaces the torn record rather than failing on it.
	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("login = %d, want 200", code)
	}
	if got := recordedDay(t, srv, "u-ada"); got != "2026-10-23" {
		t.Errorf("recorded day after a login over a torn record = %q, want 2026-10-23", got)
	}
}

// TestAFailedWriteDoesNotFailTheLogin: the person is signed in whether or not the
// day could be written, and the next login writes it again.
func TestAFailedWriteDoesNotFailTheLogin(t *testing.T) {
	srv, ts := loginDayServer(t)
	dir := srv.lastLogins.store.Dir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove store: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("block store: %v", err)
	}

	if code, _ := signIn(t, ts, "ada", "ada-password"); code != http.StatusOK {
		t.Fatalf("login with an unwritable login-day store = %d, want 200", code)
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
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("the federated login created no account")
	}
	if got := recordedDay(t, srv, u.ID); got != "2026-10-23" {
		t.Errorf("recorded day after a federated login = %q, want 2026-10-23", got)
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
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("first login created no account")
	}
	u.Disabled = true
	var saveErr error
	srv.do(func() { saveErr = srv.users.Save(u) })
	if saveErr != nil {
		t.Fatalf("disable: %v", saveErr)
	}
	if err := srv.lastLogins.forget(u.ID); err != nil {
		t.Fatalf("clear day: %v", err)
	}

	second := federate(t, ts, idp, noRedirects(t))
	second.Body.Close()
	if got := recordedDay(t, srv, u.ID); got != "" {
		t.Errorf("recorded day = %q after a refused federated login, want none", got)
	}
}
