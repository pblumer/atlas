package api

import (
	"regexp"
	"strings"
	"testing"
)

// The dark appearance answers every colour the light palette names
// (ADR-0507).
//
// app.css's first `:root` block is the palette, and the `:root[data-appearance="dark"]`
// block redeclares its tokens for a dark surface. A colour token added to the palette
// and forgotten in the dark block is drawn in its light value on a dark page — a pale
// chip, a white panel — and nothing looks wrong in the light console the change was
// made and reviewed in. So the two lists are held together here: every palette token
// whose value is a colour is answered in the dark block, and the dark block tells the
// browser it is dark, so native controls and scrollbars follow it.
func TestTheDarkAppearanceAnswersEveryPaletteColour(t *testing.T) {
	css := stripComments(readWeb(t, "app.css"))
	light := rootBlock.FindStringSubmatch(css)
	if light == nil {
		t.Fatal("app.css has no :root palette block")
	}
	dark := regexp.MustCompile(`(?s):root\[data-appearance="dark"\]\s*\{(.*?)\}`).FindStringSubmatch(css)
	if dark == nil {
		t.Fatal(`app.css has no :root[data-appearance="dark"] block`)
	}

	decl := regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:\s*([^;]+);`)
	colour := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(rgb|rgba|hsl|hsla)\(|\b(white|black)\b`)

	answered := map[string]bool{}
	for _, m := range decl.FindAllStringSubmatch(dark[1], -1) {
		answered[m[1]] = true
	}
	// The brand and the ink laid on it are the organisation's, not the appearance's:
	// a filled button is the same colour in both, and theme.js applies them inline
	// over either block. What the dark appearance answers for them is the accent as
	// text, --accent-text.
	brand := map[string]bool{"--accent": true, "--accent-ink": true}

	var palette, missing []string
	for _, m := range decl.FindAllStringSubmatch(light[1], -1) {
		if !colour.MatchString(m[2]) || brand[m[1]] {
			continue // a font stack, a length, a radius, the brand: nothing to turn over
		}
		palette = append(palette, m[1])
		if !answered[m[1]] {
			missing = append(missing, m[1])
		}
	}
	// Non-vacuous: a palette the patterns above stopped recognizing would pass
	// with nothing missing because nothing was found.
	if len(palette) < 15 {
		t.Fatalf("found only %d colour tokens in the :root palette (%v); the pattern no longer reads app.css", len(palette), palette)
	}
	if len(missing) > 0 {
		t.Errorf("the dark appearance does not answer %d palette colour(s): %s — declare each in app.css's "+
			`:root[data-appearance="dark"] block, or a dark page draws it in its light value`,
			len(missing), strings.Join(missing, ", "))
	}
	if !regexp.MustCompile(`color-scheme\s*:\s*dark\b`).MatchString(dark[1]) {
		t.Error(`the dark block does not set color-scheme: dark, so native controls stay light`)
	}
}

// Accent-coloured text reads the text tone, never the accent itself (ADR-0507).
//
// In the light palette the two are the same colour, so a rule that writes
// `color: var(--accent)` looks right in every review — and on the dark surface it
// paints a brand colour chosen for a white page at 3:1 or less, which is a link
// nobody can read. --accent-text is the accent where that is readable and a lighter
// tone of it where it is not, so text goes through it.
func TestAccentColouredTextReadsTheTextTone(t *testing.T) {
	css := stripComments(readWeb(t, "app.css"))
	raw := regexp.MustCompile(`(?m)(^|[^-\w])color\s*:\s*var\(--accent\)`)
	lines := strings.Split(css, "\n")
	var found []string
	for _, line := range lines {
		if raw.MatchString(line) {
			found = append(found, strings.TrimSpace(line))
		}
	}
	if len(found) > 0 {
		t.Errorf("app.css sets %d text colour(s) to the accent itself; use var(--accent-text) so the text stays readable in the dark appearance:\n\t%s",
			len(found), strings.Join(found, "\n\t"))
	}
	if !strings.Contains(css, "var(--accent-text)") {
		t.Fatal("app.css reads no --accent-text at all; the pattern no longer finds the text tone")
	}
}
