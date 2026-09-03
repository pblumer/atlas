package nettimeout_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// TestDefaultBudget pins the shared worker call budget. It is a deliberate
// product decision, not an incidental constant: it caps how long a hung external
// host can hold the run-loop goroutine, so a change here changes the worst-case
// stall of the whole engine.
func TestDefaultBudget(t *testing.T) {
	if nettimeout.Default != 10*time.Second {
		t.Fatalf("Default = %v, want 10s", nettimeout.Default)
	}
}

// TestHTTPClientIsBounded is the point of the package: the client it hands out
// must carry a non-zero timeout. A zero Timeout is Go's "wait forever", which is
// exactly the hazard (http.DefaultClient) this replaces.
func TestHTTPClientIsBounded(t *testing.T) {
	c := nettimeout.HTTPClient()
	if c.Timeout == 0 {
		t.Fatal("HTTPClient has no timeout; an unbounded worker call can stall the run loop")
	}
	if c.Timeout != nettimeout.Default {
		t.Fatalf("HTTPClient timeout = %v, want the shared budget %v", c.Timeout, nettimeout.Default)
	}
}

// TestHTTPClientIsNotShared: each call returns its own client, so a caller that
// adjusts one (e.g. a future per-connector timeout) cannot retune every other
// worker by accident.
func TestHTTPClientIsNotShared(t *testing.T) {
	if a, b := nettimeout.HTTPClient(), nettimeout.HTTPClient(); a == b {
		t.Fatal("HTTPClient returned the same client twice; callers could mutate each other's timeout")
	}
}
