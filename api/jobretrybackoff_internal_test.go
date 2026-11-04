package api

import (
	"testing"
	"time"
)

// TestAServerBacksOffAFailedJobByDefault: a server built without options waits before it
// retries a failed in-process job, so a connector fault that clears by itself — Graph not
// yet knowing a group created a moment earlier — does not spend the task's retries within
// one second (ADR-0111, amendment 2026-11-04). The test helpers switch it off with
// WithJobRetryBackoff(nil), because their drives expect a failing task's incident at once;
// this test builds the server the way production does.
func TestAServerBacksOffAFailedJobByDefault(t *testing.T) {
	srv, err := newServerAtDir(t, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(srv.Close)
	if srv.jobRetryBackoff == nil {
		t.Fatal("no retry backoff installed: a failed connector job would be retried at once")
	}
	if got := time.Duration(srv.jobRetryBackoff(2)); got != 15*time.Second {
		t.Errorf("backoff with two retries left = %v, want 15s", got)
	}
	if got := time.Duration(srv.jobRetryBackoff(1)); got != time.Minute {
		t.Errorf("backoff before the last retry = %v, want 1m", got)
	}

	off, _ := newValidateServer(t)
	if off.jobRetryBackoff != nil {
		t.Error("the test helper still backs off; its drives would wait for a retry timer")
	}
}
