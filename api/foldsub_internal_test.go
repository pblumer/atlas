package api

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// openingDetails is a <details> as the Console's own modules write it into markup.
var openingDetails = regexp.MustCompile(`<details\b[^>]*>`)

// TestEveryFoldInsideATopicDrawsTheTopicsCaret holds every <details> the Console writes
// to the caret a topic draws (ADR-0505, amended 2026-11-02): a fold inside a topic is
// marked fold-sub, which is what draws it. One left unmarked shows the browser's own
// marker beside the topics' caret, the mixed look the amendment removed; the e2e
// guard (e2e/fold-coverage.spec.mjs) sees only what a page draws with no data, so a
// <details> inside a list is held here, in the source. The vendored libraries are not
// the Console's and keep their own look.
func TestEveryFoldInsideATopicDrawsTheTopicsCaret(t *testing.T) {
	files, err := fs.Glob(webFS, "web/*.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, file := range files {
		src := readWeb(t, path.Base(file))
		for n, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // a comment that names the element draws nothing
			}
			for _, tag := range openingDetails.FindAllString(line, -1) {
				seen++
				if !strings.Contains(tag, "fold-sub") {
					t.Errorf("%s:%d draws %s without fold-sub, so it shows the browser's marker instead of the topics' caret", path.Base(file), n+1, tag)
				}
			}
		}
	}
	if seen < 10 {
		t.Fatalf("found only %d <details> in the Console's modules; the scan no longer matches how they are written", seen)
	}
}

// noFoldClass finds an element the Console marks no-fold: in a class attribute, an
// assigned className, or a classList call. The selector `:not(.no-fold)` that fold.js
// reads it with is not a mark, and is not matched.
var noFoldClass = regexp.MustCompile(`(?:class(?:Name)?\s*=\s*["'][^"']*\bno-fold\b|classList\.(?:add|toggle)\(\s*["']no-fold["'])`)

// noFoldReason is the comment that says why: `no-fold: <the reason>`, in JavaScript or
// in the markup a template writes.
var noFoldReason = regexp.MustCompile(`no-fold:\s*(.*)`)

// noFoldReasonLines is how far above the mark its reason may stand: the mark's own
// line and the three before it, so the reason stays beside what it excuses.
const noFoldReasonLines = 3

// noFoldMinReason is the shortest reason accepted. "n/a" or "ok" says nothing; a
// sentence about why this heading is not a section does.
const noFoldMinReason = 20

// noFoldWithoutReason returns the lines (1-based) of src that mark an element no-fold
// with no reason beside them.
func noFoldWithoutReason(src string) []int {
	lines := strings.Split(src, "\n")
	var missing []int
	for i, line := range lines {
		if !noFoldClass.MatchString(line) {
			continue
		}
		said := false
		for j := max(0, i-noFoldReasonLines); j <= i && !said; j++ {
			m := noFoldReason.FindStringSubmatch(lines[j])
			if m == nil {
				continue
			}
			// The reason may run on to the next lines of its comment; what counts is
			// what is written up to the mark, without the comment's own delimiters.
			text := m[1]
			for k := j + 1; k < i; k++ {
				text += " " + lines[k]
			}
			text = strings.NewReplacer("-->", "", "*/", "", "//", "").Replace(text)
			said = len(strings.TrimSpace(text)) >= noFoldMinReason
		}
		if !said {
			missing = append(missing, i+1)
		}
	}
	return missing
}

// TestEveryNoFoldSaysWhy holds every exception to the folding rule to a written reason
// (ADR-0505): an element marked no-fold carries a comment `no-fold: <the reason>` on its
// line or the three above it. The class is how a page tells the guards that a heading
// is not a section; without the reason it would be a quiet way past them, and the next
// reader could not tell a decision from a shortcut.
func TestEveryNoFoldSaysWhy(t *testing.T) {
	for _, pattern := range []string{"web/*.js", "web/*.html"} {
		files, err := fs.Glob(webFS, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			for _, line := range noFoldWithoutReason(readWeb(t, path.Base(file))) {
				t.Errorf("%s:%d marks an element no-fold without a reason; write `no-fold: <why this heading is not a section>` in a comment on that line or the %d above it", path.Base(file), line, noFoldReasonLines)
			}
		}
	}
}

// TestTheNoFoldGuardReadsMarksAndReasons pins what counts as a mark and as a reason, so
// the guard above cannot pass by no longer recognising either.
func TestTheNoFoldGuardReadsMarksAndReasons(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int // marks without a reason
	}{
		{"a mark with its reason in markup above", "<!-- no-fold: the preview is the form's output, not a section -->\n<div class=\"ds-preview no-fold\">", 0},
		{"a reason that runs on over two lines", "<!-- no-fold: the preview is the form's\n     output, not a section -->\n<div class=\"x no-fold\">", 0},
		{"a JavaScript comment on the line before", "// no-fold: a caption over a sample, not a topic\nel.classList.add(\"no-fold\");", 0},
		{"a mark with no reason", "<div class=\"card no-fold\">", 1},
		{"a reason too short to say anything", "<!-- no-fold: n/a -->\n<div class=\"card no-fold\">", 1},
		{"a reason too far above", "// no-fold: a caption over a sample, not a topic\n\n\n\n\n<div class=\"card no-fold\">", 1},
		{"an assigned className", "el.className = \"card no-fold\";", 1},
		{"fold.js reading the class is not a mark", "root.querySelectorAll(\".card:not(.no-fold)\")", 0},
		{"a word that only contains it", "<div class=\"card no-folding\">", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(noFoldWithoutReason(tc.src)); got != tc.want {
				t.Errorf("noFoldWithoutReason found %d marks without a reason, want %d", got, tc.want)
			}
		})
	}
}
