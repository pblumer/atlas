package api

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The forms a person actually fills in are drawn by the vendored form-js runtime,
// not by app.css. web/form-theme.css is the bridge that carries the org's brand
// palette across that boundary (ADR-0113's accent, ADR-0028's form runtime).
//
// A bridge made of CSS custom properties has exactly two ways to fail silently, and
// neither shows up as a broken page — a themed form simply goes back to looking like
// stock bpmn.io, and nobody notices until a customer does:
//
//   1. form-js renames or drops a token the bridge writes to. The declaration stays
//      valid CSS, addresses nothing, and paints nothing.
//   2. app.css renames a token the bridge reads from. `var(--gone)` resolves to
//      nothing and the property falls back to form-js's own default.
//
// Both are invisible to every other test in this package, so they are checked here
// against the real files, and both directions are checked: what the bridge writes
// must be something form-js reads, and what the bridge reads must be something a
// host page declares.

// cssDecl matches a custom-property declaration. It is deliberately not anchored to
// the start of a line: app.css writes one declaration per line, but the two public
// pages pack a whole palette onto one, and a line-anchored pattern would see only the
// first of them and report the rest as missing.
var cssDecl = regexp.MustCompile(`(?:^|[;{])\s*(--[a-zA-Z0-9-]+)\s*:`)

