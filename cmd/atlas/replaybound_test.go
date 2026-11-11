package main

import (
	"strings"
	"testing"
)

// The line where the Operations replay stops loading a whole timeline can be
// lowered, never raised: the default is the ceiling the timeline's quadratic growth
// allows (ADR-0517). A start that asks for more, or for a negative line, stops with
// a reason rather than serving a line its operator did not set.
func TestServeRefusesAFullReplayLineOutsideItsRange(t *testing.T) {
	for _, v := range []string{"501", "-1"} {
		err := runServe([]string{"--data-dir", t.TempDir(), "--replay-full-max-iterations=" + v})
		if err == nil {
			t.Fatalf("--replay-full-max-iterations=%s started a server", v)
		}
		if !strings.Contains(err.Error(), "--replay-full-max-iterations") {
			t.Errorf("--replay-full-max-iterations=%s: error %q does not name the flag", v, err)
		}
	}
}
