package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The presence rules, stated as tests before the Organization page shows any of
// it: what the three states mean, that a request refreshes the connection and
// only a person's own doing refreshes the activity, and that a session nobody has
// touched since it expired is gone rather than reported as somebody's presence.

func TestPresenceStateWindows(t *testing.T) {
	now := time.Unix(10_000, 0)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	cases := []struct {
		name       string
		lastSeen   time.Time
		lastActive time.Time
		want       string
	}{
		{"just logged in", now, now, presenceOnline},
		{"working a moment ago", ago(time.Minute), ago(time.Minute), presenceOnline},
		{"tab open, nobody touching it", ago(30 * time.Second), ago(20 * time.Minute), presenceIdle},
		{"exactly at the window is still active", now, ago(presenceWindow), presenceOnline},
		{"a second past it is not", now, ago(presenceWindow + time.Second), presenceIdle},
		{"browser gone, session not yet expired", ago(2 * time.Hour), ago(2 * time.Hour), presenceOffline},
		{"active once, then unreachable", ago(presenceWindow + time.Second), now, presenceOffline},
	}
	for _, c := range cases {
		if got := presenceState(c.lastSeen, c.lastActive, now); got != c.want {
			t.Errorf("%s: presenceState = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSessionStoreStampsAConnectionOnEveryLookup(t *testing.T) {
	now := time.Unix(1000, 0)
	st := newSessionStore(time.Hour)
	st.now = func() time.Time { return now }

	tok, err := st.create(User{ID: "usr_1", Username: "alice"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, _ := st.lookup(tok)
	if !sess.lastSeen.Equal(now) || !sess.lastActive.Equal(now) {
		t.Fatalf("a fresh login is both connected and active: %+v", sess)
	}

	// A request four minutes on keeps the session connected but says nothing about
	// whether anybody is at the keyboard — a poll is a request too.
	now = now.Add(4 * time.Minute)
	sess, _ = st.lookup(tok)
	if !sess.lastSeen.Equal(now) {
		t.Errorf("lookup did not refresh lastSeen: %v, want %v", sess.lastSeen, now)
	}
	if !sess.lastActive.Equal(time.Unix(1000, 0)) {
		t.Errorf("lookup moved lastActive; only the browser's own report may: %v", sess.lastActive)
	}

	// Six minutes after the login, the person is idle: still connected, nothing done.
	now = now.Add(2 * time.Minute)
	if got := st.presenceByUser()["usr_1"].State; got != presenceIdle {
		t.Errorf("state after six quiet minutes = %q, want %q", got, presenceIdle)
	}

	// Their own doing brings them back.
	if !st.touch(tok, true) {
		t.Fatal("touch on a live session returned false")
	}
	if got := st.presenceByUser()["usr_1"].State; got != presenceOnline {
		t.Errorf("state after activity = %q, want %q", got, presenceOnline)
	}
	if st.touch("garbage", true) {
		t.Error("touch on an unknown token returned true")
	}
	if st.touch("", true) {
		t.Error("touch on an empty token returned true")
	}
}

func TestPresenceByUserAggregatesEverySession(t *testing.T) {
	now := time.Unix(1000, 0)
	st := newSessionStore(time.Hour)
	st.now = func() time.Time { return now }

	desktop, _ := st.create(User{ID: "usr_1", Username: "alice"}, nil)
	_, _ = st.create(User{ID: "usr_2", Username: "bob"}, nil)

	// Alice's second browser signs in ten minutes later; the first is quiet by then.
	now = now.Add(10 * time.Minute)
	laptop, _ := st.create(User{ID: "usr_1", Username: "alice"}, nil)

	got := st.presenceByUser()
	alice, ok := got["usr_1"]
	if !ok {
		t.Fatal("alice has two sessions and no presence")
	}
	if alice.Sessions != 2 {
		t.Errorf("alice sessions = %d, want 2", alice.Sessions)
	}
	if alice.State != presenceOnline {
		t.Errorf("alice state = %q, want %q — the newest session decides", alice.State, presenceOnline)
	}
	if alice.LastActiveAt != now.Unix() {
		t.Errorf("alice lastActiveAt = %d, want %d", alice.LastActiveAt, now.Unix())
	}
	if alice.Username != "alice" {
		t.Errorf("alice username = %q", alice.Username)
	}
	if got["usr_2"].State != presenceOffline {
		t.Errorf("bob has not been seen for ten minutes: state = %q, want %q", got["usr_2"].State, presenceOffline)
	}

	// Both of Alice's sessions are hers, and either one of them being used is
	// enough — the presence is of the person, not of a browser.
	st.destroy(laptop)
	if got := st.presenceByUser()["usr_1"].Sessions; got != 1 {
		t.Errorf("sessions after closing one = %d, want 1", got)
	}
	st.destroy(desktop)
	if _, ok := st.presenceByUser()["usr_1"]; ok {
		t.Error("a user with no session at all should not be in the presence map")
	}
}

func TestPresenceDropsExpiredSessions(t *testing.T) {
	now := time.Unix(1000, 0)
	st := newSessionStore(time.Minute)
	st.now = func() time.Time { return now }
	tok, _ := st.create(User{ID: "usr_1"}, nil)

	now = now.Add(2 * time.Minute) // past the TTL
	if p, ok := st.presenceByUser()["usr_1"]; ok {
		t.Fatalf("an expired session was reported as presence: %+v", p)
	}
	if _, ok := st.lookup(tok); ok {
		t.Fatal("the expired session was not dropped")
	}
	st.mu.Lock()
	live := len(st.byToken)
	st.mu.Unlock()
	if live != 0 {
		t.Errorf("expired sessions left behind: %d", live)
	}
}

// TestPresenceBeaconDrivesTheStates walks one session through the three states the
// way a browser does: a heartbeat keeps it connected without claiming anybody is
// there, a beacon that claims activity brings it back, and silence takes it away.
func TestPresenceBeaconDrivesTheStates(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &Server{sessions: newSessionStore(time.Hour)}
	s.sessions.now = func() time.Time { return now }
	tok, err := s.sessions.create(User{ID: "usr_1", Username: "alice"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	beacon := func(body string, withCookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/presence", strings.NewReader(body))
		if withCookie {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		}
		w := httptest.NewRecorder()
		s.handlePresenceBeacon(w, r)
		return w
	}
	state := func() string { return s.sessions.presenceByUser()["usr_1"].State }

	// Ten quiet minutes with the tab open: the heartbeat lands every minute, so the
	// session is still connected — and idle, because a heartbeat claims nothing.
	for i := 0; i < 10; i++ {
		now = now.Add(time.Minute)
		if code := beacon(`{"active":false}`, true).Code; code != http.StatusOK {
			t.Fatalf("heartbeat: got %d", code)
		}
	}
	if got := state(); got != presenceIdle {
		t.Errorf("state after ten quiet minutes = %q, want %q", got, presenceIdle)
	}

	// Somebody touches the keyboard.
	now = now.Add(time.Minute)
	if code := beacon(`{"active":true}`, true).Code; code != http.StatusOK {
		t.Fatalf("active beacon: got %d", code)
	}
	if got := state(); got != presenceOnline {
		t.Errorf("state after activity = %q, want %q", got, presenceOnline)
	}

	// An empty body is a heartbeat too, and keeps the session connected.
	now = now.Add(time.Minute)
	if code := beacon("", true).Code; code != http.StatusOK {
		t.Fatalf("empty beacon: got %d", code)
	}
	if got := state(); got != presenceOnline {
		t.Errorf("state a minute after activity = %q, want %q", got, presenceOnline)
	}

	// Then the browser is closed: no heartbeat, and the session that outlives it
	// says offline rather than pretending somebody is there.
	now = now.Add(presenceWindow + time.Second)
	if got := state(); got != presenceOffline {
		t.Errorf("state after the browser stopped reporting = %q, want %q", got, presenceOffline)
	}

	// A beacon that carries no session — an API token's caller — is accepted and
	// stamps nothing, rather than being refused something it cannot supply.
	if code := beacon(`{"active":true}`, false).Code; code != http.StatusOK {
		t.Errorf("beacon without a session cookie: got %d, want 200", code)
	}
	if got := state(); got != presenceOffline {
		t.Errorf("a cookie-less beacon moved somebody's presence: %q", got)
	}
	if code := beacon(`{"active":`, true).Code; code != http.StatusBadRequest {
		t.Errorf("malformed beacon body: got %d, want 400", code)
	}
	// A body that cannot be read at all is the same refusal.
	unreadable := httptest.NewRequest(http.MethodPost, "/api/v1/auth/presence", errReader{})
	unreadable.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	s.handlePresenceBeacon(rec, unreadable)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable beacon body: got %d, want 400", rec.Code)
	}

	// And a beacon from a tab whose session has since expired stamps nothing
	// rather than resurrecting it.
	now = now.Add(24 * time.Hour)
	if s.sessions.touch(tok, true) {
		t.Error("touch on an expired session returned true")
	}
	if _, ok := s.sessions.presenceByUser()["usr_1"]; ok {
		t.Error("an expired session is still reported as presence")
	}
}