// cssComment matches a CSS block comment. Every reader below strips comments first:
// these files explain themselves by quoting the declarations they are about, and a
// token named inside an explanation is not a token the stylesheet sets or reads.
var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripComments removes block comments (and HTML ones, for the inline styles in the
// public pages) so only real declarations and references are examined.
func stripComments(css string) string {
	css = cssComment.ReplaceAllString(css, " ")
	return regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(css, " ")
}

// cssVarRef matches a custom-property *reference*: `var(--name`
var cssVarRef = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)`)

// rootBlock matches a `:root { … }` rule body. The blocks it has to find (app.css's
// palette, the two public pages' inline ones) contain no nested braces, so the lazy
// match to the first `}` is exact rather than approximate.
var rootBlock = regexp.MustCompile(`(?s):root\s*\{(.*?)\}`)

// readWeb reads one embedded UI asset, failing the test if it is not there.
func readWeb(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil {
		t.Fatalf("read web/%s: %v", name, err)
	}
	return string(body)
}

// declaredProps returns every custom property declared anywhere in a stylesheet.
func declaredProps(css string) map[string]bool {
	out := map[string]bool{}
	for _, m := range cssDecl.FindAllStringSubmatch(stripComments(css), -1) {
		out[m[1]] = true
	}
	return out
}

// referencedProps returns every custom property a stylesheet reads through var().
func referencedProps(css string) map[string]bool {
	out := map[string]bool{}
	for _, m := range cssVarRef.FindAllStringSubmatch(stripComments(css), -1) {
		out[m[1]] = true
	}
	return out
}

// rootProps returns the custom properties declared in a document's `:root` rules —
// the tokens a page promises to any stylesheet layered on top of it.
func rootProps(css string) map[string]bool {
	out := map[string]bool{}
	for _, block := range rootBlock.FindAllStringSubmatch(stripComments(css), -1) {
		for name := range declaredProps(block[1]) {
			out[name] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestFormThemeWritesOnlyTokensFormJSReads holds direction one: every token the
// bridge sets is one the vendored form-js stylesheet actually resolves. A form-js
// upgrade that renames `--cds-field` or drops `--font-size-label` turns the matching
// line in form-theme.css into a no-op, and this is what says so.
func TestFormThemeWritesOnlyTokensFormJSReads(t *testing.T) {
	bridge := readWeb(t, "form-theme.css")
	formJS := readWeb(t, "vendor/form-js/form-js.css")
	read := referencedProps(formJS)

	written := sortedKeys(declaredProps(bridge))
	if len(written) == 0 {
		t.Fatal("form-theme.css declares no custom properties; this guard would pass vacuously")
	}
	for _, name := range written {
		if !read[name] {
			t.Errorf("form-theme.css sets %s, which form-js.css never reads — the declaration paints nothing", name)
		}
	}
}

// TestFormThemeReachesTheFormContainer holds the assumption the bridge is built on.
//
// form-js resolves its own colours through an IBM Carbon token first and a built-in
// default second, e.g. `--color-text: var(--cds-text-primary, <grey>)`. Setting that
// Carbon layer on `:root` only reaches a form because form-js does *not* redeclare
// it on `.fjs-container`: its reset of the whole `--cds-*` layer to `initial` sits on
// `.fjs-no-theme`, a class the runtime never applies. If a future version moves that
// reset onto the container — or starts emitting that class — every `:root` mapping in
// form-theme.css stops arriving, with no error anywhere.
func TestFormThemeReachesTheFormContainer(t *testing.T) {
	formJS := readWeb(t, "vendor/form-js/form-js.css")

	container := regexp.MustCompile(`(?s)\.fjs-container\s*\{(.*?)\}`).FindStringSubmatch(formJS)
	if container == nil {
		t.Fatal("form-js.css has no `.fjs-container { … }` rule; the vendored stylesheet is not what this bridge was written against")
	}
	for name := range declaredProps(container[1]) {
		if strings.HasPrefix(name, "--cds-") {
			t.Errorf("form-js.css now declares %s on .fjs-container; a :root mapping no longer reaches it, "+
				"so form-theme.css's first block must move to `:root .fjs-container`", name)
		}
	}

	viewer, err := fs.ReadFile(webFS, "web/vendor/form-js/form-viewer.js")
	if err != nil {
		t.Fatalf("read vendored viewer: %v", err)
	}
	if strings.Contains(string(viewer), "fjs-no-theme") {
		t.Error("the form-js viewer now emits the `fjs-no-theme` class, which resets the --cds-* layer to initial; " +
			"form-theme.css's :root mapping cannot reach a container carrying it")
	}
}

// formThemeHosts are the pages that layer form-theme.css (or its tokens) over their
// own `:root`. Each has to declare the base palette the bridge reads, because a
// missing token there is a form that silently keeps form-js's stock grey-and-blue.
var formThemeHosts = []string{"app.css", "public-form.html"}

// TestFormThemeReadsOnlyTokensItsHostsDeclare holds direction two: every Atlas token
// the bridge reads is declared in the `:root` of each page that renders a form.
// app.css covers the console (Tasks, incidents, the form editor's preview);
// public-form.html covers the share-link page, which loads no app.css at all and so
// has to carry the same names itself.
func TestFormThemeReadsOnlyTokensItsHostsDeclare(t *testing.T) {
	bridge := readWeb(t, "form-theme.css")
	written := declaredProps(bridge)

	// What the bridge reads and does not itself define is a token it expects from
	// the page underneath.
	var needed []string
	for _, name := range sortedKeys(referencedProps(bridge)) {
		if !written[name] {
			needed = append(needed, name)
		}
	}
	if len(needed) == 0 {
		t.Fatal("form-theme.css reads no host tokens; this guard would pass vacuously")
	}

	for _, host := range formThemeHosts {
		declared := rootProps(readWeb(t, host))
		for _, name := range needed {
			if !declared[name] {
				t.Errorf("%s does not declare %s in :root, but form-theme.css reads it — a form rendered on that page falls back to form-js's own colours", host, name)
			}
		}
	}
}

// publicPages are the two standalone, pre-session surfaces: the start form behind a
// share link and the OAuth consent screen. Neither loads app.css, so each carries its
// own copy of the palette.
var publicPages = []string{"public-form.html", "oauth-consent.html"}

// TestPublicPagesDeclareEveryTokenTheyUse keeps those inline copies honest. They are
// the one place in the UI where a token can be *used* without app.css underneath to
// define it, and an undefined custom property is not an error — it is a property that
// silently computes to nothing, which on `color` means text that inherits whatever
// happens to be above it.
func TestPublicPagesDeclareEveryTokenTheyUse(t *testing.T) {
	for _, page := range publicPages {
		body := readWeb(t, page)
		declared := rootProps(body)
		if len(declared) == 0 {
			t.Fatalf("%s declares no :root tokens; this guard would pass vacuously", page)
		}
		for _, name := range sortedKeys(referencedProps(body)) {
			if !declared[name] {
				t.Errorf("%s uses %s but never declares it; the property computes to nothing", page, name)
			}
		}
	}
}

// TestEveryFormSurfaceLoadsTheThemeBridge holds the wiring. The bridge only works
// where it is actually loaded, and the surfaces that render a form reach the runtime
// by three different paths: the Tasks app and incidents through formviewer.js, the
// form editor through its own lazy loader, and the public page through a <link>.
// Each is a place someone can add a form and forget the theme.
func TestEveryFormSurfaceLoadsTheThemeBridge(t *testing.T) {
	for _, tc := range []struct{ file, want, why string }{
		{"formviewer.js", "form-theme.css", "the Tasks app and incident repair forms load the runtime here"},
		{"form-editor.js", "ensureFormStyles", "the form editor's preview must be painted like the runtime"},
		{"public-form.html", "/form-theme.css", "the public start form loads no app.css, so it links the bridge itself"},
	} {
		if body := readWeb(t, tc.file); !strings.Contains(body, tc.want) {
			t.Errorf("web/%s no longer references %q — %s", tc.file, tc.want, tc.why)
		}
	}
}

// TestPublicPagesApplyTheOrgBrand holds the other half of what makes these pages
// brandable: the palette and the logo are org settings on the server, and a page that
// never asks for them shows the stock Atlas blue and the Atlas glyph to whoever opens
// a share link — the one surface an organisation's own customers see.
func TestPublicPagesApplyTheOrgBrand(t *testing.T) {
	for _, page := range publicPages {
		body := readWeb(t, page)
		for _, want := range []string{`from "/theme.js"`, `from "/logo.js"`, "syncFromServer()", "syncLogoFromServer()"} {
			if !strings.Contains(body, want) {
				t.Errorf("web/%s does not %s; it would show the stock palette instead of the org's brand", page, want)
			}
		}
	}
}

// TestAccentInkIsDerivedNotHardCoded guards the reason --accent-ink exists.
//
// Text on an accent fill used to be `#fff` written out at each rule, which is a fixed
// guess that a brand colour can invalidate: white on a pale accent is a primary button
// no accessibility review passes. theme.js now derives the ink from the accent's own
// luminance, and every rule that paints on the accent has to read the token rather
// than restate the guess — otherwise the derivation exists but changes nothing.
func TestAccentInkIsDerivedNotHardCoded(t *testing.T) {
	if theme := readWeb(t, "theme.js"); !strings.Contains(theme, `"--accent-ink"`) {
		t.Fatal("theme.js no longer derives --accent-ink; the palette cannot keep a button label readable on a light brand colour")
	}
	css := readWeb(t, "app.css")
	// A rule that sets an accent background and then names a literal white is the
	// shape this replaces. Checked line by line because app.css writes most rules
	// on one line.
	for i, line := range strings.Split(css, "\n") {
		if !strings.Contains(line, "background: var(--accent)") {
			continue
		}
		if strings.Contains(line, "color: #fff") {
			t.Errorf("app.css:%d paints on the accent with a literal white: %s\n\tuse var(--accent-ink) so a light brand colour gets dark ink instead",
				i+1, strings.TrimSpace(line))
		}
	}
}

// TestFormThemeMappingIsDocumented is a small readability guard: the bridge is a file
// of bare token assignments whose *reason* is not visible in the assignments, and the
// two facts that make it work (which selector each block needs, and why the inverted
// greys are left alone) are recoverable from nothing but its comments.
func TestFormThemeMappingIsDocumented(t *testing.T) {
	bridge := readWeb(t, "form-theme.css")
	comment := strings.Count(bridge, "/*")
	if comment == 0 {
		t.Fatal("form-theme.css carries no comments; the cascade rules it depends on are not recoverable from the declarations")
	}
	for _, want := range []string{"fjs-no-theme", ":root .fjs-container"} {
		if !strings.Contains(bridge, want) {
			t.Errorf("form-theme.css no longer mentions %q; %s", want,
				fmt.Sprintf("the file's correctness depends on it and the next reader has no way to know"))
		}
	}
}
