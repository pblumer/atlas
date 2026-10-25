package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/logging"
)

// Deleting finished instances is an opt-out
// (ADR-0486), as the catalogue purge is, so a
// production installation keeps its history only if somebody set
// --instance-purge=false. The start says so, as a WARN under a stable event name.

func instancePurgeStartLog(t *testing.T, on bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := logging.Setup(&buf, logging.DefaultFormat, logging.DefaultLevel); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel) })
	logInstancePurgeSwitch(on)
	return buf.String()
}

func TestAStartWithTheInstancePurgeOnWarnsAndSaysHowToSwitchItOff(t *testing.T) {
	out := instancePurgeStartLog(t, true)
	for _, want := range []string{"level=WARN", "event=server.instance_purge_enabled", "--instance-purge=false", "ATLAS_INSTANCE_PURGE=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("start log %q lacks %q", out, want)
		}
	}
}

func TestAStartWithTheInstancePurgeOffSaysNothingAboutIt(t *testing.T) {
	if out := instancePurgeStartLog(t, false); strings.Contains(out, "instance_purge") {
		t.Errorf("a server that switched the purge off warns about it: %q", out)
	}
}
