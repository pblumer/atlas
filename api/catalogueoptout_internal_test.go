package api

import (
	"context"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/compiler"
)

// What keeps the catalogue opt-out whole as the area grows
// (ADR-draft-the-catalogue-can-be-switched-off).
//
// The switch removes the routes tagged Catalogue or Order. That is one statement of
// the boundary, and a route that joins the area under some other tag would stay
// served with the switch off — quietly, because nothing about it looks wrong. These
// tests state the boundary a second and a third way, independently of the tag, and
// fail when the statements disagree.

// catalogueSegments is the second statement: the first path segment after /api/v1/
// of every route of the area. Kept here, in the test, and not beside the switch,
// because a list the implementation reads cannot also be the check on it.
var catalogueSegments = map[string]bool{
	"catalogs":         true,
	"catalog-products": true,
	"shop":             true,
	"orders":           true,
	"approvals":        true,
	"inventory":        true,
	"inventory-load":   true,
	"entitlements":     true,
	"conflicts":        true,
	"reconciliation":   true,
	"recertification":  true,
	"pending-work":     true,
	"events":           true,
	// The event feed's push subscriptions (ADR-0433): the same facts as /events,
	// sent rather than fetched.
	"feed-subscriptions": true,
}

// catalogueHandlerPackages is the third: a handler written in either package of the
// area is a handler of the area, whatever its route is called.
var catalogueHandlerPackages = []string{
	"github.com/pblumer/atlas/api/catalog.",
	"github.com/pblumer/atlas/api/order.",
}

func firstSegment(pattern string) string {
	rest := strings.TrimPrefix(pattern, "/api/v1/")
	seg, _, _ := strings.Cut(rest, "/")
	return seg
}

func handlerName(h any) string {
	return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
}

// TestTheCatalogueSwitchCoversTheWholeArea: every route that any of the three
// statements places in the area is one the switch removes, and every route the
// switch removes is one the segment list knows — so a new segment of the area has to
// be added here, which is the moment somebody reads this comment.
func TestTheCatalogueSwitchCoversTheWholeArea(t *testing.T) {
	for _, r := range specServer().apiRoutes() {
		op := r.method + " " + r.pattern
		gated := catalogueRoute(r)
		seg := firstSegment(r.pattern)
		if catalogueSegments[seg] && !gated {
			t.Errorf("%s is under /api/v1/%s/, which belongs to the catalogue, but is tagged %q: "+
				"the switch would leave it served. Tag it Catalogue or Order", op, seg, r.op.tag)
		}
		if gated && !catalogueSegments[seg] {
			t.Errorf("%s is switched off with the catalogue, but /api/v1/%s/ is not in catalogueSegments: "+
				"add the segment, so the next route under it is held to the switch too", op, seg)
		}
		name := handlerName(r.handler)
		for _, pkg := range catalogueHandlerPackages {
			if strings.HasPrefix(name, pkg) && !gated {
				t.Errorf("%s is served by %s, a handler of the catalogue, but is tagged %q: "+
					"the switch would leave it served", op, name, r.op.tag)
			}
		}
	}
}

// TestSwitchingTheCatalogueOffDropsTheAreaAndNothingElse: the switched-off table is
// the full table minus the area, exactly — the same table the mux, the OpenAPI
// document and the node descriptor are all built from.
func TestSwitchingTheCatalogueOffDropsTheAreaAndNothingElse(t *testing.T) {
	full := specServer().apiRoutes()
	off := (&Server{catalogueOff: true}).apiRoutes()

	served := map[string]bool{}
	for _, r := range off {
		op := r.method + " " + r.pattern
		served[op] = true
		if catalogueRoute(r) {
			t.Errorf("%s is still in the route table with the catalogue off", op)
		}
	}
	area := 0
	for _, r := range full {
		op := r.method + " " + r.pattern
		if catalogueRoute(r) {
			area++
			continue
		}
		if !served[op] {
			t.Errorf("%s left the route table with the catalogue off, and it is not part of the catalogue", op)
		}
	}
	if area == 0 {
		t.Fatal("no route of the full table belongs to the catalogue; the boundary has gone stale and this test checks nothing")
	}
	if len(off)+area != len(full) {
		t.Errorf("switched-off table has %d routes, want %d (full %d minus %d of the area)", len(off), len(full)-area, len(full), area)
	}
	// The zero value is the shipped default, and it must be on: about seventy tests
	// build a Server as a literal, and every one of them describes the full surface.
	if len(specServer().apiRoutes()) != len(full) || specServer().catalogueOff {
		t.Error("a Server literal no longer serves the catalogue; the default polarity of the switch has flipped")
	}
}

// TestTheShopTasksAreServedWithTheCatalogueOff: switching the area off takes away
// the surface, never the engine's semantics. A product process already running
// reaches its shop task whether or not anybody can open the shop, and a task with no
// handler does not fail — it parks, silently, which is the failure ADR-0411 was
// written about. Deployed models behave the same under either setting.
func TestTheShopTasksAreServedWithTheCatalogueOff(t *testing.T) {
	s := newServerWithOptions(t, WithoutCatalogue())
	for _, jt := range []int32{compiler.ShopJobTypeIndex, compiler.ShopCommandJobTypeIndex} {
		if !s.jobRunner.Handles(jt) {
			t.Errorf("job type %d has no in-process handler with the catalogue off; its jobs would park", jt)
		}
	}
}

