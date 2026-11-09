package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/startgrant"
)

// The Console's start-grant page (web/startgrants.js, MFE1A) says every blocker and
// account issue the server answers in words, looking each up as a key built from the
// code. A code the server adds without a message would show as an id on the one page
// meant to explain it, so every code the startgrant package declares is held to every
// locale here, together with every key the page names literally.

func TestEveryMessageTheStartGrantPageAsksForExists(t *testing.T) {
	src, err := os.ReadFile("web/startgrants.js")
	if err != nil {
		t.Fatalf("read the start-grant page: %v", err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(startgrants\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(string(src), -1) {
		keys[m[1]] = true
	}
	if len(keys) < 40 {
		t.Fatalf("found only %d message keys in web/startgrants.js; the scan no longer matches the module", len(keys))
	}
	families := map[string][]string{
		"startgrants.blocker.": startgrant.AllBlockers,
		"startgrants.issue.":   startgrant.AllAccountIssues,
	}
	for prefix, codes := range families {
		if !keys[prefix] {
			t.Fatalf("web/startgrants.js no longer builds %q + code; update this test with it", prefix)
		}
		delete(keys, prefix)
		for _, code := range codes {
			keys[prefix+code] = true
		}
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("web/startgrants.js asks for %q, which locale %q does not have", key, locale)
			}
		}
		if locale == "de" {
			for _, line := range strings.Split(block, "\n") {
				if strings.Contains(line, `"startgrants.`) && strings.Contains(line, "ß") {
					t.Errorf("Swiss spelling writes ss, never ß: %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestTheStartGrantPageSendsIDsAndNeverRetriesAWrite pins two rules a reviewer would
// otherwise have to re-read the module for: a grant is written with the ids the
// directory answered, and a write that lost a race (409) reloads and asks the
// administrator again instead of sending itself a second time.
func TestTheStartGrantPageSendsIDsAndNeverRetriesAWrite(t *testing.T) {
	src, err := os.ReadFile("web/startgrants.js")
	if err != nil {
		t.Fatal(err)
	}
	page := string(src)
	if !strings.Contains(page, `userIds: [...sel.users], groupIds: [...sel.groups], expectedRevision: current.revision`) {
		t.Error("the page no longer writes a grant as ids with the revision it read")
	}
	if n := strings.Count(page, `api("PUT"`); n != 1 {
		t.Errorf("the page sends PUT from %d places, want exactly one", n)
	}
	if n := strings.Count(page, `api("DELETE"`); n != 1 {
		t.Errorf("the page sends DELETE from %d places, want exactly one", n)
	}
	if strings.Count(page, `e.status === 409`) != 2 {
		t.Error("each write must handle its 409 by reloading and saying so")
	}
}
