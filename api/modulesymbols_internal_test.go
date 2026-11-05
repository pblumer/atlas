package api

import (
	"encoding/json"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The modules of atlas — the apps the drawer offers — each have a symbol: a white
// glyph on the module's colour. The symbols are drawn once, in web/module-icons.js,
// whose table is plain JSON between two markers so that the brand scripts
// (scripts/brand/modules.py, scripts/brand/welcome.py) read the same drawing the
// Console paints. Three things are built from it and can drift from it: the drawer,
// the copies under docs/brand/icons/modules, and the welcome illustration, a rocket
// whose sections are the modules. These tests hold all three to the table.

type moduleIcon struct {
	Colour string `json:"colour"`
	Glyph  string `json:"glyph"`
}

var moduleTable = regexp.MustCompile(`(?s)/\* JSON \*/(.*?)/\* /JSON \*/`)

func readModuleIcons(t *testing.T) map[string]moduleIcon {
	t.Helper()
	src, err := webFS.ReadFile("web/module-icons.js")
	if err != nil {
		t.Fatalf("the Console carries no module symbols: %v", err)
	}
	m := moduleTable.FindSubmatch(src)
	if m == nil {
		t.Fatal("web/module-icons.js has no table between /* JSON */ and /* /JSON */; the brand scripts read it there")
	}
	var icons map[string]moduleIcon
	if err := json.Unmarshal(m[1], &icons); err != nil {
		t.Fatalf("the table in web/module-icons.js is not plain JSON, so the brand scripts cannot read it: %v", err)
	}
	return icons
}

// appIDs are the ids of the apps the drawer offers, read from APPS in app.js.
func appIDs(t *testing.T) []string {
	t.Helper()
	src, err := webFS.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "const APPS = [")
	if start < 0 {
		t.Fatal("app.js has no APPS table")
	}
	end := strings.Index(s[start:], "\n];")
	ids := regexp.MustCompile(`\{\s*id:\s*"([a-z]+)"`).FindAllStringSubmatch(s[start:start+end], -1)
	if len(ids) == 0 {
		t.Fatal("found no apps in APPS")
	}
	out := make([]string, 0, len(ids))
	for _, m := range ids {
		out = append(out, m[1])
	}
	return out
}

func TestEveryAppHasASymbol(t *testing.T) {
	icons := readModuleIcons(t)
	colour := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for _, id := range appIDs(t) {
		ic, ok := icons[id]
		if !ok {
			t.Errorf("the app %q has no symbol in web/module-icons.js, so the drawer shows it bare", id)
			continue
		}
		if !colour.MatchString(ic.Colour) {
			t.Errorf("the symbol of %q has colour %q, want #rrggbb", id, ic.Colour)
		}
		if !strings.Contains(ic.Glyph, "<") {
			t.Errorf("the symbol of %q has no glyph", id)
		}
	}
	if len(icons) != len(appIDs(t)) {
		t.Errorf("web/module-icons.js has %d symbols for %d apps; a symbol for an app that is gone is a stale drawing", len(icons), len(appIDs(t)))
	}
}

func TestTheDrawerPaintsTheModuleSymbols(t *testing.T) {
	src, err := webFS.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	s := string(src)
	if !regexp.MustCompile(`import\s*\{[^}]*\bmoduleSymbol\b[^}]*\}\s*from\s*"\./module-icons\.js"`).MatchString(s) {
		t.Error("app.js does not import moduleSymbol from ./module-icons.js")
	}
	start := strings.Index(s, "function paintApps()")
	if start < 0 {
		t.Fatal("app.js has no paintApps")
	}
	end := strings.Index(s[start:], "\n}\n")
	if !strings.Contains(s[start:start+end], "moduleSymbol(") {
		t.Error("paintApps does not draw the module's symbol beside its name")
	}
}

func TestTheBrandCopiesOfTheModuleSymbolsMatchTheConsole(t *testing.T) {
	for id, ic := range readModuleIcons(t) {
		file := "../docs/brand/icons/modules/module-" + id + ".svg"
		body, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("no brand copy of the %q symbol: %v (run scripts/brand/modules.py)", id, err)
			continue
		}
		if !strings.Contains(string(body), ic.Glyph) || !strings.Contains(string(body), `fill="`+ic.Colour+`"`) {
			t.Errorf("%s is not the symbol web/module-icons.js draws; run scripts/brand/modules.py", file)
		}
	}
}

func TestTheWelcomeIllustrationCarriesEveryModule(t *testing.T) {
	art, err := webFS.ReadFile("web/welcome.svg")
	if err != nil {
		t.Fatalf("the Console carries no welcome illustration: %v", err)
	}
	for id, ic := range readModuleIcons(t) {
		if !strings.Contains(string(art), ic.Glyph) {
			t.Errorf("the welcome illustration lacks the %q symbol as web/module-icons.js draws it; run scripts/brand/welcome.py", id)
		}
	}
	// No external resources and no script: it is shown through an <img>, where
	// neither would work, and it must stay a picture.
	for _, bad := range []string{"<script", "href=\"http", "xlink:href=\"http"} {
		if strings.Contains(string(art), bad) {
			t.Errorf("the welcome illustration contains %q", bad)
		}
	}
	app, err := webFS.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	if !strings.Contains(string(app), `src="welcome.svg"`) {
		t.Error("the Console's welcome card does not show welcome.svg")
	}
}

// The illustration is generated; keep the embedded copy and the brand copy one file.
func TestTheWelcomeIllustrationIsTheGeneratedOne(t *testing.T) {
	want, err := os.ReadFile("../docs/brand/atlas-welcome.svg")
	if err != nil {
		t.Fatalf("read docs/brand/atlas-welcome.svg: %v", err)
	}
	got, err := fs.ReadFile(webFS, "web/welcome.svg")
	if err != nil {
		t.Fatalf("read web/welcome.svg: %v", err)
	}
	if string(got) != string(want) {
		t.Error("api/web/welcome.svg is not docs/brand/atlas-welcome.svg; run scripts/brand/welcome.py, which writes both")
	}
}
