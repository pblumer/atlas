package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/storage"
)

// TestTheStorageCardHasWordsForEveryState holds the seam the storage card is built
// across (ADR-0267): the server reports a state id, and the Console owns the words for
// it — a pill for every state, and a title, text and hint for each one the alert above
// the dashboard is raised for. A new state would otherwise reach an administrator as a
// raw `storage.state.…` key at exactly the moment the disk needs attention.
func TestTheStorageCardHasWordsForEveryState(t *testing.T) {
	module, err := os.ReadFile("web/storage.js")
	if err != nil {
		t.Fatalf("read the storage module: %v", err)
	}
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	states := []storage.State{storage.StateOK, storage.StateWarn, storage.StateFrozen, storage.StateHalted, storage.StateUnknown}
	m := regexp.MustCompile(`export const STATES = \[([^\]]*)\]`).FindSubmatch(module)
	if m == nil {
		t.Fatal("web/storage.js has no `export const STATES = [...]`; the test no longer matches the module")
	}
	for _, st := range states {
		if !strings.Contains(string(m[1]), `"`+string(st)+`"`) {
			t.Errorf("web/storage.js does not know the state %q the server reports", st)
		}
	}

	var want []string
	for _, st := range states {
		want = append(want, "storage.state."+string(st))
	}
	for _, st := range []storage.State{storage.StateWarn, storage.StateFrozen, storage.StateHalted} {
		want = append(want, "storage.alert."+string(st)+".title", "storage.alert."+string(st)+".text")
	}
	want = append(want, "storage.alert.frozen.hint", "storage.alert.halted.hint")
	src := string(catalogue)
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(src, locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for _, key := range want {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("locale %q has no message %q", locale, key)
			}
		}
	}
}

// TestEveryMessageTheStorageCardAsksForExists: every literal key the module looks up
// is in both locales, so a typo is found here rather than by an administrator reading
// a raw id on the dashboard.
func TestEveryMessageTheStorageCardAsksForExists(t *testing.T) {
	module, err := os.ReadFile("web/storage.js")
	if err != nil {
		t.Fatalf("read the storage module: %v", err)
	}
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bt\("(storage\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bplural\("(storage\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(module), -1) {
		keys[m[1]+".one"], keys[m[1]+".other"] = true, true
	}
	if len(keys) < 20 {
		t.Fatalf("found only %d message keys in web/storage.js; the scan no longer matches how the module looks them up", len(keys))
	}
	src := string(catalogue)
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(src, locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("web/storage.js asks for %q, which locale %q does not have", key, locale)
			}
		}
	}
}
