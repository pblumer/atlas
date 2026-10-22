package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/logging"
)

// The purge is an opt-out (ADR-0469), so a production installation
// keeps its orders and held rights only if somebody set --catalogue-purge=false. A
// server that was upgraded without it says nothing is wrong — it serves, and every
// administrator can delete. The start says so instead, as a WARN under a stable event
// name an operator can alert on.

// startLog captures what logCatalogueSwitches writes.
func startLog(t *testing.T, catalogue, cataloguePurge bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := logging.Setup(&buf, logging.DefaultFormat, logging.DefaultLevel); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel) })
	logCatalogueSwitches(catalogue, cataloguePurge)
	return buf.String()
}

func TestAStartWithThePurgeOnWarnsAndSaysHowToSwitchItOff(t *testing.T) {
	out := startLog(t, true, true)
	for _, want := range []string{"level=WARN", "event=server.catalogue_purge_enabled", "--catalogue-purge=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("start log %q lacks %q", out, want)
		}
	}
}

func TestAStartWithThePurgeOffSaysNothingAboutIt(t *testing.T) {
	if out := startLog(t, true, false); strings.Contains(out, "catalogue_purge") {
		t.Errorf("a server that switched the purge off warns about it: %q", out)
	}
}

// With the catalogue switched off, its routes are not served and nothing can be
// purged, so the warning would be about a door that is not there.
func TestAStartWithoutTheCatalogueDoesNotWarnAboutItsPurge(t *testing.T) {
	out := startLog(t, false, true)
	if strings.Contains(out, "catalogue_purge") {
		t.Errorf("a server without the catalogue warns about its purge: %q", out)
	}
	if !strings.Contains(out, "event=server.catalogue_disabled") {
		t.Errorf("the catalogue switched off is no longer said at start: %q", out)
	}
}
