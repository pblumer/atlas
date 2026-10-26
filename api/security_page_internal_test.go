package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/authz"
)

// The Console's two identity pages (web/connections.js, web/security.js) copy three of
// the server's word lists so they can offer only what the server accepts: the Worker
// Types a connection can serve, the identity modes and the enforcement settings. The
// server decides regardless; a copy that drifts offers a setting the server refuses,
// or hides one it accepts. Each copy is held to its original here, and the pages' text
// to the catalogue in both languages.

func readPage(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile("web/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(src)
}

// exportedList returns the quoted words of `export const <name> = [...]`.
func exportedList(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`export const ` + name + ` = \[([^\]]*)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no `export const %s = [...]`; the test no longer matches the module", name)
	}
	var out []string
	for _, w := range regexp.MustCompile(`"([a-z-]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, w[1])
	}
	sort.Strings(out)
	return out
}

func TestTheIdentityPagesAgreeWithTheServer(t *testing.T) {
	var kinds []string
	for k, ok := range governableKinds {
		if ok {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	if got := exportedList(t, readPage(t, "connections.js"), "GOVERNABLE_KINDS"); strings.Join(got, " ") != strings.Join(kinds, " ") {
		t.Errorf("connections.js offers Worker Types %v, the server governs %v", got, kinds)
	}
	sec := readPage(t, "security.js")
	modes := []string{authz.ModeDelegatedFixed, authz.ModeDelegatedPerJob, authz.ModeService}
	sort.Strings(modes)
	if got := exportedList(t, sec, "MODES"); strings.Join(got, " ") != strings.Join(modes, " ") {
		t.Errorf("security.js offers identity modes %v, the server accepts %v", got, modes)
	}
	enf := []string{enforcementEnforce, enforcementShadow}
	sort.Strings(enf)
	if got := exportedList(t, sec, "ENFORCEMENTS"); strings.Join(got, " ") != strings.Join(enf, " ") {
		t.Errorf("security.js offers enforcement %v, the server accepts %v", got, enf)
	}
}

// TestEveryMessageTheIdentityPagesAskForExists: every key either page looks up is in
// both locales, including the ones built from a value (a connection's status, an
// identity mode's hint). A missing key renders as the key.
func TestEveryMessageTheIdentityPagesAskForExists(t *testing.T) {
	keys := map[string]bool{}
	for _, page := range []string{"connections.js", "security.js"} {
		src := readPage(t, page)
		for _, m := range regexp.MustCompile(`\bt\("((?:connections|security)\.[A-Za-z0-9._-]+)"`).FindAllStringSubmatch(src, -1) {
			keys[m[1]] = true
		}
		for _, m := range regexp.MustCompile(`\? "((?:connections|security)\.[A-Za-z0-9._-]+)" : `).FindAllStringSubmatch(src, -1) {
			keys[m[1]] = true
		}
		for _, m := range regexp.MustCompile(`: "((?:connections|security)\.[A-Za-z0-9._-]+)" : `).FindAllStringSubmatch(src, -1) {
			keys[m[1]] = true
		}
	}
	if len(keys) < 150 {
		t.Fatalf("found only %d message keys; the scan no longer matches how the modules look them up", len(keys))
	}
	// Keys ending in a dot are prefixes completed from a value.
	expand := map[string][]string{
		"connections.status.":     {identity.StatusActive, identity.StatusReauthRequired, identity.StatusRevoked},
		"security.identity.hint.": {authz.ModeService, authz.ModeDelegatedFixed, authz.ModeDelegatedPerJob},
	}
	for prefix, values := range expand {
		if !keys[prefix] {
			t.Fatalf("the pages no longer build a key as t(%q + value); update this test with them", prefix)
		}
		delete(keys, prefix)
		for _, v := range values {
			keys[prefix+v] = true
		}
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("the identity pages ask for %q, which locale %q does not have", key, locale)
			}
		}
		if locale == "de" {
			for _, line := range strings.Split(block, "\n") {
				if (strings.Contains(line, `"connections.`) || strings.Contains(line, `"security.`)) && strings.Contains(line, "ß") {
					t.Errorf("Swiss spelling writes ss, never ß: %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestTheIdentityPagesHoldNoCredential: neither page ever receives a credential, and
// neither keeps anything where it would outlive the page — so a later change that did
// receive one would not find a place to leak it already in use.
func TestTheIdentityPagesHoldNoCredential(t *testing.T) {
	for _, page := range []string{"connections.js", "security.js"} {
		src := readPage(t, page)
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
				t.Errorf("web/%s uses %q", page, src[loc[0]:loc[1]])
			}
		}
	}
}
