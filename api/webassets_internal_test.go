package api

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// The Console is buildless (ADR-0012): the browser loads the ES modules under
// api/web straight off the embedded filesystem, with no bundler in between to
// notice that one of them names a file that is not there.
//
// That makes a mistyped import path a peculiarly bad failure. It is not a broken
// page — it is a *blank* one: the browser refuses the whole module graph, so a typo
// in a leaf module takes down the entire console, and nothing in a Go test run says
// so. This walks the graph the browser would walk.

var jsImport = regexp.MustCompile(`(?m)^\s*(?:import|export)[^"']*from\s*["']([^"']+)["']`)

// TestEveryEmbeddedModuleImportResolves checks that every relative import in every
// embedded .js file names a file the embedded filesystem actually carries.
func TestEveryEmbeddedModuleImportResolves(t *testing.T) {
	files, err := fs.Glob(webFS, "web/*.js")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded modules found; this guard would pass vacuously")
	}
	checked := 0
	for _, file := range files {
		body, err := fs.ReadFile(webFS, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range jsImport.FindAllStringSubmatch(string(body), -1) {
			spec := m[1]
			// Only relative specifiers name a file here. A bare one would be a package
			// name, which this buildless setup has no way to resolve anyway — and the
			// vendored libraries are imported by path like everything else.
			if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
				t.Errorf("%s imports %q, which is not a path — nothing resolves bare specifiers here", file, spec)
				continue
			}
			checked++
			target := path.Join(path.Dir(file), spec)
			if _, err := fs.Stat(webFS, target); err != nil {
				t.Errorf("%s imports %q, which is not embedded (%s). The browser refuses the whole "+
					"module graph on a missing import, so this is a blank console, not a broken widget.",
					file, spec, target)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative imports found across the embedded modules; the pattern must have stopped matching")
	}
}

// TestTheConsoleNavPointsAtRoutesTheRouterServes: a nav entry whose route no
// handler claims renders the fallback rather than a page, which looks like a bug in
// the page rather than in the list. Both live in app.js, so they are checked
// against each other rather than against a copy of either.
func TestTheConsoleNavPointsAtRoutesTheRouterServes(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(body)

	navRoutes := regexp.MustCompile(`\{ name: "[^"]+", route: "(#/console/[^"]+)"`).FindAllStringSubmatch(src, -1)
	if len(navRoutes) == 0 {
		t.Fatal("no console nav entries found; this guard would pass vacuously")
	}
	for _, m := range navRoutes {
		route := m[1]
		if !strings.Contains(src, `path === "`+route+`"`) && !strings.Contains(src, `path.startsWith("`+route+`")`) {
			t.Errorf("the console nav offers %s, which the router does not handle", route)
		}
	}
}

// TestVendoredBundleMatchesItsRecordedChecksum.
//
// ATLAS-VENDORED.txt says "Do not edit archimate-viewer.js by hand" and records
// the SHA-256 that the documented esbuild command produces. Until now that was a
// claim nothing checked, so a hand-edit — or a rebuild somebody forgot to record —
// would have been invisible, and the recipe in that file would slowly stop
// describing the file beside it.
//
// This cannot rebuild the bundle: that needs npm, which a buildless binary
// deliberately does not have at test time (ADR-0012). What it can do is hold the
// file to the number its own documentation states, which catches every way the two
// drift apart except a rebuild whose author also updated the record — and that one
// is the case where they agree.
func TestVendoredBundleMatchesItsRecordedChecksum(t *testing.T) {
	bundle, err := fs.ReadFile(webFS, "web/vendor/archimate/archimate-viewer.js")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	notes, err := fs.ReadFile(webFS, "web/vendor/archimate/ATLAS-VENDORED.txt")
	if err != nil {
		t.Fatalf("read vendoring notes: %v", err)
	}

	sum := fmt.Sprintf("%x", sha256.Sum256(bundle))
	if !strings.Contains(string(notes), sum) {
		t.Errorf("archimate-viewer.js hashes to %s, which ATLAS-VENDORED.txt does not record.\n"+
			"Either the bundle was edited by hand — which that file forbids — or it was "+
			"rebuilt without recording the new checksum. Rebuild with the command in "+
			"ATLAS-VENDORED.txt and put this sum in it.", sum)
	}
}
