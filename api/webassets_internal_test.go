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

// unscopedInstanceList matches a hosted app asking for the whole instance listing:
// GET /instances with no query string. The quoting varies across the pages — a plain
// string or a template literal — so both spellings are matched, and a trailing "?" or
// "/" is what tells a scoped call or a sub-resource apart from the collection.
var unscopedInstanceList = regexp.MustCompile(`["'` + "`" + `]GET["'` + "`" + `]\s*,\s*["'` + "`" + `]/instances["'` + "`" + `]`)

// TestHostedAppsNeverLookForOneInstanceInTheCappedListing.
//
// The hosted apps under api/web (ADR-0204) each drive one process instance, and each
// has to answer two questions about it: which key did my start just create, and what
// state is it in now. Both were answered by reading GET /api/v1/instances and
// searching the result.
//
// That listing is not the set of instances. Unscoped it is capped at
// maxInstanceListDefault rows per half, and its active half is scanned in ascending
// instance-key order — oldest first — so the newest instance is the first thing the
// cap drops. An engine holding more than a thousand active instances therefore serves
// a page that cannot contain the instance the caller just started, and a client that
// treats the page as the whole set finds nothing and concludes the instance does not
// exist. That is how reisebuchung-kunde.html came to fail with "Cannot read properties
// of undefined" on a server whose engine was fine and whose instance had started
// correctly: nothing about the page had changed, only the number of instances in front
// of it.
//
// Two shapes answer these questions without a scan, and a hosted app must use them:
// GET /instances?process=<defKey> reads that definition's own index, newest first, so
// the page holds this definition's instances rather than the engine's oldest; and
// GET /instances/search?q=<instanceKey> is a point read of one instance, live or
// finished.
//
// The rule is checked rather than written down because the failure it prevents is
// invisible in every environment small enough to develop against. A page that reads
// the unscoped listing passes every manual test on a fresh engine and breaks in
// production months later, without a deploy.
func TestHostedAppsNeverLookForOneInstanceInTheCappedListing(t *testing.T) {
	pages, err := fs.Glob(webFS, "web/*.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no embedded pages found; this guard would pass vacuously")
	}
	for _, page := range pages {
		body, err := fs.ReadFile(webFS, page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if !unscopedInstanceList.MatchString(line) {
				continue
			}
			t.Errorf("%s:%d reads the unscoped instance listing:\n  %s\n"+
				"That listing is capped at %d rows per half and its active half is scanned "+
				"oldest-key-first, so on a busy engine it cannot contain a recently started "+
				"instance — the page finds nothing and reports an instance that is running "+
				"fine as missing.\n"+
				"Use GET /instances?process=<defKey> to list one definition's instances "+
				"newest-first off its own index, or GET /instances/search?q=<instanceKey> "+
				"to point-read a single instance.",
				page, i+1, strings.TrimSpace(line), maxInstanceListDefault)
		}
	}
}
