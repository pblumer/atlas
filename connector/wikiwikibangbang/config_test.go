package wikiwikibangbang

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseBundle(t *testing.T) {
	b, err := ParseBundle(`{"token":"wwbb_abc"}`)
	if err != nil || b.Token != "wwbb_abc" {
		t.Fatalf("ParseBundle = %+v, %v", b, err)
	}
	for raw, why := range map[string]string{
		``:                                   "empty",
		`  `:                                 "blank",
		`{"token":"wwbb_abc","user":"x"}`:    "an unknown field",
		`{"token":""}`:                       "an empty token",
		`{"token":"  "}`:                     "a blank token",
		`["wwbb_abc"]`:                       "a list",
		`{"token":"wwbb_abc"`:                "broken JSON",
		`{"token":"wwbb_abc"} {"token":"x"}`: "two values",
	} {
		_, err := ParseBundle(raw)
		if err == nil {
			t.Errorf("ParseBundle accepted %s: %q", why, raw)
			continue
		}
		if strings.Contains(err.Error(), "wwbb_abc") {
			t.Errorf("ParseBundle quoted the token in %q", err)
		}
	}
}

func TestNewClientChecksTheEndpoint(t *testing.T) {
	const bundle = `{"token":"wwbb_abc"}`
	for _, ok := range []string{"https://wiki.example.ch", "https://wiki.example.ch/", "https://example.ch/wiki/", "http://127.0.0.1:8080", "http://localhost:8080"} {
		if _, err := NewClient(ok, bundle, Options{}); err != nil {
			t.Errorf("NewClient(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "wiki.example.ch", "http://wiki.example.ch", "ftp://wiki.example.ch", "https://user:pw@wiki.example.ch", "https://"} {
		if _, err := NewClient(bad, bundle, Options{}); err == nil {
			t.Errorf("NewClient(%q) accepted an endpoint that is no https URL", bad)
		}
	}
	if err := CheckBundle("https://wiki.example.ch", `{"token":""}`); err == nil {
		t.Error("CheckBundle accepted an empty token")
	}
	if err := CheckBundle("https://wiki.example.ch", bundle); err != nil {
		t.Errorf("CheckBundle = %v", err)
	}
}

// The endpoint's path is kept: a wiki behind a reverse proxy under /wiki is reached
// there, and a query or fragment is dropped.
func TestTheEndpointPathIsKept(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte(`{"admin":false,"erasure":false,"scope":"full","user":{"id":"u1","username":"bot"}}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL+"/wiki/?x=1#frag", `{"token":"t"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Standing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "/wiki/api/me" {
		t.Errorf("path = %q, want /wiki/api/me", got)
	}
}

// An account holding a system role is refused before any operation: an administrator
// is not bound by the wiki's restrictions, and nothing this Worker Type does needs one.
func TestAnAccountWithASystemRoleIsRefused(t *testing.T) {
	for _, role := range []string{"admin", "erasure"} {
		t.Run(role, func(t *testing.T) {
			f := newFakeWiki(t)
			f.me.Admin, f.me.Erasure = role == "admin", role == "erasure"
			f.add("OPS", "page", "Runbook", nil, nil)
			_, err := Run(context.Background(), Job{Connector: "wiki", Operation: "get-item", Item: "OPS-1", ResultVariable: "page"}, registryOf(f.client(t)))
			if err == nil || !strings.Contains(err.Error(), role) {
				t.Fatalf("err = %v, want the %s role refused", err, role)
			}
			if f.count("GET /api/items/OPS-1") != 0 {
				t.Error("the item was read with an account that should have been refused")
			}
		})
	}
}

// The standing is read once and then kept for a while: it is one request, not one per
// operation.
func TestTheStandingIsKept(t *testing.T) {
	f := newFakeWiki(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c, err := NewClient(f.srv.URL, `{"token":"`+f.token+`"}`, Options{HTTPClient: f.srv.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Standing(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("GET /api/me"); n != 1 {
		t.Errorf("GET /api/me = %d, want 1", n)
	}
	now = now.Add(standingFor + time.Second)
	f.me.Admin = true
	if _, err := c.Standing(context.Background()); err == nil {
		t.Error("an account made administrator was not refused once the standing was read again")
	}
}

// A redirect is followed only to the same host, and only for a read: a write redirected
// would arrive as a GET, and a token presented to another host is a token given away.
func TestRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the token was sent to another host")
		}
		w.WriteHeader(200)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"admin":false,"erasure":false,"scope":"full","user":{"id":"u1","username":"bot"}}`))
		case "/api/items/AWAY":
			http.Redirect(w, r, other.URL+"/api/items/AWAY", http.StatusFound)
		case "/api/items/MOVED":
			http.Redirect(w, r, "/api/items/HERE", http.StatusMovedPermanently)
		case "/api/items/HERE":
			_, _ = w.Write([]byte(`{"item":{"id":"i1","key":"HERE","title":"t","type":"page","space":"OPS","version":1,"properties":{},"labels":[],"body":[]}}`))
		case "/api/items/HERE/title":
			http.Redirect(w, r, "/api/items/ELSEWHERE/title", http.StatusFound)
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"t"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	reg := registryOf(c)
	if _, err := Run(context.Background(), Job{Connector: "wiki", Operation: "get-item", Item: "MOVED", ResultVariable: "x"}, reg); err != nil {
		t.Errorf("a same-host redirect of a read was not followed: %v", err)
	}
	if _, err := Run(context.Background(), Job{Connector: "wiki", Operation: "get-item", Item: "AWAY", ResultVariable: "x"}, reg); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("a redirect to another host was followed: %v", err)
	}
	if _, err := Run(context.Background(), Job{Connector: "wiki", Operation: "update-item", Item: "HERE", Title: "new"}, reg); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("a redirected write was followed: %v", err)
	}
}

// The token appears in no error, whatever the wiki answers.
func TestTheTokenIsInNoError(t *testing.T) {
	const token = "wwbb_secret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"token ` + token + ` is not known"}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"`+token+`"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Standing(context.Background())
	if err == nil {
		t.Fatal("a 401 was not an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error quotes the token: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("err = %#v, want an APIError with status 401", err)
	}
}

// An answer above the payload budget is refused rather than read short.
func TestAnAnswerAboveTheBudgetIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			_, _ = w.Write([]byte(`{"admin":false,"erasure":false,"scope":"full","user":{"id":"u1","username":"bot"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"t"}`, Options{HTTPClient: srv.Client(), MaxAnswer: 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), Job{Connector: "wiki", Operation: "read-page", Item: "OPS-1", ResultVariable: "md"}, registryOf(c))
	if err == nil || !strings.Contains(err.Error(), "payload budget") {
		t.Errorf("err = %v, want the answer refused above the budget", err)
	}
}

func registryOf(c *Client) *Registry {
	reg := NewRegistry()
	reg.Register("wiki", c)
	return reg
}
