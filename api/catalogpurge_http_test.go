package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// Deleting a catalogue or a product for good (ADR-0469), through
// the whole stack: refused on a server started with --catalogue-purge=false, refused
// to anybody but an administrator, refused without the id repeated — and otherwise the
// catalogue, its products, its orders, the work running for them and the holds
// they granted all gone, with an import of the same document afterwards starting
// from nothing.

type purgePlanResp struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	Enabled      bool     `json:"enabled"`
	Confirm      string   `json:"confirm"`
	Done         bool     `json:"done"`
	Catalogs     []string `json:"catalogs"`
	Releases     []string `json:"releases"`
	Products     []string `json:"products"`
	StrippedFrom []struct {
		Catalog  string   `json:"catalog"`
		Products []string `json:"products"`
	} `json:"strippedFrom"`
	StillOffered []struct {
		Catalog  string   `json:"catalog"`
		Release  string   `json:"release"`
		Products []string `json:"products"`
	} `json:"stillOffered"`
	Orders     []string `json:"orders"`
	KeptOrders []struct {
		Order    string   `json:"order"`
		Products []string `json:"products"`
	} `json:"keptOrders"`
	Instances    int      `json:"instances"`
	Entitlements int      `json:"entitlements"`
	Holders      int      `json:"holders"`
	Processes    []string `json:"processes"`
	Warnings     []string `json:"warnings"`
}

// purgeShop is a catalogue with two products, published, and alice to order for.
type purgeShop struct {
	ts      *httptest.Server
	admin   *http.Client
	alice   *http.Client
	aliceID string
	cat     string
	rel     string
}

func aShopToPurge(t *testing.T, opts ...api.Option) purgeShop {
	t.Helper()
	ts, _ := newAuthServerWith(t, "root", "rootpassword", opts...)
	sh := purgeShop{ts: ts, admin: newClient(t), alice: newClient(t)}
	if login(t, sh.admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, sh.admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","displayName":"Alice Beispiel"}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: %d (%s)", code, body)
	}
	sh.aliceID = idOf(t, body)
	if login(t, sh.alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login failed")
	}

	code, body = cReq(t, sh.admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Testkatalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	sh.cat = idOf(t, body)
	for _, p := range []string{
		`{"id":"vpn","homeCatalog":"` + sh.cat + `","state":"active","texts":{"de":"VPN"},` +
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"laptop","homeCatalog":"` + sh.cat + `","state":"active","texts":{"de":"Laptop"},` +
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	} {
		if code, b := cReq(t, sh.admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, sh.admin, ts, "PATCH", "/api/v1/catalogs/"+sh.cat,
		`{"items":["vpn","laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer the products: %d (%s)", code, b)
	}
	code, body = cReq(t, sh.admin, ts, "POST", "/api/v1/catalogs/"+sh.cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	sh.rel = idOf(t, body)
	return sh
}

func (sh purgeShop) order(t *testing.T, items ...string) string {
	t.Helper()
	code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+sh.rel+`","items":["`+strings.Join(items, `","`)+`"],"recipient":"`+sh.aliceID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return idOf(t, body)
}

func (sh purgeShop) plan(t *testing.T, c *http.Client, method, path string) (int, purgePlanResp, []byte) {
	t.Helper()
	code, body := cReq(t, c, sh.ts, method, path, "")
	var p purgePlanResp
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode the plan: %v (%s)", err, body)
		}
	}
	return code, p, body
}

