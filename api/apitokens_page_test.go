package api

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The Console's API-token page (web/apitokens.js) states four of the server's rules
// so the form can say them before it is submitted: which scopes a token may be minted
// with, which roles each one carries, how long a token may live, and which scopes ask
// for a reach. The server decides regardless — but a copy that drifts makes the form
// offer a scope the server refuses, hide one it accepts, or promise roles the token
// will not carry, and the person reading it has no way to tell. So each copy is held
// to its original here, and the page's text is held to the catalogue.

// apiTokenPage reads the page module once per test.
func apiTokenPage(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("web/apitokens.js")
	if err != nil {
		t.Fatalf("read the API-token page: %v", err)
	}
	return string(src)
}

// pageConst returns the source between `export const <name> =` and the end of its
// statement. The page writes each of these as one literal for exactly this reason.
func pageConst(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "export const "+name+" = ")
	if start < 0 {
		t.Fatalf("web/apitokens.js has no `export const %s`; the test no longer matches the module", name)
	}
	rest := src[start:]
	end := strings.Index(rest, ";\n")
	if end < 0 {
		t.Fatalf("`export const %s` has no end", name)
	}
	return rest[:end]
}

var pageQuotedWord = regexp.MustCompile(`"([a-z]+)"`)

func pageStrings(block string) []string {
	var out []string
	for _, m := range pageQuotedWord.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestTheAPITokenPageAgreesWithTheServer holds the page's copies to the server's rules.
func TestTheAPITokenPageAgreesWithTheServer(t *testing.T) {
	src := apiTokenPage(t)

	// The scopes: the same set as apiMintableScopes, each once. The page orders them
	// narrowest first, so the order is the page's and only the set is compared.
	type pageScope struct {
		id, reach string
		required  bool
	}
	var scopes []pageScope
	for _, m := range regexp.MustCompile(`\{ id: "([a-z]+)", reach: "([a-z]*)"(, required: true)? \}`).FindAllStringSubmatch(pageConst(t, src, "SCOPES"), -1) {
		scopes = append(scopes, pageScope{m[1], m[2], m[3] != ""})
	}
	var got []string
	for _, s := range scopes {
		got = append(got, s.id)
	}
	sort.Strings(got)
	if want := apiScopes(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the page offers the scopes %v, the server mints %v", got, want)
	}

	// The reach each scope asks for: a landscape token must name projects (reachFor
	// refuses it otherwise), a diagrams token may name projects, because the
	// architecture list shows it only the applications its reach names (ADR-0493, as
	// amended), an events token may name catalogues, and the page asks nothing of any
	// other scope.
	for _, s := range scopes {
		want, required := "", false
		switch s.id {
		case apiScopeLandscape:
			want, required = "projects", true
		case apiScopeDiagrams:
			want = "projects"
		case apiScopeEvents:
			want = "catalogues"
		}
		if s.reach != want || s.required != required {
			t.Errorf("the page asks a %s token for reach %q (required %t), want %q (required %t)", s.id, s.reach, s.required, want, required)
		}
	}

	// The roles: what the page previews is what tokenRolesFor answers for the person
	// who can open the page — an administrator — and, with sign-in off, for nobody.
	fixed := map[string][]string{}
	for _, m := range regexp.MustCompile(`(\w+): \[([^\]]*)\]`).FindAllStringSubmatch(pageConst(t, src, "SCOPE_ROLES"), -1) {
		fixed[m[1]] = pageStrings(m[2])
	}
	minter := pageStrings(pageConst(t, src, "ADMIN_TOKEN_ROLES"))
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}}
	for _, scope := range apiMintableScopes {
		preview := minter
		if r, ok := fixed[scope]; ok {
			preview = r
		}
		for _, p := range []*httpapi.Principal{admin, nil} {
			roles, refusal := tokenRolesFor(scope, p)
			if refusal != "" || strings.Join(roles, " ") != strings.Join(preview, " ") {
				t.Errorf("for a %s token the page previews %v, the server gives %v (%q)", scope, preview, roles, refusal)
			}
		}
	}
	for scope := range fixed {
		if !validAPIScope(scope) {
			t.Errorf("the page fixes the roles of %q, which is not a mintable scope", scope)
		}
	}

	// The longest lifetime, in the days the form counts in.
	m := regexp.MustCompile(`export const MAX_DAYS = (\d+);`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("web/apitokens.js has no `export const MAX_DAYS = <n>;`")
	}
	if days, _ := strconv.Atoi(m[1]); days != int(maxAPITokenLifetime/(24*time.Hour)) {
		t.Errorf("the page allows %d days, the server %d", days, int(maxAPITokenLifetime/(24*time.Hour)))
	}

	// The warning window: the page and the atlas_api_tokens_expiring_soon alert warn
	// about the same tokens, or an operator is told twice different things.
	m = regexp.MustCompile(`export const EXPIRY_WARN_DAYS = (\d+);`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("web/apitokens.js has no `export const EXPIRY_WARN_DAYS = <n>;`")
	}
	if days, _ := strconv.Atoi(m[1]); days != int(apiTokenExpiryWarning/(24*time.Hour)) {
		t.Errorf("the page warns %d days ahead, /metrics %d", days, int(apiTokenExpiryWarning/(24*time.Hour)))
	}
}

