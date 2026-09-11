package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The inventory above the inventories.
//
// api/catalog and api/order each keep a table of their handlers, classified as
// gated on the object axis or deliberately not, held by a test in their own
// package. That protects what exists. It does not protect what is built next: a
// new area service arrives with handlers and no table, and a *missing* table
// breaks nothing — only an incomplete one does.
//
// So this test names the area services that have handlers, and insists each one
// either keeps such a table or appears below with the reason it does not. The
// list is a debt rather than a decision: every area on it predates the mechanism,
// and the count may only fall.

// areaHandler matches an HTTP handler method on an area service, which is how an
// area declares that it serves requests.
var areaHandler = regexp.MustCompile(`func \(\w+ \*\w+\) Handle\w*\(\w+ http\.ResponseWriter`)

// gateInventoryFile is what an area's own inventory test is called. One name, so
// this check is a file lookup rather than a second heuristic over test source.
const gateInventoryFile = "gateinventory_test.go"

// areasWithoutAGateInventory are the area services that have no object-axis
// inventory yet, with what stands in for one today.
//
// Adding an entry here is adding debt, and the pin below makes that a visible
// decision rather than a quiet one. Removing an entry — by writing the area its
// table — is the direction this list is meant to move.
var areasWithoutAGateInventory = map[string]string{
	"processdoc": "documentation versions carry a share token rather than a member list; the object axis here is the token, guarded by api/token's shape guard",
	"taskfolder": "a folder is owned by one person and shared by explicit list (ADR-0268); its handlers filter on the caller throughout, but no table states that in one place",
	"formgen":    "holds no state at all — it writes a form from a description and owns no object to gate",
	"panorama":   "models are application-owned and reached through the application's scope; the axis is checked, not inventoried",
	"infomodel":  "same as panorama: application-owned, checked per handler",
	"playground": "scenarios are per-session scratch state, not a shared object anybody else could reach",
}

// areasMissingAGateInventory pins the size of the debt. It may only fall.
//
// A new area service that needs an entry here is a new area service whose
// authorization nobody wrote down, and changing this number is the moment to say
// so out loud in review.
const areasMissingAGateInventory = 6

func TestEveryAreaServiceKeepsAGateInventory(t *testing.T) {
	if got := len(areasWithoutAGateInventory); got != areasMissingAGateInventory {
		t.Fatalf("areasWithoutAGateInventory has %d entries, pinned at %d.\n"+
			"The pin may only fall: writing an area its table lowers it, and adding "+
			"an area to the list is taking on debt that a reviewer should see.",
			got, areasMissingAGateInventory)
	}

	// The area packages are the directories beside this one.
	const apiDir = "."
	areas, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read areas: %v", err)
	}

	var undeclared []string
	for _, e := range areas {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		serves, err := servesHandlers(filepath.Join(apiDir, name))
		if err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		if !serves {
			continue
		}
		if _, excused := areasWithoutAGateInventory[name]; excused {
			continue
		}
		if _, err := os.Stat(filepath.Join(apiDir, name, gateInventoryFile)); err == nil {
			continue
		}
		undeclared = append(undeclared, name)
	}

	if len(undeclared) > 0 {
		t.Fatalf("area services with handlers and no %s: %v\n"+
			"An area that serves requests either proves its object axis by reading "+
			"one list, or says here why it has none. A missing table breaks nothing "+
			"on its own, which is exactly why this test exists.",
			gateInventoryFile, undeclared)
	}

	// And no stale excuses: an area on the list that has since written its table,
	// or that no longer exists, makes the list untrustworthy.
	for name := range areasWithoutAGateInventory {
		dir := filepath.Join(apiDir, name)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("areasWithoutAGateInventory names %q, which is not an area package", name)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, gateInventoryFile)); err == nil {
			t.Errorf("%q has a %s now — remove its entry and lower the pin",
				name, gateInventoryFile)
		}
	}

	for name, why := range areasWithoutAGateInventory {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q is excused with no reason", name)
		}
	}
}

// servesHandlers reports whether a package declares an HTTP handler method,
// which is what makes it an area service rather than a helper package.
func servesHandlers(dir string) (bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return false, err
		}
		if areaHandler.Match(b) {
			return true, nil
		}
	}
	return false, nil
}
