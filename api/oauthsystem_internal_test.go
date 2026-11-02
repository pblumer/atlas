package api

import (
	"slices"
	"testing"
)

// TestASystemAsksForItsCapabilitiesInTheScreensOrder: an empty scope asks for all
// three, a known subset comes back in the consent screen's order, and anything else is
// refused rather than dropped.
func TestASystemAsksForItsCapabilitiesInTheScreensOrder(t *testing.T) {
	if got, ok := systemScopesFrom(""); !ok || !slices.Equal(got, []string{"diagrams", "tasklist", "events"}) {
		t.Errorf("empty = %v %v", got, ok)
	}
	if got, ok := systemScopesFrom("events diagrams"); !ok || !slices.Equal(got, []string{"diagrams", "events"}) {
		t.Errorf("events diagrams = %v %v", got, ok)
	}
	if _, ok := systemScopesFrom("tasklist full"); ok {
		t.Error("full was accepted for a system grant")
	}
}

// TestAClientIsShownByItsHost: a client named by its document by the host of its
// client_id, one entered here by none.
func TestAClientIsShownByItsHost(t *testing.T) {
	for want, c := range map[string]oauthClient{
		"wiki.example.org": {ID: "https://wiki.example.org/api/engines/client-metadata", Metadata: true},
		"":                 {ID: "atlas-client-1"},
	} {
		if got := clientHostOf(c); got != want {
			t.Errorf("clientHostOf(%q) = %q, want %q", c.ID, got, want)
		}
	}
	if got := clientHostOf(oauthClient{ID: "https://bad host/%zz", Metadata: true}); got != "" {
		t.Errorf("an id that is no URL is shown by %q", got)
	}
}
