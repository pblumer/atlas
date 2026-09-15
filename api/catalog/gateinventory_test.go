package catalog

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// What a credential can reach has to be provable by reading one list.
//
// ADR-0209 says that of roles and holds it with an inventory test. The object
// axis had no such list, and the cost showed: four gaps in a row — the catalogue
// write side, product rehoming, ordering against a release, and reading — each
// real, each found only while building the next thing beside it. Per-route tests
// prove what their author thought of; they cannot prove that a *new* route was
// thought about at all.
//
// So: every handler this service exports is named below, either as gated (an
// outsider is refused, and the test proves it by asking) or as deliberately
// ungated with the reason. Reflection supplies the actual set, so adding a
// handler and forgetting the entry fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// gated: touches a specific catalogue object, and an outsider must be refused.
	gated gateKind = iota
	// ungated: reaches no particular object, or is itself the visibility
	// resolution. Each carries its reason.
	ungated
)

type handlerGate struct {
	name string
	kind gateKind
	// want is the status an outsider must receive from a gated handler. 404 where
	// the object must not be shown to exist, 403 where it may be seen but not
	// changed — here always 404, because an outsider sees nothing.
	want int
	// method, body and id describe how to call it. contentType is set only for the
	// handlers that read raw bytes and take the format from the header.
	method      string
	contentType string
	body        string
	id          bool
	// why explains an ungated handler. Empty for gated ones.
	why string
}

var catalogGates = []handlerGate{
	{name: "HandleListCatalogs", kind: ungated, method: "GET",
		why: "lists only what the caller maintains, so it names no object to gate; the outsider case is that the list comes back empty, proved in TestListingShowsOnlyWhatYouMaintain"},
	{name: "HandleCreateCatalog", kind: ungated, method: "POST", body: `{"rank":1}`,
		why: "creation has no object yet — the role is the whole gate, and the creator becomes the owner"},
	{name: "HandleGetCatalog", kind: gated, want: http.StatusNotFound, method: "GET", id: true},
	{name: "HandleUpdateCatalog", kind: gated, want: http.StatusNotFound, method: "PATCH", body: `{"rank":9}`, id: true},
	{name: "HandlePublish", kind: gated, want: http.StatusNotFound, method: "POST", id: true},
	{name: "HandleListReleases", kind: gated, want: http.StatusNotFound, method: "GET", id: true},
	{name: "HandleListItems", kind: ungated, method: "GET",
		why: "lists only products whose home the caller maintains; the outsider case is an empty list, proved in TestProductListingFollowsTheHomeCatalogue"},
	{name: "HandleSaveItem", kind: gated, want: http.StatusNotFound, method: "POST",
		body: `{"id":"x","homeCatalog":"CAT","state":"active","texts":{"de":"X"},"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`},
	{name: "HandleImport", kind: gated, want: http.StatusNotFound, method: "POST",
		body: `<?xml version="1.0" encoding="UTF-8"?><model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m"/>`,
		id:   true},
	{name: "HandleSetTheme", kind: gated, want: http.StatusNotFound, method: "PUT",
		body: `{"accent":"#112233"}`, id: true},
	{name: "HandleGetLogo", kind: gated, want: http.StatusNotFound, method: "GET", id: true},
	// The body is a real PNG and the header is right, so what the outsider meets is
	// the gate and not the format check. That order matters for the test and not for
	// the endpoint: a 415 is decided by the caller's own header and says nothing
	// about whether the catalogue exists.
	{name: "HandleSetLogo", kind: gated, want: http.StatusNotFound, method: "PUT",
		contentType: "image/png", body: "\x89PNG\r\n\x1a\n" + "body", id: true},
	{name: "HandleDeleteLogo", kind: gated, want: http.StatusNotFound, method: "DELETE", id: true},
	{name: "HandleMyCatalog", kind: ungated, method: "GET",
		why: "is the visibility resolution itself: it answers from the caller's own groups and returns 404 when they reach none"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range catalogGates {
		named[g.name] = true
	}

	var missing []string
	typ := reflect.TypeOf(&Service{})
	handler := reflect.TypeOf(http.HandlerFunc(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		// A handler is a method assignable to http.HandlerFunc.
		if m.Type.NumIn() != 3 || m.Type.NumOut() != 0 {
			continue
		}
		fn := reflect.FuncOf([]reflect.Type{m.Type.In(1), m.Type.In(2)}, nil, false)
		if !fn.ConvertibleTo(handler) {
			continue
		}
		if !named[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("handlers with no entry in catalogGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops
	// being read.
	for _, g := range catalogGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("catalogGates names %q, which is not a handler on this service", g.name)
		}
	}
}

// TestEveryGatedHandlerRefusesAnOutsider is the behaviour half. It asks each one
// rather than reading it, because a gate is what it does and not what it says.
func TestEveryGatedHandlerRefusesAnOutsider(t *testing.T) {
	for _, g := range catalogGates {
		if g.kind != gated {
			continue
		}
		t.Run(g.name, func(t *testing.T) {
			s := serviceWithAdmin(t)
			cat := makeCatalog(t, s, user("usr_owner"))
			as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH",
				`{"groups":["grp_inside"]}`, "id", cat.ID)

			h := reflect.ValueOf(s).MethodByName(g.name).
				Interface().(func(http.ResponseWriter, *http.Request))

			body := g.body
			if body != "" {
				body = strings.ReplaceAll(body, "CAT", cat.ID)
			}
			outsider := &httpapi.Principal{UserID: "usr_out", Roles: []string{"productmanager"},
				GroupIDs: []string{"grp_elsewhere"}}

			var rec = asTyped(t, h, outsider, g.method, g.contentType, body)
			if g.id {
				rec = asTyped(t, h, outsider, g.method, g.contentType, body, "id", cat.ID)
			}
			if rec.Code != g.want {
				t.Fatalf("%s gave an outsider %d (%s), want %d",
					g.name, rec.Code, rec.Body, g.want)
			}
		})
	}
}

// TestEveryUngatedHandlerSaysWhy: an exemption with no reason is an exemption
// nobody reviewed.
func TestEveryUngatedHandlerSaysWhy(t *testing.T) {
	for _, g := range catalogGates {
		if g.kind == ungated && g.why == "" {
			t.Errorf("%s is ungated with no reason given", g.name)
		}
		if g.kind == gated && g.why != "" {
			t.Errorf("%s is gated but carries an exemption reason", g.name)
		}
	}
}
