package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Console landing page's key-features tile is a static asset
// (web/key-features.json) rendered by web/key-features.js. Like the What's New
// feed it is served straight off the embedded FS with no Go code in the path, so
// nothing else would notice if it were emptied or hand-edited into a shape the UI
// cannot render. This test guards the served file: it must parse, carry a title
// and intro, and every feature must be complete in *both* languages — a tile that
// silently falls back to English is exactly what the bilingual toggle is there to
// avoid.
func TestKeyFeaturesJSONIsValid(t *testing.T) {
	raw, err := webFS.ReadFile("web/key-features.json")
	if err != nil {
		t.Fatalf("read embedded key-features.json: %v", err)
	}

	type bilingual struct {
		En string `json:"en"`
		De string `json:"de"`
	}
	type feature struct {
		ID    string    `json:"id"`
		Title bilingual `json:"title"`
		Text  bilingual `json:"text"`
	}
	var doc struct {
		Title    bilingual `json:"title"`
		Intro    bilingual `json:"intro"`
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("key-features.json is not valid JSON: %v", err)
	}

	for _, h := range []struct {
		name string
		b    bilingual
	}{{"title", doc.Title}, {"intro", doc.Intro}} {
		if strings.TrimSpace(h.b.En) == "" {
			t.Errorf("%s: empty English text", h.name)
		}
		if strings.TrimSpace(h.b.De) == "" {
			t.Errorf("%s: empty German text", h.name)
		}
	}
	if len(doc.Features) == 0 {
		t.Fatal("key-features.json has no features; the tile would render empty")
	}

	seen := map[string]bool{}
	for i, f := range doc.Features {
		if f.ID == "" {
			t.Errorf("feature %d: empty id", i)
		}
		if seen[f.ID] {
			t.Errorf("feature %d: duplicate id %q", i, f.ID)
		}
		seen[f.ID] = true
		for _, fl := range []struct {
			name string
			b    bilingual
		}{{"title", f.Title}, {"text", f.Text}} {
			if strings.TrimSpace(fl.b.En) == "" {
				t.Errorf("feature %d (%s): empty English %s", i, f.ID, fl.name)
			}
			if strings.TrimSpace(fl.b.De) == "" {
				t.Errorf("feature %d (%s): empty German %s", i, f.ID, fl.name)
			}
		}
		// A title is a label, not a sentence: the grid gives it one line.
		if n := len([]rune(f.Title.En)); n > 60 {
			t.Errorf("feature %d (%s): English title is %d chars; too long for the tile's grid", i, f.ID, n)
		}
		if n := len([]rune(f.Title.De)); n > 60 {
			t.Errorf("feature %d (%s): German title is %d chars; too long for the tile's grid", i, f.ID, n)
		}
	}
}
