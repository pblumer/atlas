package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/capability"
)

// TestTheBusinessArchitectureScreenHasWordsForEverythingTheServerSays holds the seam
// the capability screens are built across (ADR-0305, roadmap B8): the server publishes
// finding kinds, capability states and realization kinds as ids in its authoring
// subset, and the Console owns the words (ADR-0267).
//
// Adding a finding kind to the gap report is a few lines in Go, and the landing view
// would then title a card `ba.finding.stage.orphaned.title` — visibly broken by design,
// but only to whoever has that finding. This makes it fail in CI instead.
func TestTheBusinessArchitectureScreenHasWordsForEverythingTheServerSays(t *testing.T) {
	sub := capability.Subset()
	var want []string
	for _, kind := range sub.FindingKinds {
		want = append(want, "ba.finding."+kind+".title", "ba.finding."+kind+".hint")
	}
	for _, state := range sub.States {
		want = append(want, "ba.state."+state)
	}
	for _, kind := range sub.RealizationKinds {
		want = append(want, "ba.realization."+kind)
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for _, key := range want {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("locale %q has no message %q", locale, key)
			}
		}
	}
}

// TestEveryMessageTheCapabilityScreenAsksForExists is the other half: every literal key
// the module looks up is in both locales. A typo in a key is otherwise found by
// somebody reading a raw id on screen, in whichever locale they happen to use.
func TestEveryMessageTheCapabilityScreenAsksForExists(t *testing.T) {
	module, err := os.ReadFile("web/capabilities.js")
	if err != nil {
		t.Fatalf("read the capability module: %v", err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\b(?:t|tx)\("(ba\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bplural\("(ba\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]+".one"], keys[m[1]+".other"] = true, true
	}
	// A key chosen by a ternary is written as two literals inside one call; both arms
	// are lookups and both must exist.
	for _, m := range regexp.MustCompile(`\? "(ba\.[A-Za-z0-9.-]+)" : "(ba\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]], keys[m[2]] = true, true
	}
	if len(keys) < 50 {
		t.Fatalf("found only %d message keys in web/capabilities.js; the scan no longer matches how the module looks them up", len(keys))
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("web/capabilities.js asks for %q, which locale %q does not have", key, locale)
			}
		}
	}
}
