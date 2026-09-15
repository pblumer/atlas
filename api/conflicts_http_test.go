package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Incompatible rights, end to end (ADR-0342).
//
// Two halves have to hold, and they are different claims: an order that would
// create a forbidden combination is refused, and a combination that already exists
// is reported the day the rule is declared.

func readConflicts(t *testing.T, c *http.Client, ts *httptest.Server) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/conflicts", "")
	if code != http.StatusOK {
		t.Fatalf("GET conflicts: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}

// anExcludingCatalogue publishes two products that must not be held together and
// returns the release id.
func anExcludingCatalogue(t *testing.T, c *http.Client, ts *httptest.Server) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Finanzen"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, id := range []string{"create-supplier", "approve-payment"} {
		product := fmt.Sprintf(`{"id":%q,"homeCatalog":%q,"state":"active","texts":{"de":%q},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`,
			id, cat.ID, id)
		if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save %s: %d %s", id, code, b)
		}
	}
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["create-supplier","approve-payment"],`+
			`"edges":[{"from":"create-supplier","to":"approve-payment","kind":"excludes"}]}`,
	); code != http.StatusOK {
		t.Fatalf("declare the incompatibility: %d %s", code, b)
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
	return rel.ID
}

func placeOrder(t *testing.T, c *http.Client, ts *httptest.Server, release string, items ...string) (int, []byte) {
	t.Helper()
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return cReq(t, c, ts, "POST", "/api/v1/orders",
		fmt.Sprintf(`{"releaseId":%q,"items":[%s]}`, release, strings.Join(quoted, ",")))
}

// TestAnOrderThatWouldCreateAForbiddenCombinationIsRefused.
//
// Refused rather than granted-and-detected, which is the whole point of a
// preventive control: detection means the forbidden combination exists in the real
// world for as long as detection takes.
func TestAnOrderThatWouldCreateAForbiddenCombinationIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	rel := anExcludingCatalogue(t, c, ts)

	// Both halves in one basket.
	code, body := placeOrder(t, c, ts, rel, "create-supplier", "approve-payment")
	if code != http.StatusConflict {
		t.Fatalf("both halves in one order: %d %s, want 409", code, body)
	}
	for _, want := range []string{"create-supplier", "approve-payment"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refusal does not name %q. A refusal without the other half sends "+
				"somebody to ask why: %s", want, body)
		}
	}
	if !strings.Contains(string(body), "Neither is wrong on its own") {
		t.Errorf("the refusal blames one of them: %s", body)
	}

	// One half alone is fine.
	if code, b := placeOrder(t, c, ts, rel, "create-supplier"); code != http.StatusCreated {
		t.Fatalf("one half alone: %d %s, want it placed", code, b)
	}
	if code, b := cReq(t, c, ts, "POST",
		"/api/v1/orders/"+firstOrderID(t, c, ts)+"/lines/create-supplier",
		`{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision the first half: %d %s", code, b)
	}

	// And now the other half, against what is held.
	code, body = placeOrder(t, c, ts, rel, "approve-payment")
	if code != http.StatusConflict {
		t.Fatalf("the second half against a held first: %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), "already") {
		t.Errorf("the refusal does not say which side is already held: %s", body)
	}
}

// firstOrderID reads the caller's newest order.
func firstOrderID(t *testing.T, c *http.Client, ts *httptest.Server) string {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/orders", "")
	if code != http.StatusOK {
		t.Fatalf("GET orders: %d %s", code, raw)
	}
	var orders []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &orders); err != nil {
		t.Fatalf("decode orders: %v (%s)", err, raw)
	}
	if len(orders) == 0 {
		t.Fatal("no orders")
	}
	return orders[0].ID
}

// TestARuleAppliesToWhatPeopleAlreadyHold.
//
// The deliberate opposite of the expiry ceiling, which never reaches a right
// granted before it was declared. An expiry is part of what was granted; a conflict
// is a statement about what may coexist now, and a rule that did not apply to the
// estate the day it was written would be decoration.
func TestARuleAppliesToWhatPeopleAlreadyHold(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	// A catalogue with the two products and *no* rule yet, so both can be ordered.
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Finanzen"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, id := range []string{"create-supplier", "approve-payment"} {
		product := fmt.Sprintf(`{"id":%q,"homeCatalog":%q,"state":"active","texts":{"de":%q},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`,
			id, cat.ID, id)
		if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save %s: %d %s", id, code, b)
		}
	}
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["create-supplier","approve-payment"]}`); code != http.StatusOK {
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

	// Both ordered and provisioned while nothing forbade it.
	if code, b := placeOrder(t, c, ts, rel.ID, "create-supplier", "approve-payment"); code != http.StatusCreated {
		t.Fatalf("order both before any rule: %d %s", code, b)
	}
	id := firstOrderID(t, c, ts)
	for _, item := range []string{"create-supplier", "approve-payment"} {
		if code, b := cReq(t, c, ts, "POST",
			"/api/v1/orders/"+id+"/lines/"+item, `{"status":"done"}`); code != http.StatusOK {
			t.Fatalf("provision %s: %d %s", item, code, b)
		}
	}

	// Nothing forbidden yet, and the answer says which empty it is.
	rep := readConflicts(t, c, ts)
	if rep["pairs"] != float64(0) {
		t.Fatalf("pairs = %v before any rule", rep["pairs"])
	}
	if note, _ := rep["note"].(string); !strings.Contains(note, "declares an incompatibility") {
		t.Errorf("note = %q; an estate with no rules and a clean estate read the same "+
			"without it", note)
	}

	// The rule is declared and published now — after the fact.
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["create-supplier","approve-payment"],`+
			`"edges":[{"from":"approve-payment","to":"create-supplier","kind":"excludes"}]}`,
	); code != http.StatusOK {
		t.Fatalf("declare: %d %s", code, b)
	}
	if code, b := cReq(t, c, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", ""); code != http.StatusCreated {
		t.Fatalf("publish the rule: %d %s", code, b)
	}

	rep = readConflicts(t, c, ts)
	if rep["pairs"] != float64(1) {
		t.Errorf("pairs = %v, want the one declared rule counted once rather than once per "+
			"direction", rep["pairs"])
	}
	counts, _ := rep["counts"].(map[string]any)
	if counts["findings"] != float64(1) || counts["people"] != float64(1) {
		t.Fatalf("counts = %+v, want the pre-existing combination found the day the rule "+
			"was published", counts)
	}
	findings, _ := rep["findings"].([]any)
	f, _ := findings[0].(map[string]any)
	if f["a"] != "approve-payment" || f["b"] != "create-supplier" {
		t.Errorf("finding = %v/%v, want both halves named and sorted", f["a"], f["b"])
	}
}

// TestAProductCannotExcludeItself — holding it once would be a violation.
func TestAProductCannotExcludeItself(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Finanzen"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", fmt.Sprintf(
		`{"id":"vpn","homeCatalog":%q,"state":"active","texts":{"de":"VPN"},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`,
		cat.ID)); code != http.StatusOK {
		t.Fatalf("save product: %d %s", code, b)
	}
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn"],"edges":[{"from":"vpn","to":"vpn","kind":"excludes"}]}`); code != http.StatusOK {
		t.Fatalf("offer: %d %s", code, b)
	}

	code, body = cReq(t, c, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("publishing a self-conflict: %d %s, want it refused", code, body)
	}
	if !strings.Contains(string(body), "excludes itself") {
		t.Errorf("the refusal does not say what is wrong: %s", body)
	}
}
