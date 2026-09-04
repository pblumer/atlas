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

// TestVendoredBundlesMatchTheirRecordedChecksums.
//
// Each ATLAS-VENDORED.txt says "Do not edit <bundle> by hand" and records the
// SHA-256 that the documented esbuild command produces. Until now that was a claim
// nothing checked, so a hand-edit — or a rebuild somebody forgot to record — would
// have been invisible, and the recipe in that file would slowly stop describing the
// file beside it.
//
// This cannot rebuild the bundles: that needs npm, which a buildless binary
// deliberately does not have at test time (ADR-0012). What it can do is hold each
// file to the number its own documentation states, which catches every way the two
// drift apart except a rebuild whose author also updated the record — and that one
// is the case where they agree.
//
// It walks the vendor directories rather than naming the bundles, so a second
// vendored bundle is covered by existing in the tree rather than by somebody
// remembering to add it here. A directory that carries a record is held to it; a
// directory with no record at all is out of scope here, because the sum to pin it to
// would have to be invented rather than read off a documented rebuild.
func TestVendoredBundlesMatchTheirRecordedChecksums(t *testing.T) {
	records, err := fs.Glob(webFS, "web/vendor/*/ATLAS-VENDORED.txt")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no ATLAS-VENDORED.txt found; this guard would pass vacuously")
	}

	recorded := regexp.MustCompile(`(?m)^SHA-256 ([^\s:]+):\s*\n\s*([0-9a-f]{64})`)
	checked := 0
	for _, rec := range records {
		notes, err := fs.ReadFile(webFS, rec)
		if err != nil {
			t.Fatalf("read %s: %v", rec, err)
		}
		sums := recorded.FindAllStringSubmatch(string(notes), -1)
		if len(sums) == 0 {
			t.Errorf("%s records no SHA-256, so nothing holds the bundle beside it to its recipe.\n"+
				"Add a \"SHA-256 <file>:\" line with the sum the documented rebuild produces.", rec)
			continue
		}
		for _, m := range sums {
			name, want := m[1], m[2]
			file := path.Join(path.Dir(rec), name)
			bundle, err := fs.ReadFile(webFS, file)
			if err != nil {
				t.Errorf("%s records a sum for %s, which is not embedded: %v", rec, name, err)
				continue
			}
			checked++
			if got := fmt.Sprintf("%x", sha256.Sum256(bundle)); got != want {
				t.Errorf("%s hashes to %s, which %s does not record.\n"+
					"Either the bundle was edited by hand — which that file forbids — or it was "+
					"rebuilt without recording the new checksum. Rebuild with the command in "+
					"that file and put this sum in it.", file, got, rec)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no vendored bundle was checked against a recorded sum")
	}
}

// TestOurEmbeddedSourcesAreTextToGrep keeps the hand-written half of api/web
// readable by the tools everyone actually uses on it.
//
// grep, ripgrep, git diff, most editors and every grep-driven CI check classify a
// file as binary the moment it carries a NUL byte, and then skip it silently — no
// error, no mention, just no matches from that file. api/web/editor.js carried one
// for a while (a composite key written with a raw NUL separator), which made the
// largest UI file in the tree invisible to searches nobody thought to run with -a.
//
// The byte was not only a nuisance to tooling: the same key went through a data-key
// attribute, and the HTML parser replaces U+0000 in an attribute value with U+FFFD,
// so the value read back never equalled the one written (e2e/decision-picker.spec.mjs
// covers what that broke). Both problems have one rule: our sources say what they
// mean with escape sequences, not with raw control bytes.
//
// Vendored bundles are exempt: they are third-party build output, guarded by their
// recorded checksums above, and not ours to edit.
func TestOurEmbeddedSourcesAreTextToGrep(t *testing.T) {
	var checked int
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		switch path.Ext(p) {
		case ".js", ".css", ".html", ".json":
		default:
			return nil
		}
		body, err := fs.ReadFile(webFS, p)
		if err != nil {
			return err
		}
		checked++
		if i := strings.IndexByte(string(body), 0); i >= 0 {
			line := 1 + strings.Count(string(body[:i]), "\n")
			t.Errorf("%s carries a NUL byte at line %d.\n"+
				"grep and friends treat the whole file as binary and skip it without saying so. "+
				"Write the character as an escape sequence (\\u0000) instead of emitting the byte — "+
				"and if it travels through an HTML attribute, use a separator that survives parsing: "+
				"the parser turns U+0000 into U+FFFD.", p, line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("no embedded source was checked; this guard would pass vacuously")
	}
}
