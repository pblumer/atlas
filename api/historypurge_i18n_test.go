package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryMessageThePurgeAndReclaimSurfacesAskForExists holds the purge dialog, the
// storage card's reclaim section and the Operations menu entry to the catalogue
// (ADR-0486, ADR-0267): every literal key they
// look up is in both locales. A typo in a key is otherwise found by the administrator
// about to delete five million instances, reading a raw id where the warning belongs.
func TestEveryMessageThePurgeAndReclaimSurfacesAskForExists(t *testing.T) {
	const key = `((?:purge|storage\.reclaim|storage\.every|common)\.[A-Za-z0-9.-]+)`
	lookups := regexp.MustCompile(`\b(?:t|tr)\("` + key + `"`)
	plurals := regexp.MustCompile(`\b(?:plural|trPlural)\("` + key + `"`)

	keys := map[string]bool{}
	for _, file := range []string{"web/historypurge.js", "web/storage.js", "web/app.js"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range lookups.FindAllStringSubmatch(string(src), -1) {
			if file == "web/app.js" && !strings.HasPrefix(m[1], "purge.") {
				continue // app.js's other screens answer to their own tests
			}
			keys[m[1]] = true
		}
		for _, m := range plurals.FindAllStringSubmatch(string(src), -1) {
			keys[m[1]+".one"], keys[m[1]+".other"] = true, true
		}
	}
	if len(keys) < 40 {
		t.Fatalf("found only %d message keys; the scan no longer matches how the modules look them up", len(keys))
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for k := range keys {
			if !strings.Contains(block, `"`+k+`":`) {
				t.Errorf("the purge or reclaim surface asks for %q, which locale %q does not have", k, locale)
			}
		}
	}
}
