package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Time-bounded rights, end to end (ADR-0344).
//
// The path that matters is the one through an order, because that is the only path
// that may produce an end: the ceiling reaches a grant through the release, and
// nothing else grants under a release.

// readExpiring asks what is due within a window.
func readExpiring(t *testing.T, c *http.Client, ts *httptest.Server, query string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/entitlements/expiring"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET expiring: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}

// aBoundedProductOrdered publishes one product carrying a ceiling, orders it, and
// drives the line to done so the grant is written. It returns the order id.
func aBoundedProductOrdered(t *testing.T, c *http.Client, ts *httptest.Server, maxDays int) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Standard"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}

	product := fmt.Sprintf(`{"id":"vpn","homeCatalog":%q,"state":"active","texts":{"de":"VPN"},`+
		`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov",`+
		`"maxDays":%d}`, cat.ID, maxDays)
	if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d %s", code, b)
	}
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d %s", code, b)
	}
	code, body = cReq(t, c, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d %s", code, body)
	}
	var rel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v", err)
	}

	code, body = cReq(t, c, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel.ID+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d %s", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	if code, b := cReq(t, c, ts, "POST",
		"/api/v1/orders/"+ord.ID+"/lines/vpn", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d %s", code, b)
	}
	return ord.ID
}

// TestARightGrantedUnderACeilingCarriesItsEnd.
//
// The ceiling is declared on the product, frozen into the line by the release, and
// turned into a date when the right is granted. Every one of those steps is
// somewhere a ceiling could be silently dropped, and the only way to see that it
// was not is to look at what the inventory ended up holding.
func TestARightGrantedUnderACeilingCarriesItsEnd(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	aBoundedProductOrdered(t, c, ts, 90)

	rep := readExpiring(t, c, ts, "?within=120")
	counts, _ := rep["counts"].(map[string]any)
	if counts["withAnEnd"] != float64(1) {
		t.Fatalf("counts = %+v, want the one granted right to carry an end. The ceiling "+
			"travels product → release → line → grant, and any of those dropping it looks "+
			"like this", counts)
	}
	if counts["due"] != float64(1) || counts["overdue"] != float64(0) {
		t.Errorf("counts = %+v, want it due and not yet overdue", counts)
	}

	rights, _ := rep["rights"].([]any)
	if len(rights) != 1 {
		t.Fatalf("%d right(s) listed, want 1", len(rights))
	}
	row, _ := rights[0].(map[string]any)
	if row["origin"] != "ordered" {
		t.Errorf("origin = %v, want ordered — it is the only origin that ever carries an end",
			row["origin"])
	}
	if days, _ := row["days"].(float64); days > -89 || days < -90 {
		t.Errorf("days = %v, want about -90: negative while the end is still ahead", days)
	}
	if row["returnable"] != true {
		t.Errorf("returnable = %v; a row that does not say its order line is still there "+
			"is a row nothing can act on", row["returnable"])
	}

	// A narrower window does not conclude that it is not due — it simply does not
	// answer about it. The denominator stays, which is what says so.
	near := readExpiring(t, c, ts, "?within=7")
	counts, _ = near["counts"].(map[string]any)
	if counts["due"] != float64(0) {
		t.Errorf("a seven-day window reported %v due", counts["due"])
	}
	if counts["withAnEnd"] != float64(1) {
		t.Errorf("counts = %+v; the window narrows the answer and not the estate, so the "+
			"denominator has to survive it", counts)
	}
}

// TestACeilingNeverReachesARightTheLoadFound.
//
// The sharpest rule in the record. A commissioning load records a found right's
// start as the moment it was *found*, so applying the product's ceiling to it would
// schedule an entire estate to expire on the anniversary of the day somebody
// switched the portal on — a mass deprovisioning computed from a date that was
// never a start date.
func TestACeilingNeverReachesARightTheLoadFound(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	// The same product, with a ceiling and a target reference, so a load can
	// attribute a found right to it.
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Standard"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	product := fmt.Sprintf(`{"id":"vpn","homeCatalog":%q,"state":"active","texts":{"de":"VPN"},`+
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d",`+
		`"maxDays":90,"targets":[{"system":"ad","ref":"CN=VPN-Users"}]}`, cat.ID)
	if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d %s", code, b)
	}
	anAccountWithMail(t, c, ts, "ada", "ada@example.org")

	rep := postLoad(t, c, ts, `{"apply":true,"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)
	if rep["applied"] != true {
		t.Fatalf("the inventory was not filled: %+v", rep)
	}

	exp := readExpiring(t, c, ts, "?within=365")
	counts, _ := exp["counts"].(map[string]any)
	if counts["withAnEnd"] != float64(0) {
		t.Fatalf("a right the load found carries an end: %+v. Its start is the moment it was "+
			"found, so a ceiling measured from it would expire an estate on the anniversary "+
			"of its own commissioning", exp)
	}
	note, _ := exp["note"].(string)
	if !strings.Contains(note, "adopted or legacy") {
		t.Errorf("note = %q, want it to say why a loaded estate reads empty here", note)
	}
}

// TestTheWindowIsAQuestionAndNotAScope.
//
// Unlike a reconciliation's scope, which is a promise and has no default, this is
// only how far ahead to look. It has a default, it takes zero, and it is bounded —
// a window wide enough to cover every right with an end is a list of the whole
// inventory, which is a different route's job.
func TestTheWindowIsAQuestionAndNotAScope(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aBoundedProductOrdered(t, c, ts, 90)

	// No window at all is answerable, which a reconciliation's scope is not.
	rep := readExpiring(t, c, ts, "")
	if rep["within"] != float64(30) {
		t.Errorf("within = %v with no window given, want the default", rep["within"])
	}

	// Zero answers what is already overdue and nothing else.
	rep = readExpiring(t, c, ts, "?within=0")
	counts, _ := countsOfExpiring(t, rep)
	if counts["due"] != float64(0) || counts["overdue"] != float64(0) {
		t.Errorf("counts = %+v for a zero window over an estate with nothing overdue", counts)
	}

	for _, bad := range []string{"?within=-1", "?within=soon", "?within=100000"} {
		code, body := cReq(t, c, ts, "GET", "/api/v1/entitlements/expiring"+bad, "")
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400: %s", bad, code, body)
		}
	}
}

func countsOfExpiring(t *testing.T, rep map[string]any) (map[string]any, bool) {
	t.Helper()
	c, ok := rep["counts"].(map[string]any)
	if !ok {
		t.Fatalf("no counts in %+v", rep)
	}
	return c, true
}

// TestACampaignSaysWhichRowsEndByThemselves.
//
// The composition with the slice before this one. A right that ends in three weeks
// on its own is not what a quarterly review is for, and the count says how much of
// the campaign a ceiling on the product would have removed.
func TestACampaignSaysWhichRowsEndByThemselves(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aBoundedProductOrdered(t, c, ts, 90)

	opened := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	rows := rowsOf(t, opened)
	if len(rows) != 1 {
		t.Fatalf("%d row(s), want the one granted right", len(rows))
	}
	if rows[0]["until"] == nil {
		t.Error("the row does not carry the right's own end, so a reviewer spends attention " +
			"on a question that answers itself")
	}
	if countsOf(t, opened)["ending"] != float64(1) {
		t.Errorf("counts = %+v, want the row counted as ending by itself", countsOf(t, opened))
	}
}
