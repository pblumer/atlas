package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The product manager's delivery report, over HTTP
// (ADR-0515).
//
// The measuring has its own tests in api/servicereport. This is the route: that a
// promise written on a product reaches the report through publish, order and
// provisioning, that the answer names nobody, and that the CSV is the same report.

type deliveryReportResp struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Catalogs []string `json:"catalogs"`
	Products []struct {
		ItemID       string   `json:"itemId"`
		Name         string   `json:"name"`
		ServiceLevel string   `json:"serviceLevel"`
		DeliveryDays int      `json:"deliveryDays"`
		Ordered      int      `json:"ordered"`
		Delivered    int      `json:"delivered"`
		Met          int      `json:"met"`
		Open         int      `json:"open"`
		Held         int      `json:"held"`
		MetShare     *float64 `json:"metShare"`
	} `json:"products"`
	Totals struct {
		Ordered int `json:"ordered"`
		Met     int `json:"met"`
	} `json:"totals"`
}

// aPromisingCatalogueWithAnOrder publishes one product promising delivery within
// three working days, orders it and answers the catalogue and order ids.
func aPromisingCatalogueWithAnOrder(t *testing.T, ts interface{ Close() }, do func(method, path, body string) (int, []byte)) (string, string) {
	t.Helper()
	code, body := do("POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct{ ID string }
	_ = json.Unmarshal(body, &cat)
	product := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN-Zugang"},` +
		`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov",` +
		`"serviceLevel":{"name":"Standard","deliveryDays":3}}`
	if code, b := do("POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := do("PATCH", "/api/v1/catalogs/"+cat.ID, `{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offer product: %d (%s)", code, b)
	}
	code, body = do("POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct{ ID string }
	_ = json.Unmarshal(body, &rel)
	code, body = do("POST", "/api/v1/orders", `{"releaseId":"`+rel.ID+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d (%s)", code, body)
	}
	var ord struct{ ID string }
	_ = json.Unmarshal(body, &ord)
	return cat.ID, ord.ID
}

func TestTheDeliveryReportMeasuresAPromiseAndNamesNobody(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	do := func(method, path, body string) (int, []byte) { return cReq(t, admin, ts, method, path, body) }
	catID, orderID := aPromisingCatalogueWithAnOrder(t, ts, do)

	read := func() deliveryReportResp {
		t.Helper()
		code, body := do("GET", "/api/v1/catalog-products/delivery-report", "")
		if code != http.StatusOK {
			t.Fatalf("report = %d (%s)", code, body)
		}
		var rep deliveryReportResp
		if err := json.Unmarshal(body, &rep); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if strings.Contains(string(body), "root") {
			t.Errorf("the product manager's report names a person: %s", body)
		}
		return rep
	}

	before := read()
	if len(before.Catalogs) != 1 || before.Catalogs[0] != catID {
		t.Errorf("catalogs = %v, want the one maintained", before.Catalogs)
	}
	if len(before.Products) != 1 || before.Products[0].Open != 1 || before.Products[0].Delivered != 0 {
		t.Fatalf("before provisioning: %+v, want one open position", before.Products)
	}

	if code, b := do("POST", fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}
	after := read()
	p := after.Products[0]
	if p.ItemID != "vpn" || p.Name != "VPN-Zugang" || p.ServiceLevel != "Standard" || p.DeliveryDays != 3 ||
		p.Ordered != 1 || p.Delivered != 1 || p.Met != 1 || p.Held != 1 || p.MetShare == nil || *p.MetShare != 1 {
		t.Errorf("after provisioning: %+v, want one position delivered and met", p)
	}
	if after.Totals.Ordered != 1 || after.Totals.Met != 1 {
		t.Errorf("totals = %+v", after.Totals)
	}

	code, body := do("GET", "/api/v1/catalog-products/delivery-report?format=csv", "")
	csvText := strings.TrimPrefix(string(body), "\ufeff")
	if code != http.StatusOK || !strings.HasPrefix(csvText, "item;name;") || !strings.Contains(string(body), "vpn;VPN-Zugang;Standard;3;1;1;1;") {
		t.Errorf("csv = %d:\n%s", code, body)
	}

	// A window that ends before the order was placed holds nothing ordered, and the
	// right is still counted as held: holding is now, ordering is in a period.
	code, body = do("GET", "/api/v1/catalog-products/delivery-report?from=2001-01-01&to=2001-12-31", "")
	if code != http.StatusOK {
		t.Fatalf("windowed = %d (%s)", code, body)
	}
	var old deliveryReportResp
	_ = json.Unmarshal(body, &old)
	if old.From != "2001-01-01" || old.To != "2001-12-31" || len(old.Products) != 1 ||
		old.Products[0].Ordered != 0 || old.Products[0].Held != 1 {
		t.Errorf("windowed: %+v", old)
	}

	for _, bad := range []string{"?from=gestern", "?to=2001-13-01", "?from=2002-01-01&to=2001-01-01", "?catalog=gibt-es-nicht"} {
		if code, b := do("GET", "/api/v1/catalog-products/delivery-report"+bad, ""); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Errorf("%s = %d (%s), want a refusal", bad, code, b)
		}
	}
	if code, _ := do("GET", "/api/v1/catalog-products/delivery-report?catalog="+catID, ""); code != http.StatusOK {
		t.Errorf("narrowing to the maintained catalogue = %d", code)
	}
}

// A product manager who maintains nothing gets an empty report, not somebody
// else's: the scope is the catalogues the caller may edit.
func TestTheDeliveryReportIsScopedToWhatTheCallerMaintains(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	do := func(method, path, body string) (int, []byte) { return cReq(t, admin, ts, method, path, body) }
	aPromisingCatalogueWithAnOrder(t, ts, do)

	if code, b := do("POST", "/api/v1/users", `{"username":"pm","password":"pmpassword1","roles":["productmanager"]}`); code != http.StatusCreated {
		t.Fatalf("create pm: %d (%s)", code, b)
	}
	pm := newClient(t)
	if login(t, pm, ts, "pm", "pmpassword1") != http.StatusOK {
		t.Fatal("pm login failed")
	}
	code, body := cReq(t, pm, ts, "GET", "/api/v1/catalog-products/delivery-report", "")
	if code != http.StatusOK {
		t.Fatalf("report = %d (%s)", code, body)
	}
	var rep deliveryReportResp
	_ = json.Unmarshal(body, &rep)
	if len(rep.Catalogs) != 0 || len(rep.Products) != 0 {
		t.Errorf("a product manager who maintains nothing sees %+v", rep)
	}
}