// TestTheOrderMachineryIsNotDeployedWithTheCatalogueOff: a server that does not
// offer a shop does not file its fulfilment and approval processes into the system
// project. The user-management processes are deployed as before, and the delete
// guard still knows every platform process — one deployed by an earlier start with
// the catalogue on stays protected.
func TestTheOrderMachineryIsNotDeployedWithTheCatalogueOff(t *testing.T) {
	s := newServerWithOptions(t, WithSystemProcesses(), WithoutCatalogue())
	for pid := range catalogueSystemProcesses {
		if _, ok := s.latestDeploymentFor(pid); ok {
			t.Errorf("%s was deployed with the catalogue off", pid)
		}
		if !s.systemPIDs[pid] {
			t.Errorf("%s is no longer a protected system process with the catalogue off", pid)
		}
	}
	for _, pid := range []string{"proc_benutzer_aufnahme", "proc_benutzer_offboarding", "proc_benutzer_review"} {
		if _, ok := s.latestDeploymentFor(pid); !ok {
			t.Errorf("%s was not deployed with the catalogue off; it is not part of the catalogue", pid)
		}
	}
	for id := range catalogueSystemForms {
		if _, ok, err := s.forms.Get(id); err != nil || ok {
			t.Errorf("form %s was seeded with the catalogue off (err %v)", id, err)
		}
	}

	on := newServerWithOptions(t, WithSystemProcesses())
	for pid := range catalogueSystemProcesses {
		if _, ok := on.latestDeploymentFor(pid); !ok {
			t.Errorf("%s was not deployed with the catalogue on", pid)
		}
	}
}

var (
	bundleAPIPath = regexp.MustCompile(`/api/v1/([a-z-]+)`)
	bundleFormID  = regexp.MustCompile(`formId="([^"]+)"`)
)

// TestTheCatalogueSystemProcessesAreTheOnesThatCallIt: which embedded processes are
// the catalogue's is read off what they call, not remembered. A process that calls a
// route of the area is the area's, and one that does not must not be held back with
// it; a form is the area's when only the area's processes use it.
func TestTheCatalogueSystemProcessesAreTheOnesThatCallIt(t *testing.T) {
	procs, forms, err := loadSystemBundle(systemBundleFS)
	if err != nil {
		t.Fatal(err)
	}
	usedBy := map[string][]string{}
	known := map[string]bool{}
	for _, p := range procs {
		known[p.processID] = true
		calls := false
		for _, m := range bundleAPIPath.FindAllStringSubmatch(string(p.xml), -1) {
			if catalogueSegments[m[1]] {
				calls = true
			}
		}
		if calls != catalogueSystemProcesses[p.processID] {
			t.Errorf("system process %s: calls the catalogue = %v, held back with it = %v", p.processID, calls, catalogueSystemProcesses[p.processID])
		}
		for _, m := range bundleFormID.FindAllStringSubmatch(string(p.xml), -1) {
			usedBy[m[1]] = append(usedBy[m[1]], p.processID)
		}
	}
	for pid := range catalogueSystemProcesses {
		if !known[pid] {
			t.Errorf("catalogueSystemProcesses names %s, which the bundle does not ship", pid)
		}
	}
	for _, f := range forms {
		users := usedBy[f.id]
		sort.Strings(users)
		onlyArea := len(users) > 0
		for _, u := range users {
			if !catalogueSystemProcesses[u] {
				onlyArea = false
			}
		}
		if onlyArea != catalogueSystemForms[f.id] {
			t.Errorf("system form %s (used by %v): only the catalogue's = %v, held back with it = %v", f.id, users, onlyArea, catalogueSystemForms[f.id])
		}
	}
}

// TestTheModelerIsNotShownTheProductActionsOfASwitchedOffCatalogue: the message
// picker reads the catalogue store directly, like the starmap, so it is told
// separately. With the area on the same store answers, which is what makes the empty
// answer with it off mean something.
func TestTheModelerIsNotShownTheProductActionsOfASwitchedOffCatalogue(t *testing.T) {
	seed := func(s *Server) {
		t.Helper()
		if err := s.catalogStore.SaveCatalog(catalog.Catalog{ID: "c1", Rank: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.catalogStore.SaveItem(catalog.Item{ID: "tool", HomeCatalog: "c1", LifecycleProcess: "tool-strand",
			Actions: []catalog.Action{{Key: "password-reset", Message: "tool.reset", Effect: "service"}}}); err != nil {
			t.Fatal(err)
		}
	}

	on := newServerWithOptions(t)
	seed(on)
	if rows, err := on.productActionSources(nil); err != nil || len(rows) == 0 {
		t.Fatalf("with the area on: %d row(s), %v; the seed lists nothing and this test proves nothing", len(rows), err)
	}

	off := newServerWithOptions(t, WithoutCatalogue())
	seed(off)
	if rows, err := off.productActionSources(nil); err != nil || len(rows) != 0 {
		t.Errorf("with the area off: %d product action(s) listed, %v", len(rows), err)
	}
}

// TestTheFeedIsNotPushedWithTheCatalogueOff: the feed says who holds what across the
// catalogue, and with the area off its pull route is not served — so it does not
// leave Atlas by push either. A subscription made while the area was on keeps its
// cursor, and delivery picks up there once the area is back; setting the field on a
// running server here stands in for that restart.
func TestTheFeedIsNotPushedWithTheCatalogueOff(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)

	srv.catalogueOff = true
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 0 {
		t.Fatalf("%d batch(es) were pushed with the catalogue off", n)
	}
	if rec := storedSub(t, srv, sub.ID); rec.Cursor != sub.Cursor || !rec.Enabled {
		t.Fatalf("the subscription moved while the catalogue was off: %+v", rec)
	}

	srv.catalogueOff = false
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 1 {
		t.Fatalf("after switching back on, %d batch(es) were pushed, want the one it held back", n)
	}
}
