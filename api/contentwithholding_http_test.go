package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// The quarantine is a security guarantee, so it is asserted on every surface
// outside the catalogue screen that shows anything of a product: the shop's
// listing and detail (the releases), its keyword search (which runs over the
// releases' keywords), the picture route, the approval page, the order views, the
// shop's task list and the export. Each is read after a recheck quarantined the
// product, as the person who would read it, and none may carry the description
// or a keyword. The name and the price stay.
// (ADR-draft-service-content-from-an-external-source, § 9.)

const (
	wikiText    = "Sicherer Zugang"
	wikiKeyword = "fernzugriff"
)

func boundWiki(t *testing.T) *wikifake.Server {
	t.Helper()
	f := wikifake.New()
	t.Cleanup(f.Close)
	f.Put(wikifake.Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	f.Put(wikifake.Page{ID: "it_de", Key: "SVC-12", Space: "SVC", Parent: "it_root", Title: "VPN",
		Labels: []string{wikiKeyword}, Properties: map[string]any{"bild": "logo.png"},
		Body: []wikifake.Block{{Kind: "paragraph", Text: wikiText + " für alle."}}})
	f.Attach("it_de", wikifake.Attachment{ID: "att_1", Name: "logo.png", Type: "image/png", Data: tinyPNG})
	return f
}

// tinyPNG is a picture the picture store accepts.
var tinyPNG = []byte("\x89PNG\r\n\x1a\n" + "bytes")

func mustCode(t *testing.T, what string, want, code int, body []byte) {
	t.Helper()
	if code != want {
		t.Fatalf("%s: %d (%s), want %d", what, code, body, want)
	}
}

func withholds(t *testing.T, surface string, body []byte) {
	t.Helper()
	if strings.Contains(string(body), wikiText) || strings.Contains(string(body), wikiKeyword) {
		t.Errorf("%s shows quarantined source content: %s", surface, body)
	}
}

func TestNoSourceConfiguredChangesNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login")
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/content-sources", "")
	mustCode(t, "list sources", 200, code, body)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("sources = %s", body)
	}
}

