//go:build !windows

package workercmd

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Without --once a worker works until it is told to stop, and a stop is an ordinary
// exit: it finishes what it holds and returns nil.
func TestRunWorksUntilTerminated(t *testing.T) {
	stub, ts := newAtlasStub(t)
	done := make(chan error, 1)
	go func() {
		done <- Run([]string{"--server", ts.URL, "--connector", "ldif", "--wait", "10ms"})
	}()
	deadline := time.Now().Add(20 * time.Second)
	for len(stub.to("/api/v1/jobs/activate")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the worker never polled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after SIGTERM = %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}
}
