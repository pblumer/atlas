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
