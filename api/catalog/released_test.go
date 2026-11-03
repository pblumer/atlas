package catalog

import (
	"net/http"
	"testing"
)

// TestKnownOutcomeNamesTheOutcomeConstants: an outcome a shop task states is one of
// the three constants, and nothing else reads as one.
func TestKnownOutcomeNamesTheOutcomeConstants(t *testing.T) {
	for _, o := range []string{OutcomeCompleted, OutcomeRejected, OutcomeFailed} {
		if !KnownOutcome(o) {
			t.Errorf("KnownOutcome(%q) = false, want true", o)
		}
	}
	for _, o := range []string{"", "done", "Completed"} {
		if KnownOutcome(o) {
			t.Errorf("KnownOutcome(%q) = true, want false", o)
		}
	}
}

// TestReleasedReportsWhetherACatalogueHasARelease: a catalogue that was only
// imported has no release; once an import publishes it, it has one.
func TestReleasedReportsWhetherACatalogueHasARelease(t *testing.T) {
	s := newService(t)
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	if ok, err := s.Released("cat-verwaltung"); err != nil || ok {
		t.Fatalf("Released before publishing = %v, %v; want false, nil", ok, err)
	}
	if code, out := importDoc(t, s, importer, withPublish(shopDocument)); code != http.StatusOK {
		t.Fatalf("publishing import = %d %v", code, out)
	}
	if ok, err := s.Released("cat-verwaltung"); err != nil || !ok {
		t.Fatalf("Released after publishing = %v, %v; want true, nil", ok, err)
	}
	if ok, err := s.Released("cat-unknown"); err != nil || ok {
		t.Fatalf("Released of an unknown catalogue = %v, %v; want false, nil", ok, err)
	}
}
