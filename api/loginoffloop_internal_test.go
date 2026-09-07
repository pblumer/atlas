package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestLoginDoesNotWaitForTheRunLoop is the property this change exists for.
//
// Accounts are a durable sidecar, not engine state (ADR-0044): nothing about
// signing in needs the single writer. Both of the login's lookups were dispatched
// onto it anyway, which made the front door only as available as the engine — on a
// server carrying ~50k active instances the loop was busy enough that
// POST /api/v1/auth/login stopped answering, and nobody could get in to do
// anything about it (ADR-draft-login-off-the-run-loop).
//
// The loop is held for the whole request here, exactly as a saturated processor
// holds it. On the intended path the login never asks for it and answers straight
// away; the deadline is a failure guard, not a timing assumption.
func TestLoginDoesNotWaitForTheRunLoop(t *testing.T) {
	srv, _ := newOffLoopServer(t)

	const password = "correct-horse-battery"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if err := srv.users.Save(User{
		ID:           "u-ada",
		Username:     "ada",
		Source:       SourceLocal,
		Roles:        []string{RoleAdmin},
		PasswordHash: hash,
		CreatedAt:    1,
	}); err != nil {
		t.Fatalf("save user: %v", err)
	}

	// Take the single writer and keep it, the way a batch of engine work does.
	held, release := make(chan struct{}), make(chan struct{})
	go srv.do(func() { close(held); <-release })
	<-held
	defer close(release)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"ada","password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { defer close(done); srv.handleLogin(w, req) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the login was still waiting while the run loop was held: it is back on the loop")
	}

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	// A login that answers but hands out no session has not let anybody in.
	if len(w.Result().Cookies()) == 0 {
		t.Error("the login succeeded but set no session cookie")
	}
}

// TestLoginStillRefusesAWrongPasswordOffTheLoop guards the obvious way to make the
// test above pass: moving the lookup off the loop must not have moved the check
// with it. The engine is held here too, so the refusal is decided on the same path
// as the success.
func TestLoginStillRefusesAWrongPasswordOffTheLoop(t *testing.T) {
	srv, _ := newOffLoopServer(t)

	hash, err := hashPassword("the-real-one")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if err := srv.users.Save(User{
		ID: "u-ada", Username: "ada", Source: SourceLocal,
		Roles: []string{RoleUser}, PasswordHash: hash, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("save user: %v", err)
	}

	held, release := make(chan struct{}), make(chan struct{})
	go srv.do(func() { close(held); <-release })
	<-held
	defer close(release)

	for _, tc := range []struct{ name, body string }{
		{"wrong password", `{"username":"ada","password":"guess"}`},
		{"no such account", `{"username":"nobody","password":"the-real-one"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			done := make(chan struct{})
			go func() { defer close(done); srv.handleLogin(w, req) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the login was still waiting while the run loop was held")
			}
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Error("a refused login handed out a session cookie")
			}
		})
	}
}
