package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryMessageTheCreateMenuAsksForExists holds the Modeler's "Create new" menu —
// on the application landscape and inside an application — to the catalogue
// (ADR-0267): every key it looks up is in both locales. A typo in a key otherwise
// shows the author a raw id in the one menu every new artifact starts from.
func TestEveryMessageTheCreateMenuAsksForExists(t *testing.T) {
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	app, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web/app.js: %v", err)
	}
	lookups := regexp.MustCompile(`\btr\("(create\.[A-Za-z0-9.-]+)"`)

	keys := map[string]bool{}
	for _, m := range lookups.FindAllStringSubmatch(string(app), -1) {
		keys[m[1]] = true
	}
	if len(keys) < 9 {
		t.Fatalf("found only %d create-menu keys; the scan no longer matches how app.js looks them up", len(keys))
	}
	src := string(catalogue)
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(src, locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for k := range keys {
			if !strings.Contains(block, `"`+k+`":`) {
				t.Errorf("the create menu asks for %q, which locale %q does not have", k, locale)
			}
		}
	}
}
