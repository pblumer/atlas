package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJSONWritesContentTypeAndStatus pins the response envelope every Atlas
// endpoint produces: the status the caller asked for, a charset-qualified JSON
// content type, and the value encoded as JSON.
func TestJSONWritesContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusCreated, map[string]any{"id": "abc", "n": 3})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body)
	}
	if got["id"] != "abc" {
		t.Errorf("body = %v", got)
	}
}

// TestErrorShape pins the error envelope clients parse: a JSON object with a
// single "error" key. Changing it is an API break, so it is asserted rather than
// left to the helper's implementation.
func TestErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusBadRequest, "bad request")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body)
	}
	if len(got) != 1 || got["error"] != "bad request" {
		t.Errorf("body = %v, want exactly {\"error\": \"bad request\"}", got)
	}
}

// TestJSONEncodeFailureStillSendsTheStatus covers a value that cannot be
// encoded. The status line is already written by then, so the caller's chosen
// status must still reach the client rather than the write panicking or
// silently becoming a 200.
func TestJSONEncodeFailureStillSendsTheStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusOK, make(chan int)) // channels do not marshal
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestClientIPStripsThePort is what the rate limiter buckets on: two requests
// from one host must share a bucket even though their source ports differ.
func TestClientIPStripsThePort(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"192.0.2.7:54321", "192.0.2.7"},
		{"192.0.2.7:1", "192.0.2.7"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"192.0.2.7", "192.0.2.7"}, // no port at all: used as-is
		{"", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		if got := ClientIP(r); got != tc.want {
			t.Errorf("ClientIP(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

// TestPrincipalRoundTrip covers the request-scoped identity handlers read: what
// the auth middleware puts in comes back out.
func TestPrincipalRoundTrip(t *testing.T) {
	want := &Principal{UserID: "usr_1", Username: "ada", Roles: []string{"admin"}}
	ctx := WithPrincipal(context.Background(), want)
	got := PrincipalFrom(ctx)
	if got != want {
		t.Fatalf("PrincipalFrom = %+v, want %+v", got, want)
	}
}

// TestPrincipalFromUnauthenticated proves an unauthenticated request reads as
// nil rather than as a zero-valued principal — a handler branches on nil, and a
// zero Principal would look like a real user with an empty name.
func TestPrincipalFromUnauthenticated(t *testing.T) {
	if p := PrincipalFrom(context.Background()); p != nil {
		t.Errorf("PrincipalFrom(empty ctx) = %+v, want nil", p)
	}
}

// TestPrincipalKeyIsPrivate proves the context key cannot be forged from
// outside this package: a value stored under a look-alike key from another
// package is not seen as a principal. Without a private key type, any package
// could inject an identity into a request context.
func TestPrincipalKeyIsPrivate(t *testing.T) {
	type lookalike struct{}
	ctx := context.WithValue(context.Background(), lookalike{}, &Principal{Username: "attacker"})
	if p := PrincipalFrom(ctx); p != nil {
		t.Errorf("a foreign context key resolved as a principal: %+v", p)
	}
}

// TestHasRole covers the membership check the authorization gates are built on,
// including the nil-principal case an unauthenticated request produces.
func TestHasRole(t *testing.T) {
	p := &Principal{Roles: []string{"editor", "admin"}}
	if !p.HasRole("admin") {
		t.Error("HasRole(admin) = false, want true")
	}
	if p.HasRole("owner") {
		t.Error("HasRole(owner) = true, want false")
	}
	if (&Principal{}).HasRole("admin") {
		t.Error("a principal with no roles reported admin")
	}
}

// TestInGroupAnswersMembership covers the group check every group-scoped
// authorization gate calls. It fails closed on a principal with no groups, which is
// the property that matters: a request whose identity carries nothing must not pass
// a check for something.
func TestInGroupAnswersMembership(t *testing.T) {
	p := &Principal{GroupIDs: []string{"identity", "finance"}}
	if !p.InGroup("identity") || !p.InGroup("finance") {
		t.Errorf("a member was not recognised: groups = %v", p.GroupIDs)
	}
	if p.InGroup("operations") {
		t.Error("a non-member passed the group check")
	}
	// Exact match, not a prefix or a fold: "ident" is a different group, and so is
	// "Identity" — a group id is an id.
	if p.InGroup("ident") || p.InGroup("Identity") || p.InGroup("") {
		t.Error("the group check matched something that is not the group")
	}
	if (&Principal{}).InGroup("identity") {
		t.Error("a principal with no groups passed a group check; the gate must fail closed")
	}
}
