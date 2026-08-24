package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/processdoc"
)

// What is left here is server *wiring* for the documentation area (ADR-0143).
// The area's own behavior — every handler branch, the store, the counter, the
// filename sanitizer — is tested against the service directly in api/processdoc,
// which is the point of carving it out (ADR-0147): those cases no longer need a
// whole server to reach. What a service test cannot show is that the collaborators
// the server supplies are really connected, so that is what this covers.

// TestPublicProcessDocRateLimited proves the anonymous route is throttled: past
// the burst an IP gets 429s rather than an unbounded scan of the token space.
func TestPublicProcessDocRateLimited(t *testing.T) {
	srv := newServerForErrors(t)
	srv.publicRate = newRateLimiter(1, 0)
	h := srv.Handler()
	limited := false
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, processdoc.PublicPath+"deadbeef", nil)
		req.RemoteAddr = "203.0.113.7:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("the public documentation route never rate-limited, want a 429 past the burst")
	}
}
