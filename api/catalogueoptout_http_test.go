package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The catalogue opt-out (ADR-draft-the-catalogue-can-be-switched-off), through the
// whole stack.
//
// An installation that runs Atlas as a workflow engine and nothing else must be able
// to say so once, at start, and be believed everywhere: no route of the shop, the
// catalogue, the orders or the inventory answers, the API explorer does not describe
// one, the Console does not lead to one, and nothing that was stored is lost — the
// switch is a door, not a delete.

// catalogueTags are the two route tags the opt-out removes. They are the boundary,
// read here off the served OpenAPI document rather than repeated as a list of paths,
// so a route added to the area tomorrow is covered by these tests without editing
// them. The internal test beside this one is what keeps a route of the area from
// arriving under another tag.
var catalogueTags = map[string]bool{"Catalogue": true, "Order": true}

// openServerAt boots a server over dir, so one test can stop it and start another
// over the same data with a different switch — the reversibility case.
func openServerAt(t *testing.T, dir string, opts ...api.Option) (*httptest.Server, func()) {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	closed := false
	stop := func() {
		if closed {
			return
		}
		closed = true
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	}
	t.Cleanup(stop)
	return ts, stop
}

// documentedOps reads the served OpenAPI document into "METHOD /pattern" → tags.
func documentedOps(t *testing.T, ts *httptest.Server) map[string][]string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/openapi.json", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET openapi.json: %d (%s)", code, body)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Tags []string `json:"tags"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi.json: %v", err)
	}
	ops := map[string][]string{}
	for pattern, methods := range doc.Paths {
		for method, op := range methods {
			ops[strings.ToUpper(method)+" "+pattern] = op.Tags
		}
	}
	return ops
}

func inCatalogueArea(tags []string) bool {
	for _, tag := range tags {
		if catalogueTags[tag] {
			return true
		}
	}
	return false
}

// catalogueOps is every operation of the area a server with the catalogue on
// documents, sorted so a failure names routes in a stable order.
func catalogueOps(t *testing.T, ops map[string][]string) []string {
	t.Helper()
	var out []string
	for op, tags := range ops {
		if inCatalogueArea(tags) {
			out = append(out, op)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("the default server documents no Catalogue or Order operation; the tags these tests read have gone stale")
	}
	return out
}

var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// unrouted reports whether a request was answered by the /api/v1 catch-all, which
// is what a route that is not mounted gets: no handler of the area ran.
func unrouted(code int, body []byte) bool {
	return code == http.StatusNotFound && strings.Contains(string(body), "no such endpoint")
}

// TestTheCatalogueIsOnByDefault: an installation that says nothing keeps what it had,
// and the Console is told so.
func TestTheCatalogueIsOnByDefault(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/shop/catalog", "", "")
	if unrouted(code, body) {
		t.Fatalf("GET /api/v1/shop/catalog by default was not routed: %d (%s)", code, body)
	}
	_, info := doReq(t, ts, http.MethodGet, "/api/v1/info", "", "")
	if !strings.Contains(string(info), `"catalogue":true`) {
		t.Errorf("/info does not say the catalogue is on: %s", info)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/shop.html", "", ""); code != http.StatusOK {
		t.Errorf("GET /shop.html by default: %d, want 200", code)
	}
}

// TestSwitchingTheCatalogueOffRemovesEveryRouteOfTheArea is the guarantee itself.
// Each operation the default server documents under Catalogue or Order is asked of a
// server started without the catalogue, and each one must reach no handler at all —
// the same answer an endpoint that never existed gets, so nothing about the area can
// be probed, read or written.
func TestSwitchingTheCatalogueOffRemovesEveryRouteOfTheArea(t *testing.T) {
	on := newTestServer(t)
	off := newTestServerWith(t, api.WithoutCatalogue())

	for _, op := range catalogueOps(t, documentedOps(t, on)) {
		method, pattern, _ := strings.Cut(op, " ")
		path := wildcard.ReplaceAllString(pattern, "x")
		body, ctype := "", ""
		if method != http.MethodGet && method != http.MethodDelete {
			body, ctype = "{}", "application/json"
		}
		if code, got := doReq(t, off, method, path, body, ctype); !unrouted(code, got) {
			t.Errorf("%s with the catalogue off: %d (%s), want the unrouted 404", op, code, got)
		}
	}
}

// TestSwitchingTheCatalogueOffLeavesTheEngineAlone: the rest of the API is exactly
// what it was, and the explorer describes what is served — no less, and nothing of
// the area.
func TestSwitchingTheCatalogueOffLeavesTheEngineAlone(t *testing.T) {
	on := documentedOps(t, newTestServer(t))
	off := newTestServerWith(t, api.WithoutCatalogue())
	offOps := documentedOps(t, off)

	for op, tags := range on {
		_, documented := offOps[op]
		switch {
		case inCatalogueArea(tags) && documented:
			t.Errorf("%s is still documented with the catalogue off", op)
		case !inCatalogueArea(tags) && !documented:
			t.Errorf("%s disappeared with the catalogue off, and it is not part of the catalogue", op)
		}
	}

	// And it still runs processes: deploy one, start it, read it back.
	if code, b := doReq(t, off, http.MethodPost, "/api/v1/deployments", catalogueProvisionBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy with the catalogue off: %d (%s)", code, b)
	}
	code, b := doReq(t, off, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK || !strings.Contains(string(b), "provision-vpn") {
		t.Errorf("GET /api/v1/processes with the catalogue off: %d (%s)", code, b)
	}
}

// TestSwitchingTheCatalogueOffIsSaidToTheConsole: /info is what the Console reads to
// leave the Shop, the Catalogue, Reconciliation and Access review out of its menus,
// and the shop page itself is not served — a bookmark lands on a plain 404 rather
// than on a page that renders and then fails every call it makes.
func TestSwitchingTheCatalogueOffIsSaidToTheConsole(t *testing.T) {
	ts := newTestServerWith(t, api.WithoutCatalogue())

	code, info := doReq(t, ts, http.MethodGet, "/api/v1/info", "", "")
	if code != http.StatusOK || !strings.Contains(string(info), `"catalogue":false`) {
		t.Errorf("/info with the catalogue off: %d (%s)", code, info)
	}
	// The old address must not redirect to a page that is not there either.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/shop.html", "/portal.html"} {
		resp, err := noRedirect.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s with the catalogue off: %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestTheCatalogueSwitchLosesNothing: off is a door, not a delete. A catalogue
// created while the area was on is neither shown nor reachable while it is off — the
// starmap included, which reads the store directly and not through a route — and it
// is all still there when the area is switched back on.
func TestTheCatalogueSwitchLosesNothing(t *testing.T) {
	dir := t.TempDir()

	on, stop := openServerAt(t, dir)
	code, body := doReq(t, on, http.MethodPost, "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	if n := meshNodeKind(getProductMap(t, on), "catalog"); n == 0 {
		t.Fatal("the starmap draws no catalogue while the area is on; this test would prove nothing")
	}
	stop()

	off, stop := openServerAt(t, dir, api.WithoutCatalogue())
	if code, b := doReq(t, off, http.MethodGet, "/api/v1/catalogs/"+cat.ID, "", ""); !unrouted(code, b) {
		t.Errorf("the catalogue is reachable with the area off: %d (%s)", code, b)
	}
	if n := meshNodeKind(getProductMap(t, off), "catalog"); n != 0 {
		t.Errorf("the starmap still draws %d catalogue(s) with the area off", n)
	}
	stop()

	again, _ := openServerAt(t, dir)
	code, body = doReq(t, again, http.MethodGet, "/api/v1/catalogs/"+cat.ID, "", "")
	if code != http.StatusOK || !strings.Contains(string(body), "Arbeitsplatz") {
		t.Errorf("the catalogue did not survive being switched off and on: %d (%s)", code, body)
	}
}

func meshNodeKind(g meshGraph, kind string) int {
	n := 0
	for _, node := range g.Nodes {
		if node.Kind == kind {
			n++
		}
	}
	return n
}
