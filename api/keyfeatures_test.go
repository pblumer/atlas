package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// The tile enumerates what Atlas *is*, and the product grows. Nothing about a
// feature landing makes the tile wrong in a way anything notices: the page still
// renders, the fourteen entries still read well, and the capability nobody
// mentioned is simply absent. That is the same silent staleness the handbook's
// screenshots have (see .github/workflows/nuggets-check.yml), except the fix is a
// sentence rather than a re-shoot — so it is worth catching where it is cheap,
// which is on the change that introduces it.
//
// So the tile carries a marker: `reviewedThrough` names the newest CHANGELOG
// `### Added` bullet somebody has held against it. A change that adds a bullet
// above that marker has to move it, and moving it is the moment to ask the only
// question that matters — does this change what Atlas is? Most bullets do not,
// and answering "no" costs one line. What the marker buys is that the question
// gets asked at all, by the person who knows the feature, instead of by nobody.
//
// It is deliberately not a check that the tile *mentions* each feature: sixteen
// evergreen statements are not a feature list, and a test that demanded coverage
// would force the tile to become one.
//
// Two branches that each move the marker conflict on one line, and whichever
// resolution wins, the one it did not keep is simply pending again for the next
// change — the failure mode is a question asked twice, never one skipped.
func TestKeyFeaturesTileIsReviewedAgainstTheChangelog(t *testing.T) {
	raw, err := webFS.ReadFile("web/key-features.json")
	if err != nil {
		t.Fatalf("read embedded key-features.json: %v", err)
	}
	var doc struct {
		ReviewedThrough string `json:"reviewedThrough"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("key-features.json is not valid JSON: %v", err)
	}
	if strings.TrimSpace(doc.ReviewedThrough) == "" {
		t.Fatal("key-features.json has no reviewedThrough; it must name the newest CHANGELOG 'Added' bullet the tile has been held against")
	}

	added := changelogAddedTitles(t, filepath.Join("..", "CHANGELOG.md"))
	if len(added) == 0 {
		t.Fatal("no '### Added' bullets found in CHANGELOG.md; the parser above no longer matches the file")
	}

	at := -1
	for i, title := range added {
		if title == doc.ReviewedThrough {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("key-features.json reviewedThrough names %q, which is not an 'Added' bullet in CHANGELOG.md.\n"+
			"An entry was renamed or removed after it was reviewed. Point it at the newest bullet the tile "+
			"still holds for — the current newest is:\n  %q", doc.ReviewedThrough, added[0])
	}
	if at == 0 {
		return // nothing has landed since the last review
	}

	pending := added[:at]
	shown := pending
	if len(shown) > 10 {
		shown = shown[:10]
	}
	var b strings.Builder
	for _, title := range shown {
		b.WriteString("\n  - ")
		b.WriteString(title)
	}
	if len(pending) > len(shown) {
		fmt.Fprintf(&b, "\n  … and %d more", len(pending)-len(shown))
	}
	t.Fatalf("%d feature(s) have landed since the Console's key-features tile was last reviewed:%s\n\n"+
		"For each, ask only: does it change what Atlas *is*? If it does, edit api/web/key-features.json "+
		"(both languages, see its _comment). Most do not, and then there is nothing to write.\n"+
		"Either way, record that the question was asked by setting reviewedThrough to:\n  %q",
		len(pending), b.String(), added[0])
}

// changelogAddedTitles returns the bold headline of every bullet under a
// "### Added" heading, in document order — which CHANGELOG.md keeps newest-first.
//
// It mirrors how scripts/whats-new/gen.mjs reads the same file, including the one
// non-obvious part: a bullet's bold headline may wrap across lines, so the bullet's
// first paragraph is joined before the headline is cut out of it. Headlines are
// matched by text rather than by the generator's slug so that this test does not
// have to keep a second copy of the slug rule in step with it.
func changelogAddedTitles(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var (
		version  = regexp.MustCompile(`^## \[([^\]]+)\]`)
		category = regexp.MustCompile(`^### (\w+)`)
		bullet   = regexp.MustCompile(`^-\s+\*\*`)
		headline = regexp.MustCompile(`^-\s+\*\*(.+?)\*\*`)
		spaces   = regexp.MustCompile(`\s+`)
	)

	lines := strings.Split(string(raw), "\n")
	var out []string
	inVersion, inAdded := false, false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case version.MatchString(line):
			inVersion, inAdded = true, false
			continue
		case category.MatchString(line):
			inAdded = category.FindStringSubmatch(line)[1] == "Added"
			continue
		case !inVersion || !bullet.MatchString(line):
			continue
		}

		// The bullet's first paragraph: this line plus its continuations.
		block := []string{line}
		j := i + 1
		for ; j < len(lines); j++ {
			l := lines[j]
			if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "#") {
				break
			}
			block = append(block, l)
		}
		i = j - 1

		if m := headline.FindStringSubmatch(strings.Join(block, " ")); m != nil && inAdded {
			out = append(out, strings.TrimSpace(spaces.ReplaceAllString(m[1], " ")))
		}
	}
	return out
}
