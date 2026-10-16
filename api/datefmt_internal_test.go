package api

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every instant the console prints goes through web/datefmt.js
// (ADR-0459), and this is what keeps the
// forty-fourth call site from quietly going back to the browser's own clock.
//
// The failure it guards is invisible in review and invisible on screen: a bare
// `toLocaleString()` renders in whatever time zone the browser happens to be in,
// which looks perfectly right to the person who wrote it — they are usually in
// that zone — and reads an hour or nine off to everybody else. There is nothing
// to notice until somebody acts on a timestamp at the wrong time.
//
// So the rule is mechanical: in the console's own modules, those three methods
// belong to one file.
var localeTimeCall = regexp.MustCompile(`toLocale(String|DateString|TimeString)\s*\(`)

// datefmtExempt are the files the rule does not apply to, each for a stated
// reason. It is a short list on purpose — "it was easier here" is not one of the
// reasons, and a new entry should have to be argued for in review.
var datefmtExempt = map[string]string{
	// The formatter itself, which is where Intl is supposed to be called.
	"web/datefmt.js": "owns the formatting",
	// numfmt.js names the method in a comment explaining why it does not use it.
	"web/numfmt.js": "mentions it in prose, does not call it",
	// The customer-facing portal is a different audience with a different rule:
	// its language follows the browser (ADR-0313), because the person reading it
	// has no Atlas account and no preference stored anywhere.
	"web/shop.js": "portal, follows the browser by ADR-0313",
}

func TestEveryTimestampGoesThroughTheSharedFormatter(t *testing.T) {
	var offenders []string
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		// Vendored libraries are somebody else's code and are not ours to convert.
		if strings.Contains(p, "/vendor/") {
			return nil
		}
		if _, ok := datefmtExempt[p]; ok {
			return nil
		}
		data, readErr := webFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			if localeTimeCall.MatchString(line) {
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", p, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk web: %v", err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these render an instant in the browser's own time zone rather than the one the "+
			"reader chose — use fmtDateTime/fmtDate/fmtTime from web/datefmt.js:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