// aliceHolds answers the products alice holds, as the inventory reads them.
func (sh purgeShop) aliceHolds(t *testing.T) []string {
	t.Helper()
	code, body := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/inventory?principal="+sh.aliceID, "")
	if code != http.StatusOK {
		t.Fatalf("inventory = %d (%s)", code, body)
	}
	var inv struct {
		Items []struct {
			ItemID string `json:"itemId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("decode inventory: %v (%s)", err, body)
	}
	var out []string
	for _, it := range inv.Items {
		out = append(out, it.ItemID)
	}
	return out
}

// TestThePurgeIsRefusedOnAServerThatSwitchedItOff.
//
// The purge is an opt-out, so the switch is what a production server sets to keep
// its orders and holds: started with it, the server refuses the delete — and still
// answers the preview, which says why.
func TestThePurgeIsRefusedOnAServerThatSwitchedItOff(t *testing.T) {
	sh := aShopToPurge(t, api.WithoutCataloguePurge())
	sh.order(t, "vpn")

	code, plan, body := sh.plan(t, sh.admin, "GET", "/api/v1/catalogs/"+sh.cat+"/deletion")
	if code != http.StatusOK {
		t.Fatalf("preview = %d (%s), want 200", code, body)
	}
	if plan.Enabled {
		t.Error("the preview says the purge is allowed on a server started with --catalogue-purge=false")
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "--catalogue-purge=false") {
		t.Errorf("the warnings do not say why it is refused: %v", plan.Warnings)
	}

	code, body = cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalogs/"+sh.cat+"?confirm="+sh.cat, "")
	if code != http.StatusForbidden {
		t.Fatalf("delete = %d (%s), want 403", code, body)
	}
	if !strings.Contains(string(body), "--catalogue-purge=false") {
		t.Errorf("the refusal does not name the switch: %s", body)
	}
	if code, b := cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalog-products/vpn?confirm=vpn", ""); code != http.StatusForbidden {
		t.Fatalf("delete a product = %d (%s), want 403", code, b)
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat, ""); code != http.StatusOK {
		t.Errorf("the refused purge removed the catalogue anyway: %d", code)
	}
}

// TestThePurgeNeedsTheIdRepeated: a delete that does not repeat what it deletes
// deletes nothing.
func TestThePurgeNeedsTheIdRepeated(t *testing.T) {
	sh := aShopToPurge(t)
	for _, path := range []string{
		"/api/v1/catalogs/" + sh.cat,
		"/api/v1/catalogs/" + sh.cat + "?confirm=",
		"/api/v1/catalogs/" + sh.cat + "?confirm=something-else",
		"/api/v1/catalog-products/vpn?confirm=laptop",
	} {
		if code, body := cReq(t, sh.admin, sh.ts, "DELETE", path, ""); code != http.StatusBadRequest {
			t.Errorf("DELETE %s = %d (%s), want 400", path, code, body)
		}
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat, ""); code != http.StatusOK {
		t.Errorf("an unconfirmed purge removed the catalogue: %d", code)
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalog-products/vpn/usage", ""); code != http.StatusOK {
		t.Errorf("an unconfirmed purge removed the product: %d", code)
	}
}

// TestThePurgeIsTheAdministrators: nobody else reads the plan or carries it out,
// on a server that allows it, which is every server not switched off.
func TestThePurgeIsTheAdministrators(t *testing.T) {
	sh := aShopToPurge(t)
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/catalogs/" + sh.cat + "/deletion"},
		{"DELETE", "/api/v1/catalogs/" + sh.cat + "?confirm=" + sh.cat},
		{"GET", "/api/v1/catalog-products/vpn/deletion"},
		{"DELETE", "/api/v1/catalog-products/vpn?confirm=vpn"},
	} {
		if code, body := cReq(t, sh.alice, sh.ts, req.method, req.path, ""); code != http.StatusForbidden {
			t.Errorf("alice %s %s = %d (%s), want 403", req.method, req.path, code, body)
		}
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat, ""); code != http.StatusOK {
		t.Errorf("a refused purge removed the catalogue: %d", code)
	}
}

// TestAnUnknownTargetIsNotFound: there is nothing to preview or delete.
func TestAnUnknownTargetIsNotFound(t *testing.T) {
	sh := aShopToPurge(t)
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/catalogs/cat-nirgends/deletion"},
		{"DELETE", "/api/v1/catalogs/cat-nirgends?confirm=cat-nirgends"},
		{"GET", "/api/v1/catalog-products/nirgends/deletion"},
		{"DELETE", "/api/v1/catalog-products/nirgends?confirm=nirgends"},
	} {
		if code, body := cReq(t, sh.admin, sh.ts, req.method, req.path, ""); code != http.StatusNotFound {
			t.Errorf("%s %s = %d (%s), want 404", req.method, req.path, code, body)
		}
	}
}

// TestPurgingACatalogueRemovesItsOrdersTheirWorkAndTheirHolds.
//
// The case the purge exists for: a catalogue somebody ordered from to watch the
// fulfilment run. Afterwards the catalogue, its products and the order are gone,
// the provisioning still working the order is cancelled, alice holds nothing, and
// the other catalogue that offered one of the products no longer does.
func TestPurgingACatalogueRemovesItsOrdersTheirWorkAndTheirHolds(t *testing.T) {
	sh := aShopToPurge(t, api.WithSystemProcesses())
	ord := sh.order(t, "vpn", "laptop")

	code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs",
		`{"rank":2,"languages":["de"],"texts":{"de":"Anderer Katalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create the other catalogue: %d (%s)", code, body)
	}
	other := idOf(t, body)
	if code, b := cReq(t, sh.admin, sh.ts, "PATCH", "/api/v1/catalogs/"+other, `{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offer vpn in the other catalogue: %d (%s)", code, b)
	}
	_, body = cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+other, "")
	var before struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	if code, b := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/orders/"+ord+"/lines/vpn", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision vpn: %d (%s)", code, b)
	}
	if got := sh.aliceHolds(t); !slices.Contains(got, "vpn") {
		t.Fatalf("alice holds %v before the purge, want vpn", got)
	}
	if code, b := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/deployments", handProvisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	provisioning := startProvisioning(t, sh.ts, sh.admin, ord, "laptop")

	code, plan, body := sh.plan(t, sh.admin, "GET", "/api/v1/catalogs/"+sh.cat+"/deletion")
	if code != http.StatusOK {
		t.Fatalf("preview = %d (%s)", code, body)
	}
	// A server started with no option allows it: the purge is an opt-out.
	if !plan.Enabled || plan.Done || plan.Confirm != sh.cat {
		t.Errorf("enabled/done/confirm = %v/%v/%q, want a default server to allow the purge",
			plan.Enabled, plan.Done, plan.Confirm)
	}
	if !slices.Equal(plan.Catalogs, []string{sh.cat}) || !slices.Equal(plan.Releases, []string{sh.rel}) {
		t.Errorf("catalogs %v, releases %v", plan.Catalogs, plan.Releases)
	}
	if !slices.Equal(plan.Products, []string{"laptop", "vpn"}) {
		t.Errorf("products = %v, want both of the catalogue's own", plan.Products)
	}
	if !slices.Equal(plan.Orders, []string{ord}) {
		t.Errorf("orders = %v, want %s", plan.Orders, ord)
	}
	if len(plan.StrippedFrom) != 1 || plan.StrippedFrom[0].Catalog != other ||
		!slices.Equal(plan.StrippedFrom[0].Products, []string{"vpn"}) {
		t.Errorf("strippedFrom = %+v, want vpn out of %s", plan.StrippedFrom, other)
	}
	if plan.Entitlements != 1 || plan.Holders != 1 {
		t.Errorf("entitlements/holders = %d/%d, want 1/1", plan.Entitlements, plan.Holders)
	}
	if plan.Instances < 1 {
		t.Errorf("instances = %d, want at least the provisioning", plan.Instances)
	}
	if !slices.Equal(plan.Processes, []string{"deprov", "prov"}) {
		t.Errorf("processes = %v", plan.Processes)
	}
	if len(plan.Warnings) == 0 {
		t.Error("a purge that deletes an order and a hold carries no warning")
	}

	code, done, body := sh.plan(t, sh.admin, "DELETE", "/api/v1/catalogs/"+sh.cat+"?confirm="+sh.cat)
	if code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", code, body)
	}
	if !done.Done || !slices.Equal(done.Orders, []string{ord}) {
		t.Errorf("the answer = %s", body)
	}

	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat, ""); code != http.StatusNotFound {
		t.Errorf("the catalogue is still there: %d", code)
	}
	for _, p := range []string{"vpn", "laptop"} {
		if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalog-products/"+p+"/usage", ""); code != http.StatusNotFound {
			t.Errorf("product %s is still there: %d", p, code)
		}
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/orders/"+ord, ""); code != http.StatusNotFound {
		t.Errorf("the order is still there: %d", code)
	}
	if got := sh.aliceHolds(t); len(got) != 0 {
		t.Errorf("alice still holds %v", got)
	}
	if stillRunning(t, sh.ts, sh.admin, provisioning) {
		t.Error("the provisioning of a deleted order is still running")
	}

	code, body = cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+other, "")
	if code != http.StatusOK {
		t.Fatalf("the other catalogue = %d (%s)", code, body)
	}
	var after struct {
		Items    []string `json:"items"`
		Revision int64    `json:"revision"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(after.Items) != 0 {
		t.Errorf("the other catalogue still offers %v", after.Items)
	}
	if after.Revision <= before.Revision {
		t.Errorf("stripping the other catalogue left its revision at %d (was %d), so a maintainer "+
			"holding the old one would put the deleted product back", after.Revision, before.Revision)
	}

	// Run again, it finds nothing: the catalogue went last, and it is gone.
	if code, b := cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalogs/"+sh.cat+"?confirm="+sh.cat, ""); code != http.StatusNotFound {
		t.Errorf("a second purge = %d (%s), want 404", code, b)
	}
}

// TestPurgingAProductKeepsTheOrdersThatCarryOthers.
//
// An order with another product is somebody's order of that other product too, so
// it stays; the line on the deleted product is withdrawn, saying why.
func TestPurgingAProductKeepsTheOrdersThatCarryOthers(t *testing.T) {
	sh := aShopToPurge(t)
	both := sh.order(t, "vpn", "laptop")
	only := sh.order(t, "laptop")

	code, plan, body := sh.plan(t, sh.admin, "GET", "/api/v1/catalog-products/laptop/deletion")
	if code != http.StatusOK {
		t.Fatalf("preview = %d (%s)", code, body)
	}
	if len(plan.Catalogs) != 0 || len(plan.Releases) != 0 || !slices.Equal(plan.Products, []string{"laptop"}) {
		t.Errorf("catalogs %v, releases %v, products %v", plan.Catalogs, plan.Releases, plan.Products)
	}
	if !slices.Equal(plan.Orders, []string{only}) {
		t.Errorf("orders = %v, want only the order of nothing but the laptop (%s)", plan.Orders, only)
	}
	if len(plan.KeptOrders) != 1 || plan.KeptOrders[0].Order != both {
		t.Errorf("keptOrders = %+v, want %s", plan.KeptOrders, both)
	}
	if len(plan.StrippedFrom) != 1 || plan.StrippedFrom[0].Catalog != sh.cat {
		t.Errorf("strippedFrom = %+v, want its own catalogue", plan.StrippedFrom)
	}
	if len(plan.StillOffered) != 1 || plan.StillOffered[0].Release != sh.rel {
		t.Errorf("stillOffered = %+v, want the published release %s", plan.StillOffered, sh.rel)
	}

	if code, b := cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalog-products/laptop?confirm=laptop", ""); code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", code, b)
	}
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/orders/"+only, ""); code != http.StatusNotFound {
		t.Errorf("the order of nothing but the laptop is still there: %d", code)
	}
	code, body = cReq(t, sh.admin, sh.ts, "GET", "/api/v1/orders/"+both, "")
	if code != http.StatusOK {
		t.Fatalf("the order with the VPN = %d (%s), want it kept", code, body)
	}
	var kept struct {
		Lines []struct {
			ItemID string `json:"itemId"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &kept); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, l := range kept.Lines {
		switch l.ItemID {
		case "laptop":
			if l.Status != "cancelled" || l.Reason == "" {
				t.Errorf("the laptop line is %q (%q), want cancelled with a reason", l.Status, l.Reason)
			}
		case "vpn":
			if l.Status == "cancelled" {
				t.Error("deleting the laptop withdrew the VPN")
			}
		}
	}
	code, body = cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat, "")
	if code != http.StatusOK || strings.Contains(string(body), `"laptop"`) {
		t.Errorf("the catalogue = %d and still names the laptop: %s", code, body)
	}
}

// TestAPurgedCatalogueImportsAgainAsNew.
//
// Import, try, delete, import again: the second import meets a catalogue and a
// product this server no longer has, and the import's memory of the first must not
// make it ask whether they were deleted on purpose.
func TestAPurgedCatalogueImportsAgainAsNew(t *testing.T) {
	sh := aShopToPurge(t)
	doc := `{"publish":true,
	  "catalogs":[{"id":"cat-test","texts":{"de":"Test"},"rank":7,"languages":["de"],"items":["badge"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-test","state":"active","texts":{"de":"Badge"},
	    "approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}]}`
	importIt := func() []string {
		code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs/import", doc)
		if code != http.StatusOK {
			t.Fatalf("import = %d (%s)", code, body)
		}
		var res struct {
			Created []string `json:"created"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		slices.Sort(res.Created)
		return res.Created
	}
	want := []string{"catalog:cat-test", "product:badge"}
	if got := importIt(); !slices.Equal(got, want) {
		t.Fatalf("first import created %v, want %v", got, want)
	}
	if code, b := cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalogs/cat-test?confirm=cat-test", ""); code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", code, b)
	}
	if got := importIt(); !slices.Equal(got, want) {
		t.Errorf("the import after the purge created %v, want %v again", got, want)
	}
}

// TestAPurgeForgetsWhatTheImportBrought.
//
// The import remembers what it last brought, to tell a record edited here from one
// the document moved on from. After a purge that memory describes records that no
// longer exist. Kept, it would make a product somebody later wrote here by hand,
// under the same id, look like an edit of the imported one — and the next import
// would keep it without asking. Forgotten, the import asks.
func TestAPurgeForgetsWhatTheImportBrought(t *testing.T) {
	sh := aShopToPurge(t)
	doc := `{"catalogs":[{"id":"cat-test","texts":{"de":"Test"},"rank":7,"languages":["de"],"items":["badge"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-test","state":"active","texts":{"de":"Badge"},
	    "approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}]}`
	if code, b := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs/import", doc); code != http.StatusOK {
		t.Fatalf("import = %d (%s)", code, b)
	}
	if code, b := cReq(t, sh.admin, sh.ts, "DELETE", "/api/v1/catalogs/cat-test?confirm=cat-test", ""); code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", code, b)
	}
	byHand := `{"id":"badge","homeCatalog":"` + sh.cat + `","state":"draft","texts":{"de":"Von Hand"},` +
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`
	if code, b := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalog-products", byHand); code != http.StatusOK {
		t.Fatalf("write badge by hand: %d (%s)", code, b)
	}

	code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs/import?dryRun=true", doc)
	if code != http.StatusOK && code != http.StatusConflict {
		t.Fatalf("dry run = %d (%s)", code, body)
	}
	var res struct {
		Artifacts []struct {
			Ref    string `json:"ref"`
			Status string `json:"status"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	seen := false
	for _, a := range res.Artifacts {
		if a.Ref != "product:badge" {
			continue
		}
		seen = true
		if a.Status != "changed" {
			t.Errorf("the hand-written badge reads as %q, want changed: the import must ask "+
				"rather than keep it as an edit of a product that was deleted", a.Status)
		}
	}
	if !seen {
		t.Errorf("the dry run does not name product:badge: %s", body)
	}
}
