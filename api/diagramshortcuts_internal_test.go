package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/infomodel"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/limits"
)

func shortcutReq(method, kind, id string, p *httpapi.Principal) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/diagram-shortcuts/favourites/"+url.PathEscape(kind)+"/"+url.PathEscape(id), nil)
	req.SetPathValue("kind", kind)
	req.SetPathValue("id", id)
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	return req
}

// TestDiagramMarksAreRefusedWithoutAnAccountOrADiagram: a mark is filed under an
// account and names a kind of diagram; without either nothing is stored.
func TestDiagramMarksAreRefusedWithoutAnAccountOrADiagram(t *testing.T) {
	srv := newServerForErrors(t)
	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada"}
	for name, tc := range map[string]struct {
		req  *http.Request
		says string
	}{
		"nobody signed in": {shortcutReq(http.MethodPut, "process", "p", nil), "nobody is signed in"},
		"no id":            {shortcutReq(http.MethodPut, "process", " ", ada), "name a diagram"},
		"not a diagram":    {shortcutReq(http.MethodPut, "form", "f", ada), "not form"},
		"too long":         {shortcutReq(http.MethodPut, "process", strings.Repeat("x", maxDiagramID+1), ada), "longer"},
	} {
		rec := httptest.NewRecorder()
		srv.handleMarkDiagram(rec, tc.req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %d (%s), want 400 saying %q", name, rec.Code, rec.Body, tc.says)
		}
	}
	if entries, err := os.ReadDir(srv.diagramShortcuts.Dir()); err != nil || len(entries) != 0 {
		t.Errorf("store after refusals = %v (%v), want empty", entries, err)
	}
}

