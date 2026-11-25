package api

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The reach of a person's grant for `…/shop`, and the writes a catalogue maintained in
// another system holds (ADR-0565).

// TestTheShopReachIsTheTasksReachAndTheShop: everything a tasks grant reaches, a shop
// grant reaches too — a system that shows the shop also shows the tasks it opens — and
// none of the three order routes ADR-0550 keeps off MCP, which are the fulfilment's and
// the approval process's own calls.
func TestTheShopReachIsTheTasksReachAndTheShop(t *testing.T) {
	shop := apiScopeAllowed[apiScopeShop]
	for _, pattern := range apiScopeAllowed[apiScopeTasks] {
		if !slices.Contains(shop, pattern) {
			t.Errorf("the shop scope does not reach %q, which the tasks scope does", pattern)
		}
	}
	for _, kept := range []string{
		"POST /api/v1/orders/{id}/lines/{item}",
		"POST /api/v1/orders/{id}/lines/{item}/start",
		"POST /api/v1/orders/{id}/lines/{item}/decision",
	} {
		if slices.Contains(shop, kept) {
			t.Errorf("the shop scope reaches %q, which ADR-0550 keeps off MCP and off a customer", kept)
		}
		method, path, _ := strings.Cut(kept, " ")
		path = strings.NewReplacer("{id}", "o1", "{item}", "i1").Replace(path)
		if apiScopeMayReach(apiScopeShop, httptest.NewRequest(method, path, nil)) {
			t.Errorf("a shop grant may perform %s %s", method, path)
		}
	}
	// The shop's customer side is reached as MCP reaches it: every route the
	// --mcp-shop switch names is in the reach, except the two order-builder reads the
	// portal's basket uses and a connected system does not.
	for route := range mcpShopRoutes {
		if route == "GET /api/v1/orders/{id}/next" || route == "GET /api/v1/orders/{id}/round" {
			continue
		}
		if !slices.Contains(shop, route) {
			t.Errorf("the shop scope does not reach %q, a route of the shop's customer side", route)
		}
	}
	if validAPIScope(apiScopeShop) {
		t.Error("the shop scope is mintable; it follows from the resource a person approved")
	}
	if got := (oauthGrant{Resource: "https://atlas.example.com/shop"}).scope(); got != apiScopeShop {
		t.Errorf("a …/shop grant scoped %q, want %q", got, apiScopeShop)
	}
}

// TestTheShopIsACanonicalResource: a client asking for `…/shop` names a resource this
// server publishes, and pre-consent applies to it as to `…/tasks`.
func TestTheShopIsACanonicalResource(t *testing.T) {
	s := &Server{externalURL: "https://atlas.example.com"}
	r := httptest.NewRequest("GET", "https://atlas.example.com/oauth/authorize", nil)
	for _, res := range []string{"https://atlas.example.com/shop", "https://atlas.example.com/tasks"} {
		if !s.isCanonicalResource(r, res) {
			t.Errorf("%s is not a canonical resource", res)
		}
	}
	if s.isCanonicalResource(r, "https://atlas.example.com/shopping") {
		t.Error("a resource that only starts like the shop was accepted")
	}
}

// TestOnlyAShopGrantOfAClientPublishesForIt: what the catalogue service is told about
// a request. A person's own session, a tasks grant, an API token and a shop grant with
// no client all publish for nobody.
func TestOnlyAShopGrantOfAClientPublishesForIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *httpapi.Principal
		want string
	}{
		{"nobody", nil, ""},
		{"a session", &httpapi.Principal{UserID: "usr_a", Session: true}, ""},
		{"a tasks grant", &httpapi.Principal{UserID: "usr_a", Scope: apiScopeTasks, ClientID: "c"}, ""},
		{"a grant for the whole server", &httpapi.Principal{UserID: "usr_a", ClientID: "c"}, ""},
		{"a shop grant without a client", &httpapi.Principal{UserID: "usr_a", Scope: apiScopeShop}, ""},
		{"a shop grant", &httpapi.Principal{UserID: "usr_a", Scope: apiScopeShop, ClientID: "c", ClientName: "Wiki"}, "c"},
	} {
		if got, _ := catalogPublisher(tc.p); got != tc.want {
			t.Errorf("%s publishes for %q, want %q", tc.name, got, tc.want)
		}
	}
}

