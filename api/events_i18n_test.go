package api

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
)

// TestTheEventsPageHasWordsForEverythingTheServerSays holds the seam the Events page
// is built across (ADR-0267): the catalogue and the inventories send ids — an
// event's kind and stability, a channel, an element's role, a finding's rule — and the
// Console owns the words for them. The page looks each up by its id, so a new kind or
// rule would otherwise reach the screen as a raw `events.kind.…` key, visibly broken
// by design but only to whoever has that event. This makes it fail in CI instead.
func TestTheEventsPageHasWordsForEverythingTheServerSays(t *testing.T) {
	var want []string
	for _, k := range []eventcatalog.Kind{eventcatalog.Domain, eventcatalog.Platform} {
		want = append(want, "events.kind."+string(k))
	}
	for _, s := range []eventcatalog.Stability{eventcatalog.Stable, eventcatalog.Experimental} {
		want = append(want, "events.stability."+string(s))
	}
	for _, c := range []eventcatalog.Channel{eventcatalog.Signal, eventcatalog.Message, eventcatalog.Feed, eventcatalog.Log} {
		want = append(want, "events.channel."+string(c)+".what")
	}
	for _, r := range []compiler.SignalRole{compiler.SignalThrows, compiler.SignalStarts, compiler.SignalCatches,
		compiler.SignalBoundary, compiler.SignalEventSubProcess} {
		want = append(want, "events.role."+string(r))
	}
	for _, r := range []compiler.MessageRole{compiler.MessageSends, compiler.MessageStarts, compiler.MessageCatches,
		compiler.MessageBoundary, compiler.MessageEventSubProcess} {
		if !slices.Contains(want, "events.role."+string(r)) { // the message roles' words are the signal roles'
			want = append(want, "events.role."+string(r))
		}
	}
	for _, rule := range []string{findingSignalUnreceived, findingSignalUnthrown, findingSignalCrossesProjects,
		findingSignalReservedName, findingMessageUnreceived, findingMessageCrossesProjects, findingMessageReservedName} {
		want = append(want, "events.finding."+rule)
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

// TestEveryMessageTheEventsPageAsksForExists is the other half: every literal key the
// page looks up is in both locales, so a typo in a key is not first found by somebody
// reading a raw id on screen in whichever locale they happen to use.
func TestEveryMessageTheEventsPageAsksForExists(t *testing.T) {
	module, err := os.ReadFile("web/events.js")
	if err != nil {
		t.Fatalf("read the events module: %v", err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\b(?:t|tx)\("((?:events|common)\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bplural\("(events\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]+".one"], keys[m[1]+".other"] = true, true
	}
	// A key chosen by a ternary is written as two literals inside one call; both arms
	// are lookups and both must exist.
	for _, m := range regexp.MustCompile(`\? "(events\.[A-Za-z0-9.-]+)" : "(events\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]], keys[m[2]] = true, true
	}
	if len(keys) < 80 {
		t.Fatalf("found only %d message keys in web/events.js; the scan no longer matches how the module looks them up", len(keys))
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("web/events.js asks for %q, which locale %q does not have", key, locale)
			}
		}
	}
}