// TestDiagramFavouritesStopAtTheBudget: the budget bounds what one account can grow
// the store by, and a refused mark leaves the list as it was.
func TestDiagramFavouritesStopAtTheBudget(t *testing.T) {
	l := limits.Default()
	l.DiagramFavourites = 1
	srv := newServerWithOptions(t, WithLimits(l))
	var err error
	srv.do(func() {
		err = srv.diagramShortcuts.Save(diagramShortcuts{
			Principal:  "usr_ada",
			Favourites: []diagramRef{{Kind: "process", ID: "gone"}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.do(func() { err = srv.drafts.Save(draft{ProcessID: "order", Name: "Order", XML: `<definitions/>`}) })
	if err != nil {
		t.Fatal(err)
	}
	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada", Roles: []string{RoleAdmin}}
	rec := httptest.NewRecorder()
	srv.handleMarkDiagram(rec, shortcutReq(http.MethodPut, "process", "order", ada))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mark beyond the budget: %d (%s), want 422", rec.Code, rec.Body)
	}
	var got diagramShortcuts
	srv.do(func() { got, err = srv.diagramShortcutsOf("usr_ada") })
	if err != nil || len(got.Favourites) != 1 || got.Favourites[0].ID != "gone" {
		t.Errorf("after a refused mark = %+v (%v), want the list unchanged", got.Favourites, err)
	}
}

// TestARecentDiagramMovesToTheFrontAndTheListStaysShort: one entry per diagram, the
// newest first, and never more than recentDiagrams.
func TestARecentDiagramMovesToTheFrontAndTheListStaysShort(t *testing.T) {
	var have []diagramVisit
	for i := 0; i < recentDiagrams+5; i++ {
		have = withDiagramVisit(have, diagramRef{Kind: "process", ID: string(rune('a' + i))}, int64(i))
	}
	if len(have) != recentDiagrams {
		t.Fatalf("len = %d, want %d", len(have), recentDiagrams)
	}
	again := have[3].diagramRef
	have = withDiagramVisit(have, again, 99)
	if have[0].diagramRef != again || have[0].OpenedAt != 99 || len(have) != recentDiagrams {
		t.Fatalf("revisit = %+v, want it first, once", have[:2])
	}
	seen := map[diagramRef]bool{}
	for _, v := range have {
		if seen[v.diagramRef] {
			t.Fatalf("%v listed twice", v.diagramRef)
		}
		seen[v.diagramRef] = true
	}
}

// TestDiagramMarksKeepTheOrderTheyWereMadeIn and never rewrite the caller's slice.
func TestDiagramMarksKeepTheOrderTheyWereMadeIn(t *testing.T) {
	a, b, c := diagramRef{"process", "a"}, diagramRef{"decision", "b"}, diagramRef{"panorama", "c"}
	have := []diagramRef{a, b}
	out, changed := withDiagramMark(have, c, true)
	if !changed || !reflect.DeepEqual(out, []diagramRef{a, b, c}) {
		t.Errorf("mark = %v (%v)", out, changed)
	}
	if out, changed = withDiagramMark(out, a, true); changed || len(out) != 3 {
		t.Errorf("marking what is marked changed the list: %v", out)
	}
	out, changed = withDiagramMark(have, a, false)
	if !changed || !reflect.DeepEqual(out, []diagramRef{b}) {
		t.Errorf("clear = %v (%v)", out, changed)
	}
	if !reflect.DeepEqual(have, []diagramRef{a, b}) {
		t.Errorf("the input was modified in place: %v", have)
	}
}

// TestEveryDiagramKindResolvesToItsCanvasAndOnlyWithinItsScope stores one diagram of
// each kind in a private application and asks for them as its owner and as somebody
// outside it. The owner gets each one's name and the route its canvas opens on; the
// outsider gets none of them, except the deployed process, which the process list
// shows everybody and which a visit names by its deployment key.
func TestEveryDiagramKindResolvesToItsCanvasAndOnlyWithinItsScope(t *testing.T) {
	srv := newServerForErrors(t)
	key := deployInternal(t, srv, waitForOrderBPMN)
	srv.authEnabled = true

	var err error
	srv.do(func() {
		for _, save := range []func() error{
			func() error { return srv.projects.Save(project{ID: "app1", Name: "App", OwnerID: "usr_ada"}) },
			func() error {
				return srv.infomodelModels.Save(infomodel.Model{ID: "0a1b2c3d", ApplicationID: "app1", Name: "Kunde"})
			},
			func() error {
				return srv.panoramaModels.Save(panorama.Model{ID: "0d3c2b1a", ApplicationID: "app1", Name: "Landschaft"})
			},
			func() error { return srv.dmnrefs.Save(dmnRef{ID: "dec1", Name: "Rabatt", ProjectID: "app1"}) },
			func() error { return srv.dmnDrafts.Save(dmnDraft{ID: "dd-1", Name: "Neu", ProjectID: "app1"}) },
		} {
			if err = save(); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[diagramRef]string{
		{"infomodel", "0a1b2c3d"}: "#/data/m/0a1b2c3d",
		{"panorama", "0d3c2b1a"}:  "#/panorama/models/0d3c2b1a",
		{"decision", "dec1"}:      "#/modeler/dmn/dec1",
		{"decision", "dd-1"}:      "#/modeler/dmn/d/dd-1",
		{"process", "waiter"}:     "#/modeler/d/" + strconv.FormatUint(key, 10),
	}
	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada"}
	bob := &httpapi.Principal{UserID: "usr_bob", Username: "bob"}
	resolveAs := func(p *httpapi.Principal, ref diagramRef) (resolvedDiagram, bool) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/diagram-shortcuts", nil)
		r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		var (
			got resolvedDiagram
			ok  bool
		)
		srv.do(func() {
			var d *diagramResolver
			if d, err = srv.newDiagramResolver(r); err == nil {
				got, ok, err = d.resolve(ref)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		return got, ok
	}
	for ref, href := range want {
		got, ok := resolveAs(ada, ref)
		// The deployed model carries no name, so it is listed under its id.
		if !ok || got.Href != href || got.Name == "" || (got.Name == ref.ID) != (ref.Kind == "process") {
			t.Errorf("owner, %v = %+v (%v), want %s under its own name", ref, got, ok, href)
		}
		_, ok = resolveAs(bob, ref)
		if outsider := ref.Kind == "process"; ok != outsider {
			t.Errorf("outsider, %v resolves = %v, want %v", ref, ok, outsider)
		}
	}

	// A visit to a deployed version is filed under its process, so v2 and v1 are one
	// entry and the star on a process is the star on all of its versions.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/diagram-shortcuts/recent",
		strings.NewReader(`{"kind":"process","deploymentKey":`+strconv.FormatUint(key, 10)+`}`))
	r = r.WithContext(httpapi.WithPrincipal(r.Context(), bob))
	rec := httptest.NewRecorder()
	srv.handleRecordDiagramVisit(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("visit by deployment key: %d (%s)", rec.Code, rec.Body)
	}
	var got diagramShortcuts
	srv.do(func() { got, err = srv.diagramShortcutsOf("usr_bob") })
	if err != nil || len(got.Recent) != 1 || got.Recent[0].diagramRef != (diagramRef{"process", "waiter"}) {
		t.Errorf("recent = %+v (%v), want the process by its id", got.Recent, err)
	}
}
