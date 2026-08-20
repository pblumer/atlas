package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Console "What's New" feed is a static asset (web/whats-new.json) generated
// from CHANGELOG.md by scripts/whats-new/gen.mjs. It is served straight off the
// embedded FS with no Go code in the path, so nothing else would notice if it were
// regenerated into garbage, emptied, or hand-edited into an invalid shape. This test
// guards the served file: it must parse, be non-empty, and every entry must carry
// the fields the UI relies on (an English title and summary, an http(s) link), with
// any "Try it" route pointing inside the app and any dates in newest-first order.
func TestWhatsNewJSONIsValid(t *testing.T) {
	raw, err := webFS.ReadFile("web/whats-new.json")
	if err != nil {
		t.Fatalf("read embedded whats-new.json: %v", err)
	}

	type bilingual struct {
		En string `json:"en"`
		De string `json:"de"`
	}
	type link struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	type tryLink struct {
		Label bilingual `json:"label"`
		Route string    `json:"route"`
	}
	type entry struct {
		ID       string    `json:"id"`
		Date     string    `json:"date"`
		Version  string    `json:"version"`
		Title    bilingual `json:"title"`
		Summary  bilingual `json:"summary"`
		Link     link      `json:"link"`
		Tags     []string  `json:"tags"`
		Tutorial *struct {
			En []string `json:"en"`
			De []string `json:"de"`
		} `json:"tutorial"`
		Try *tryLink `json:"try"`
	}
	var doc struct {
		GeneratedAt string  `json:"generatedAt"`
		Entries     []entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("whats-new.json is not valid JSON: %v", err)
	}
	if len(doc.Entries) == 0 {
		t.Fatal("whats-new.json has no entries; the section would render empty")
	}

	seen := map[string]bool{}
	prevDate := "￿" // sorts after any real date, so the first entry always passes
	for i, e := range doc.Entries {
		if e.ID == "" {
			t.Errorf("entry %d: empty id", i)
		}
		if seen[e.ID] {
			t.Errorf("entry %d: duplicate id %q", i, e.ID)
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.Title.En) == "" {
			t.Errorf("entry %d (%s): empty English title", i, e.ID)
		}
		if strings.TrimSpace(e.Summary.En) == "" {
			t.Errorf("entry %d (%s): empty English summary", i, e.ID)
		}
		if !strings.HasPrefix(e.Link.URL, "http://") && !strings.HasPrefix(e.Link.URL, "https://") {
			t.Errorf("entry %d (%s): link url %q is not http(s)", i, e.ID, e.Link.URL)
		}
		if e.Try != nil && !strings.HasPrefix(e.Try.Route, "#/") {
			t.Errorf("entry %d (%s): try route %q does not point inside the app", i, e.ID, e.Try.Route)
		}
		// Newest-first: dated entries must not grow later down the list. Undated
		// (Unreleased) entries sit at the top and are skipped by this check.
		if e.Date != "" {
			if e.Date > prevDate {
				t.Errorf("entry %d (%s): date %q is newer than the entry above (%q); list must be newest-first", i, e.ID, e.Date, prevDate)
			}
			prevDate = e.Date
		}
	}
}
