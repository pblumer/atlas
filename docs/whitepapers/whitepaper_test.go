package whitepapers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/blueprintregistry"
)

// The atlas hub whitepaper is a maintained document, not a snapshot: it explains
// decisions that keep being amended and limits that live in code. A whitepaper that
// silently falls behind is worse than none, because readers quote it. These tests
// therefore hold it to its sources the way key-features.json is held to the
// CHANGELOG: a new amendment to ADR-0499 or ADR-0467 fails here until somebody has
// read the whitepaper against it and moved the marker, and a limit changed in code
// fails here until the whitepaper states the new value.

const hubWhitepaper = "atlas-hub-signierte-verteilung.md"

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// lastAmendment is the last heading of an ADR that records an amendment, in the
// order the record keeps them: "## Amendment 2026-11-03 (d): …" or
// "### … (amended 2026-11-05)".
func lastAmendment(t *testing.T, adr string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "adr", adr+"-*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("find %s: %v %v", adr, matches, err)
	}
	heading := regexp.MustCompile(`(?m)^#{2,3} (.*(?:Amendment \d{4}-\d{2}-\d{2}|\(amended \d{4}-\d{2}-\d{2}\)).*)$`)
	all := heading.FindAllStringSubmatch(read(t, matches[0]), -1)
	if len(all) == 0 {
		t.Fatalf("%s records no amendment heading", adr)
	}
	return strings.TrimSpace(all[len(all)-1][1])
}

func TestTheHubWhitepaperIsReviewedAgainstTheLatestAmendments(t *testing.T) {
	doc := read(t, hubWhitepaper)
	for _, adr := range []string{"0499", "0467"} {
		want := lastAmendment(t, adr)
		marker := regexp.MustCompile(`(?m)^reviewed-through ADR-` + adr + `: (.*)$`).FindStringSubmatch(doc)
		if marker == nil {
			t.Errorf("%s has no `reviewed-through ADR-%s:` line in its header comment", hubWhitepaper, adr)
			continue
		}
		if got := strings.TrimSpace(marker[1]); got != want {
			t.Errorf("ADR-%s has a newer amendment than %s was reviewed against.\n"+
				"  reviewed through: %s\n  latest:           %s\n"+
				"Read the whitepaper against the amendment, correct what it changes, then set the marker to the latest heading.",
				adr, hubWhitepaper, got, want)
		}
	}
}

// mib renders a byte limit the way the whitepaper states it.
func mib(n int64) string { return fmt.Sprintf("höchstens %d MiB", n>>20) }

func TestTheHubWhitepaperStatesTheLimitsTheCodeEnforces(t *testing.T) {
	doc := read(t, hubWhitepaper)
	for what, phrase := range map[string]string{
		"signed index (MaxIndexBytes)":               mib(blueprintregistry.MaxIndexBytes),
		"releases per index (MaxReleases)":           fmt.Sprintf("höchstens %d", blueprintregistry.MaxReleases),
		"compressed package (MaxPackageBytes)":       mib(blueprintregistry.MaxPackageBytes),
		"expanded package (MaxPackageExpandedBytes)": mib(blueprintregistry.MaxPackageExpandedBytes),
		"tags per release (MaxTags)":                 fmt.Sprintf("bis zu %d eindeutige", blueprintregistry.MaxTags),
		"pinned keys (MaxPinnedKeys = 2)":            map[int]string{1: "genau einen", 2: "einen oder zwei"}[blueprintregistry.MaxPinnedKeys],
	} {
		if phrase == "" || !strings.Contains(doc, phrase) {
			t.Errorf("%s does not state the %s limit as %q", hubWhitepaper, what, phrase)
		}
	}
}

// The whitepaper follows the house style its readers were promised: Swiss spelling.
func TestTheHubWhitepaperUsesSwissSpelling(t *testing.T) {
	if strings.Contains(read(t, hubWhitepaper), "ß") {
		t.Errorf("%s contains ß; Swiss spelling writes ss", hubWhitepaper)
	}
}
