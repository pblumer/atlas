package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/formlookup"
)

// TestTheDirectoryFieldSpeaksBothLanguages holds the seam between the lookup service
// and the console's directory field (ADR-0473): the server
// answers with codes, the field words them, and a code or a message the catalogue
// lacks would show a raw key in the middle of a task form.
func TestTheDirectoryFieldSpeaksBothLanguages(t *testing.T) {
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	widget, err := os.ReadFile("web/remote-select.js")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bt\("(remote\.[\w.]+)"`).FindAllStringSubmatch(string(widget), -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bplural\("(remote\.[\w.]+)"`).FindAllStringSubmatch(string(widget), -1) {
		keys[m[1]+".one"], keys[m[1]+".other"] = true, true
	}
	if len(keys) < 10 {
		t.Fatalf("found only %d message keys in remote-select.js; the scan no longer reads the widget", len(keys))
	}
	keys["remote.error.generic"] = true
	for _, c := range formlookup.Codes {
		keys["remote.error."+c] = true
	}
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(string(catalogue), locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for k := range keys {
			if !strings.Contains(block, `"`+k+`":`) {
				t.Errorf("locale %q has no message %q", locale, k)
			}
		}
	}
}
