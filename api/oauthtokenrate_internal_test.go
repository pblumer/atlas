package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/limits"
)

// The token and revocation endpoints throttle on a bucket of their own (ADR-0510).
//
// They used to draw on the public forms' bucket, twenty at once and then one a
// second per address. That is right for a person at a public form and wrong for the
// caller these endpoints exist for: a confidential server-side client renewing all of
// its people's tokens from one address, all at once after its own restart. And the
// two shared one budget, so a burst of form traffic through the same proxy refused
// the client's renewals as well.

// oauthPost sends a form to one of the two endpoints from a fixed client address.
func oauthPost(t *testing.T, h http.Handler, path string, form url.Values) (int, map[string]string, http.Header) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "198.51.100.7:4711"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Header()
}

// TestTheTokenEndpointsDoNotShareThePublicFormsThrottle: spending either bucket
// leaves the other alone, and a spent token bucket still answers in the shape an
// OAuth client parses.
func TestTheTokenEndpointsDoNotShareThePublicFormsThrottle(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	badClient := url.Values{"client_id": {"nope"}, "client_secret": {"nope"}, "token": {"x"}, "grant_type": {"refresh_token"}}

	// The public forms' bucket spent: the forms are refused, the token endpoints are not.
	srv.publicRate = newRateLimiter(0, 0)
	r := httptest.NewRequest(http.MethodGet, "/public/forms/abcd", nil)
	r.RemoteAddr = "198.51.100.7:4711"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a public form under a spent bucket = %d, want 429 — the premise of this test", rec.Code)
	}
	for _, path := range []string{oauthTokenPath, oauthRevokePath} {
		if status, out, _ := oauthPost(t, h, path, badClient); status != http.StatusUnauthorized || out["error"] != "invalid_client" {
			t.Errorf("POST %s with the public forms' bucket spent = %d %v, want 401 invalid_client — "+
				"form traffic must not throttle a client's renewals", path, status, out)
		}
	}

	// The other way round: the token bucket spent refuses both endpoints with the
	// token endpoint's own answer, and the forms are untouched by it.
	srv.publicRate = newRateLimiter(20, 1)
	srv.tokenRate = newRateLimiter(0, 0)
	for _, path := range []string{oauthTokenPath, oauthRevokePath} {
		status, out, hdr := oauthPost(t, h, path, badClient)
		if status != http.StatusTooManyRequests || out["error"] != "temporarily_unavailable" || out["error_description"] == "" {
			t.Errorf("POST %s under a spent token bucket = %d %v, want 429 temporarily_unavailable", path, status, out)
		}
		if hdr.Get("Cache-Control") != "no-store" {
			t.Errorf("POST %s: the 429 is not no-store, unlike every other answer an OAuth client reads here", path)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code == http.StatusTooManyRequests {
		t.Error("spending the token endpoints' bucket throttled a public form")
	}
}

// TestTheTokenThrottleTakesARestartingClientsBurst: the default budget lets one
// address renew three hundred grants at once — a wiki with three hundred connected
// people coming back up — and refuses the next until time passes.
func TestTheTokenThrottleTakesARestartingClientsBurst(t *testing.T) {
	srv := newServerForErrors(t)
	frozen := time.Unix(1_800_000_000, 0)
	srv.tokenRate.now = func() time.Time { return frozen }
	h := srv.Handler()
	form := url.Values{"client_id": {"nope"}, "client_secret": {"nope"}, "grant_type": {"refresh_token"}}

	burst := int(limits.Default().TokenEndpointBurst)
	for i := 0; i < burst; i++ {
		if status, out, _ := oauthPost(t, h, oauthTokenPath, form); status == http.StatusTooManyRequests {
			t.Fatalf("renewal %d of a %d burst was throttled (%v)", i+1, burst, out)
		}
	}
	if status, _, _ := oauthPost(t, h, oauthTokenPath, form); status != http.StatusTooManyRequests {
		t.Errorf("renewal %d = %d, want 429 once the burst is spent", burst+1, status)
	}
	// A second later the address has earned back the budget's rate, and no more.
	frozen = frozen.Add(time.Second)
	rate := int(limits.Default().TokenEndpointRate)
	for i := 0; i < rate; i++ {
		if status, _, _ := oauthPost(t, h, oauthRevokePath, form); status == http.StatusTooManyRequests {
			t.Fatalf("request %d of the %d earned back in a second was throttled", i+1, rate)
		}
	}
	if status, _, _ := oauthPost(t, h, oauthRevokePath, form); status != http.StatusTooManyRequests {
		t.Errorf("request %d after one second = %d, want 429", rate+1, status)
	}
}

// TestTheTokenThrottleIsConfigured: the numbers are budgets, so an installation's
// ATLAS_LIMIT_TOKEN_ENDPOINT_* reach the bucket rather than the defaults shadowing them.
func TestTheTokenThrottleIsConfigured(t *testing.T) {
	l := limits.Default()
	l.TokenEndpointBurst, l.TokenEndpointRate = 7, 3
	srv := newServerWithOptions(t, WithLimits(l))
	if srv.tokenRate.capacity != 7 || srv.tokenRate.refill != 3 {
		t.Errorf("token bucket = %v at once, %v a second; want the configured 7 and 3",
			srv.tokenRate.capacity, srv.tokenRate.refill)
	}
}
