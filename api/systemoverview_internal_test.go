package api

import (
	"bytes"
	"os"
	"testing"
)

// The system overview is drawn once, in docs/architecture/system-overview.svg, where
// the architecture document shows it. The Console's welcome card, its help menu and the
// handbook show it too, and they can only serve what `//go:embed web` carries — so
// api/web holds a copy. A copy drifts: the diagram gains a layer in docs/ and every
// person opening the Console goes on reading the old one, with nothing to say so.
//
// This holds the copy to the original, byte for byte.
func TestTheConsoleShowsTheCurrentSystemOverview(t *testing.T) {
	const original = "../docs/architecture/system-overview.svg"
	want, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read %s: %v", original, err)
	}
	got, err := webFS.ReadFile("web/system-overview.svg")
	if err != nil {
		t.Fatalf("the Console serves no system overview: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("api/web/system-overview.svg is not %s, so the Console and the handbook "+
			"show an outdated diagram. The docs file is the one that is edited; copy it over:\n"+
			"\tcp docs/architecture/system-overview.svg api/web/system-overview.svg", original)
	}
}
