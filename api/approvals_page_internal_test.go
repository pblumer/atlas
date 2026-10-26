package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Console's approvals page (web/approvals.js) answers through the decide route and
// sorts what the worklist route returns by the caller's role. Its words for both are
// copies of the server's, held to them here, and its text to the catalogue in both
// languages.

// TestTheApprovalsPageSpeaksTheRoutesWords: every outcome the page posts is one the
// decide route accepts, and every role it sorts by is one the worklist sets.
func TestTheApprovalsPageSpeaksTheRoutesWords(t *testing.T) {
	src := readPage(t, "approvals.js")
	server := readPage(t, "../actionapprovals.go")
	for _, outcome := range []string{"granted", "rejected", "withdrawn"} {
		if !strings.Contains(src, `data-act="`+outcome+`"`) {
			t.Errorf("the page offers no %q answer", outcome)
		}
		if !strings.Contains(server, `case "`+outcome+`":`) {
			t.Errorf("the decide route no longer accepts %q, which the page posts", outcome)
		}
	}
	for _, role := range []string{"approver", "requester"} {
		if !strings.Contains(server, `role = "`+role+`"`) {
			t.Errorf("the worklist no longer sets role %q", role)
		}
	}
	if !strings.Contains(src, `a.role === "requester"`) {
		t.Error("the page no longer sorts by the requester role")
	}
}

// TestEveryMessageTheApprovalsPageAsksForExists: every key the page looks up is in
// both locales, and the German is written the Swiss way.
func TestEveryMessageTheApprovalsPageAsksForExists(t *testing.T) {
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	src := readPage(t, "approvals.js")
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(approvals\.[A-Za-z0-9._-]+)"`).FindAllStringSubmatch(src, -1) {
		keys[m[1]] = true
	}
	if len(keys) < 40 {
		t.Fatalf("found only %d message keys; the scan no longer matches how the module looks them up", len(keys))
	}
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(string(catalogue), locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("the approvals page asks for %q, which locale %q does not have", key, locale)
			}
		}
		if locale == "de" {
			for _, line := range strings.Split(block, "\n") {
				if strings.Contains(line, `"approvals.`) && strings.Contains(line, "ß") {
					t.Errorf("Swiss spelling writes ss, never ß: %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestTheApprovalsPageHoldsNothing: the page keeps nothing where it would outlive it
// and writes nothing to the address bar or the browser console.
func TestTheApprovalsPageHoldsNothing(t *testing.T) {
	src := readPage(t, "approvals.js")
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`\blocalStorage\b`),
		regexp.MustCompile(`\bsessionStorage\b`),
		regexp.MustCompile(`\bindexedDB\b`),
		regexp.MustCompile(`\bconsole\.`),
		regexp.MustCompile(`\bhistory\.(push|replace)State\b`),
		regexp.MustCompile(`\blocation\.(hash|search|href)\s*=`),
		regexp.MustCompile(`(?i)refreshToken|accessToken`),
	} {
		if loc := forbidden.FindStringIndex(src); loc != nil {
			t.Errorf("web/approvals.js uses %q", src[loc[0]:loc[1]])
		}
	}
}