// TestEveryMessageTheAPITokenPageAsksForExists: every key the page looks up is in
// both locales, including the one sentence per scope it builds from the scope's id.
// A missing key renders as the key (i18n.js), which nobody notices until they read
// the page in that language.
func TestEveryMessageTheAPITokenPageAsksForExists(t *testing.T) {
	src := apiTokenPage(t)
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\b(?:t|th)\("(apitokens\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(src, -1) {
		keys[m[1]] = true
	}
	// Keys chosen by a ternary are two literals inside one call; both are lookups.
	for _, m := range regexp.MustCompile(`\? "(apitokens\.[A-Za-z0-9.-]+)" : "(apitokens\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(src, -1) {
		keys[m[1]], keys[m[2]] = true, true
	}
	// A counted message is two keys, its one and its other form (i18n.js plural).
	plurals := regexp.MustCompile(`\bplural\("(apitokens\.[A-Za-z0-9.-]+)"`).FindAllStringSubmatch(src, -1)
	if len(plurals) < 3 {
		t.Fatalf("found only %d counted messages in web/apitokens.js; the scan no longer matches how the module counts", len(plurals))
	}
	for _, m := range plurals {
		keys[m[1]+".one"], keys[m[1]+".other"] = true, true
	}
	if len(keys) < 40 {
		t.Fatalf("found only %d message keys in web/apitokens.js; the scan no longer matches how the module looks them up", len(keys))
	}
	// A key ending in a dot is the prefix of one built from a value — the scope's
	// sentence, apitokens.scope.<id> — and stands for one key per mintable scope.
	if !keys["apitokens.scope."] {
		t.Fatal(`web/apitokens.js no longer builds a scope's sentence as t("apitokens.scope." + id); update this test with it`)
	}
	delete(keys, "apitokens.scope.")
	for _, scope := range apiMintableScopes {
		keys["apitokens.scope."+scope] = true
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("web/apitokens.js asks for %q, which locale %q does not have", key, locale)
			}
		}
		if locale == "de" {
			for _, line := range strings.Split(block, "\n") {
				if strings.Contains(line, `"apitokens.`) && strings.Contains(line, "ß") {
					t.Errorf("Swiss spelling writes ss, never ß: %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestTheAPITokenPageKeepsTheSecretOutOfEverythingThatOutlivesIt: the secret is shown
// once and must end with the page. Storage outlives the tab, the URL lands in history
// and in referrers, and the console is read by whoever opens the developer tools, so
// the module touches none of them — not even for something unrelated, because a
// reviewer of the next change should not have to tell the two apart.
func TestTheAPITokenPageKeepsTheSecretOutOfEverythingThatOutlivesIt(t *testing.T) {
	src := apiTokenPage(t)
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`\blocalStorage\b`),
		regexp.MustCompile(`\bsessionStorage\b`),
		regexp.MustCompile(`\bindexedDB\b`),
		regexp.MustCompile(`\bconsole\.`),
		regexp.MustCompile(`\bhistory\.(push|replace)State\b`),
		regexp.MustCompile(`\blocation\.(hash|search|href)\s*=`),
	} {
		if loc := forbidden.FindStringIndex(src); loc != nil {
			t.Errorf("web/apitokens.js uses %q", src[loc[0]:loc[1]])
		}
	}
	// The cleanup the router runs on every navigation is the page's own.
	if !strings.Contains(src, "window.__atlasCleanup = dropSecret") {
		t.Error("web/apitokens.js no longer hands dropSecret to the router as its cleanup")
	}
}