func TestAQuarantinedProductIsWithheldOnEverySurface(t *testing.T) {
	f := boundWiki(t)
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithContentTransport(f.TS.Client().Transport))
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]

	// The administrator configures the source; the token is in the vault.
	code, body := cReq(t, admin, ts, "PUT", "/api/v1/secrets/wiki-token", `{"value":"`+wikifake.Token+`"}`)
	mustCode(t, "store token", 200, code, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/content-sources", `{"id":"wiki","name":"Wiki","kind":"wikiwikibangbang",`+
		`"baseURL":"`+f.URL()+`","credentialsRef":"wiki-token","pictureField":"bild"}`)
	mustCode(t, "create source", 201, code, body)
	if strings.Contains(string(body), wikifake.Token) {
		t.Fatal("the source's answer carries the token")
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/content-sources/wiki/check", "")
	if code != 200 || !strings.Contains(string(body), `"reachable":true`) {
		t.Fatalf("check: %d %s", code, body)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`)
	mustCode(t, "create catalogue", 201, code, body)
	var cat struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &cat)
	product := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN-Zugang"},` +
		`"approval":{"kind":"kunden-genehmigung","ref":"alice"},"provisionProcess":"prov","deprovisionProcess":"deprov","price":"CHF 12"}`
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product)
	mustCode(t, "save product", 200, code, body)
	code, body = cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID, `{"items":["vpn"]}`)
	mustCode(t, "offer", 200, code, body)

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products/vpn/content", `{"source":"wiki","pages":{"de":"SVC-12"}}`)
	mustCode(t, "link", 200, code, body)
	if !strings.Contains(string(body), wikiText) {
		t.Fatalf("link wrote no description: %s", body)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	mustCode(t, "publish", 201, code, body)
	var rel struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &rel)
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != 200 || !strings.Contains(string(body), wikiText) || !strings.Contains(string(body), wikiKeyword) {
		t.Fatalf("the release does not show the content before the quarantine: %s", body)
	}
	code, _ = cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/picture", "")
	mustCode(t, "picture before", 200, code, nil)

	// An order and its approval, as the fulfilment model would start it.
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel.ID+`","items":["vpn"]}`)
	mustCode(t, "order", 201, code, body)
	var ord struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &ord)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/deployments", ownApprovalBPMN)
	mustCode(t, "deploy approval", 200, code, body)
	start := fmt.Sprintf(`{"processId":"kunden-genehmigung","variables":{"orderId":%q,"itemId":"vpn","recipient":"root","orderer":"root"}}`, ord.ID)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/instances", start)
	mustCode(t, "start approval", 200, code, body)

	// The product owner restricts the page's parent. The refresh rechecks first,
	// records the quarantine, and is refused itself.
	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"@staff"} })
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products/vpn/content/refresh", "")
	mustCode(t, "refresh", http.StatusUnprocessableEntity, code, body)
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/content", "")
	if code != 200 || !strings.Contains(string(body), `"quarantine"`) {
		t.Fatalf("status: %d %s", code, body)
	}

	// The shop: listing, detail and keyword search all read the release.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	mustCode(t, "releases", 200, code, body)
	withholds(t, "the shop's release", body)
	if !strings.Contains(string(body), "VPN-Zugang") || !strings.Contains(string(body), "CHF 12") {
		t.Errorf("the name or the price was withheld too: %s", body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/picture", "")
	mustCode(t, "picture", http.StatusNotFound, code, body)
	// The approval page.
	code, body = cReq(t, alice, ts, "GET", "/api/v1/approvals", "")
	mustCode(t, "approvals", 200, code, body)
	if !strings.Contains(string(body), "VPN-Zugang") {
		t.Fatalf("alice sees no approval: %s", body)
	}
	withholds(t, "the approval page", body)
	// The order views and the shop's tasks.
	for _, path := range []string{"/api/v1/orders", "/api/v1/orders/" + ord.ID, "/api/v1/shop/tasks"} {
		code, body = cReq(t, admin, ts, "GET", path, "")
		mustCode(t, path, 200, code, body)
		withholds(t, path, body)
	}
	code, body = cReq(t, alice, ts, "GET", "/api/v1/shop/tasks", "")
	mustCode(t, "alice's tasks", 200, code, body)
	withholds(t, "alice's shop tasks", body)
	// The exports.
	for _, path := range []string{"/api/v1/catalogs/" + cat.ID + "/document", "/api/v1/catalog-products/document?ids=vpn"} {
		code, body = cReq(t, admin, ts, "GET", path, "")
		mustCode(t, path, 200, code, body)
		withholds(t, path, body)
	}
	// And publishing refuses.
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	mustCode(t, "publish while quarantined", http.StatusUnprocessableEntity, code, body)

	// Removing a source a product is bound to is refused.
	code, body = cReq(t, admin, ts, "DELETE", "/api/v1/content-sources/wiki", "")
	mustCode(t, "delete source", http.StatusConflict, code, body)
}

// The recheck runs without anybody acting: linking watches the page and its
// ancestors, and a restriction set on an ancestor afterwards quarantines the
// product through the change feed, at once.
func TestTheRecheckQuarantinesWithoutAnybodyActing(t *testing.T) {
	f := boundWiki(t)
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithContentTransport(f.TS.Client().Transport))
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "PUT", "/api/v1/secrets/wiki-token", `{"value":"`+wikifake.Token+`"}`)
	mustCode(t, "store token", 200, code, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/content-sources", `{"id":"wiki","name":"Wiki","kind":"wikiwikibangbang",`+
		`"baseURL":"`+f.URL()+`","credentialsRef":"wiki-token"}`)
	mustCode(t, "create source", 201, code, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"]}`)
	mustCode(t, "create catalogue", 201, code, body)
	var cat struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &cat)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products", `{"id":"vpn","homeCatalog":"`+cat.ID+
		`","state":"active","texts":{"de":"VPN"},"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`)
	mustCode(t, "save product", 200, code, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products/vpn/content", `{"source":"wiki","pages":{"de":"SVC-12"}}`)
	mustCode(t, "link", 200, code, body)

	eventually(t, func() bool {
		w := f.Watched()
		return len(w) == 2 && w[0] == "it_de" && w[1] == "it_root"
	})
	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"@staff"} })
	eventually(t, func() bool {
		_, body := cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/content", "")
		return strings.Contains(string(body), `"quarantine"`)
	})
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