// catalogueWritesHeld are the writes refused while a catalogue, or a product's home, is
// maintained in another system, unless the request comes through that system's own
// grant for the shop. api/catalog's TestEveryWriteToACatalogueMaintainedElsewhere… and
// this package's TestAShopGrantShopsAsThePerson… prove each.
var catalogueWritesHeld = []string{
	"PATCH /api/v1/catalogs/{id}",
	"DELETE /api/v1/catalogs/{id}",
	"POST /api/v1/catalogs/{id}/releases",
	"POST /api/v1/catalogs/{id}/import",
	"POST /api/v1/catalogs/import",
	"POST /api/v1/catalog-products",
	"DELETE /api/v1/catalog-products/{id}",
	"PUT /api/v1/catalog-products/{id}/picture",
	"DELETE /api/v1/catalog-products/{id}/picture",
	"POST /api/v1/catalog-products/{id}/content",
	"POST /api/v1/catalog-products/{id}/content/unlink",
}

// catalogueWritesOpen are the catalogue's writes that stay open while it is maintained
// elsewhere, each with the reason.
var catalogueWritesOpen = map[string]string{
	"POST /api/v1/catalogs":                              "creates a catalogue, which is maintained here",
	"POST /api/v1/catalogs/import/mappings":              "this installation's table of names, not a catalogue",
	"DELETE /api/v1/catalogs/import/mappings":            "this installation's table of names, not a catalogue",
	"POST /api/v1/catalogs/import/answers":               "writes nothing",
	"POST /api/v1/catalogs/{id}/releases/pruning":        "an administrator's housekeeping of releases nobody orders against; a document carries no releases",
	"POST /api/v1/catalog-products/{id}/rebind":          "moves orders onto the product's current binding; the orders are Atlas's, the product is not written",
	"POST /api/v1/catalog-products/{id}/content/refresh": "fetches what the binding names, as the recheck does on its own; the binding is the document's",
	"POST /api/v1/catalogs/{id}/content/refresh":         "the same refresh for every bound product",
	"PUT /api/v1/catalogs/{id}/theme":                    "the appearance is an administrator's (ADR-0316) and a document carries none",
	"PUT /api/v1/catalogs/{id}/logo":                     "the appearance is an administrator's (ADR-0316) and a document carries none",
	"DELETE /api/v1/catalogs/{id}/logo":                  "the appearance is an administrator's (ADR-0316) and a document carries none",
	"DELETE /api/v1/catalogs/{id}/maintainer":            "the take-back itself",
}

// TestEveryCatalogueWriteIsHeldOrOpenOnPurpose: a write route of the catalogue added
// later must be decided — held while the catalogue is maintained elsewhere, or open
// with a reason — rather than left to overwrite what the next publish replaces.
func TestEveryCatalogueWriteIsHeldOrOpenOnPurpose(t *testing.T) {
	_, policy := accessTestServer(t).mountRoutes()
	declared := map[string]bool{}
	for _, pattern := range policy.declaredPatterns() {
		declared[pattern] = true
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || method == "GET" || method == "HEAD" {
			continue
		}
		if !strings.HasPrefix(path, "/api/v1/catalogs") && !strings.HasPrefix(path, "/api/v1/catalog-products") {
			continue
		}
		_, open := catalogueWritesOpen[pattern]
		held := slices.Contains(catalogueWritesHeld, pattern)
		switch {
		case open && held:
			t.Errorf("%s is listed both as held and as open", pattern)
		case !open && !held:
			t.Errorf("%s writes the catalogue and is neither held while it is maintained elsewhere nor open on purpose: decide which", pattern)
		}
	}
	for _, pattern := range catalogueWritesHeld {
		if !declared[pattern] {
			t.Errorf("held route %s is not a route (stale entry?)", pattern)
		}
	}
	for pattern := range catalogueWritesOpen {
		if !declared[pattern] {
			t.Errorf("open route %s is not a route (stale entry?)", pattern)
		}
	}
}
