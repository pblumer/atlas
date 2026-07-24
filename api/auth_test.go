package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := hashPassword("s3cret-pw")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if hash == "s3cret-pw" {
		t.Fatalf("password stored in clear")
	}
	if !checkPassword(hash, "s3cret-pw") {
		t.Fatalf("correct password rejected")
	}
	if checkPassword(hash, "wrong") {
		t.Fatalf("wrong password accepted")
	}
	if checkPassword("", "anything") {
		t.Fatalf("empty hash must never match")
	}
}

func TestRandomHexAndUserID(t *testing.T) {
	a, err := randomHex(16)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	b, _ := randomHex(16)
	if a == b {
		t.Fatalf("randomHex not random: %q", a)
	}
	if len(a) != 32 {
		t.Fatalf("randomHex(16) len = %d, want 32", len(a))
	}
	id, err := newUserID()
	if err != nil {
		t.Fatalf("newUserID: %v", err)
	}
	if len(id) < 5 || id[:4] != "usr_" {
		t.Fatalf("newUserID format: %q", id)
	}
}

func TestUserAndPrincipalHasRole(t *testing.T) {
	u := User{Roles: []string{"admin", "user"}}
	if !u.hasRole("admin") || u.hasRole("owner") {
		t.Fatalf("User.hasRole wrong")
	}
	p := &Principal{Roles: []string{"user"}}
	if p.hasRole("admin") || !p.hasRole("user") {
		t.Fatalf("Principal.hasRole wrong")
	}
}

func TestSessionStoreLifecycle(t *testing.T) {
	fixed := time.Unix(1000, 0)
	st := newSessionStore(time.Hour)
	st.now = func() time.Time { return fixed }

	tok, err := st.create(User{ID: "usr_1", Username: "alice", Roles: []string{"admin"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, ok := st.lookup(tok)
	if !ok || sess.userID != "usr_1" || sess.username != "alice" || len(sess.roles) != 1 {
		t.Fatalf("lookup: ok=%v sess=%+v", ok, sess)
	}
	if _, ok := st.lookup(""); ok {
		t.Fatalf("empty token must not resolve")
	}
	if _, ok := st.lookup("garbage"); ok {
		t.Fatalf("unknown token must not resolve")
	}
	st.destroy(tok)
	if _, ok := st.lookup(tok); ok {
		t.Fatalf("destroyed session still resolves")
	}
	st.destroy("") // no-op, must not panic
}

func TestSessionStoreExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	st := newSessionStore(time.Minute)
	st.now = func() time.Time { return now }
	tok, _ := st.create(User{ID: "usr_1"})
	now = now.Add(2 * time.Minute) // advance past the TTL
	if _, ok := st.lookup(tok); ok {
		t.Fatalf("expired session still resolves")
	}
}

func TestSessionStoreDestroyUser(t *testing.T) {
	st := newSessionStore(time.Hour)
	t1, _ := st.create(User{ID: "usr_1"})
	t2, _ := st.create(User{ID: "usr_1"})
	t3, _ := st.create(User{ID: "usr_2"})
	st.destroyUser("usr_1")
	if _, ok := st.lookup(t1); ok {
		t.Fatalf("session 1 for destroyed user still resolves")
	}
	if _, ok := st.lookup(t2); ok {
		t.Fatalf("session 2 for destroyed user still resolves")
	}
	if _, ok := st.lookup(t3); !ok {
		t.Fatalf("other user's session was destroyed")
	}
}

func TestRequiresAuth(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/index.html", false},
		{"/healthz", false},
		{"/api/v1/auth/login", false},
		{"/api/v1/info", false},
		{"/api/v1/openapi.json", false},
		{"/api/v1/users", true},
		{"/api/v1/auth/me", true},
		{"/api/v1/processes", true},
	}
	for _, c := range cases {
		if got := requiresAuth(c.path); got != c.want {
			t.Errorf("requiresAuth(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestPrincipalForAndWithAuthMiddleware(t *testing.T) {
	s := &Server{authEnabled: true, sessions: newSessionStore(time.Hour)}
	tok, _ := s.sessions.create(User{ID: "usr_1", Username: "alice", Roles: []string{"user"}})

	// A protected request with no cookie is rejected.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		if p == nil {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := s.withAuth(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/users", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-cookie protected request: got %d, want 401", rec.Code)
	}

	// With a valid cookie the principal is resolved and passed through.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid-cookie request: got %d, want 200", rec.Code)
	}

	// A public path passes through even without a session.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/info", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("public path should reach next handler unauthenticated: got %d", rec.Code)
	}
}

func TestWithAuthDisabledPassesThrough(t *testing.T) {
	s := &Server{authEnabled: false, sessions: newSessionStore(time.Hour)}
	reached := false
	h := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if principalFrom(r.Context()) != nil {
			t.Errorf("no principal expected when auth disabled")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/users", nil))
	if !reached {
		t.Fatalf("request blocked while auth disabled")
	}
}

func TestRequireAdmin(t *testing.T) {
	// Auth disabled: always allowed, nothing written.
	open := &Server{authEnabled: false}
	rec := httptest.NewRecorder()
	if !open.requireAdmin(rec, httptest.NewRequest("GET", "/api/v1/users", nil)) {
		t.Fatalf("auth disabled should allow")
	}

	s := &Server{authEnabled: true}
	// No principal -> 403.
	rec = httptest.NewRecorder()
	if s.requireAdmin(rec, httptest.NewRequest("GET", "/api/v1/users", nil)) {
		t.Fatalf("no principal should be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	// Non-admin principal -> 403.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req = req.WithContext(withPrincipal(req.Context(), &Principal{Roles: []string{"user"}}))
	if s.requireAdmin(rec, req) {
		t.Fatalf("non-admin should be denied")
	}
	// Admin principal -> allowed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/users", nil)
	req = req.WithContext(withPrincipal(req.Context(), &Principal{Roles: []string{"admin"}}))
	if !s.requireAdmin(rec, req) {
		t.Fatalf("admin should be allowed")
	}
}
