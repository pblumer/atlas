package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/limits"
)

// Changing one position rather than a whole order, over HTTP
// (ADR-0359).
//
// The transitions are proved in api/order. What is proved here is the seam: the
// routes exist, the refusals arrive as statuses a caller can act on, and the two
// side effects the whole-order withdrawal needs are taken for a single position
// too.

// aWorkplaceWithParts publishes a workplace whose account is integral and whose
// laptop and screen are optional, orders all of them, and returns the order id.
// The laptop asks for a cost centre, so a correction has something to correct.
//
// A fixture of its own rather than the return path's aWorkplaceOrder: that one has
// no composition edge and no form, which are the two things every case here turns
// on.
func aWorkplaceWithParts(t *testing.T, c *http.Client, ts *httptest.Server) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}

	// The form the laptop asks for, so a correction has something to correct.
	if code, b := cReq(t, c, ts, "POST", "/api/v1/forms",
		`{"id":"form_laptop","name":"Angaben","schema":{"type":"default","components":[`+
			`{"key":"kostenstelle","label":"Kostenstelle","type":"textfield"}]}}`); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, b)
	}

	for _, p := range []struct{ id, form string }{
		{"workplace", ""}, {"account", ""}, {"laptop", "form_laptop"}, {"screen", ""},
	} {
		product := fmt.Sprintf(`{"id":%q,"homeCatalog":%q,"state":"active","texts":{"de":%q},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d",`+
			`"configForm":%q}`, p.id, cat.ID, p.id, p.form)
		if code, b := cReq(t, c, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save %s: %d %s", p.id, code, b)
		}
	}
	if code, b := cReq(t, c, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["workplace","account","laptop","screen"],"edges":[`+
			`{"from":"workplace","to":"account","kind":"composition"},`+
			`{"from":"workplace","to":"laptop","kind":"aggregation"},`+
			`{"from":"workplace","to":"screen","kind":"aggregation"}]}`); code != http.StatusOK {
		t.Fatalf("shape the catalogue: %d %s", code, b)
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
		fmt.Sprintf(`{"releaseId":%q,"items":["workplace","laptop","screen"],`+
			`"config":{"laptop":{"kostenstelle":"4711"}}}`, rel.ID))
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d %s", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	return ord.ID
}

func orderNow(t *testing.T, c *http.Client, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/orders/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("read order: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}

func lineIn(t *testing.T, o map[string]any, item string) map[string]any {
	t.Helper()
	for _, raw := range o["lines"].([]any) {
		l := raw.(map[string]any)
		if l["itemId"] == item {
			return l
		}
	}
	t.Fatalf("the order carries no line for %q: %v", item, o["lines"])
	return nil
}

// TestOnePositionIsWithdrawnAndTheRestStands.
func TestOnePositionIsWithdrawnAndTheRestStands(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST",
		"/api/v1/orders/"+id+"/lines/screen/cancel", `{"reason":"not needed"}`)
	if code != http.StatusOK {
		t.Fatalf("withdrawing one position = %d, want 200 (%s)", code, body)
	}
	o := orderNow(t, c, ts, id)
	if got := lineIn(t, o, "screen")["status"]; got != "cancelled" {
		t.Errorf("the screen is %v, want cancelled", got)
	}
	if got := lineIn(t, o, "laptop")["status"]; got != "pending" {
		t.Errorf("withdrawing the screen also took back the laptop: %v", got)
	}
	// And it says who, in a record kept for years.
	if lineIn(t, o, "screen")["decidedBy"] == nil {
		t.Error("the withdrawal does not say who made it")
	}
}

// TestAnIntegralPositionIsRefusedWithWhatCarriesIt.
//
// The basket will not let anybody deselect a part its whole always carries, and a
// rule enforced when ordering and not afterwards is not a rule. The refusal names
// the whole, because "take back the workplace instead" is the answer somebody
// needs.
func TestAnIntegralPositionIsRefusedWithWhatCarriesIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/lines/account/cancel", "")
	if code != http.StatusConflict {
		t.Fatalf("withdrawing an integral position = %d, want 409 (%s)", code, body)
	}
	if !strings.Contains(string(body), "workplace") {
		t.Errorf("the refusal does not name what carries it: %s", body)
	}
	if got := lineIn(t, orderNow(t, c, ts, id), "account")["status"]; got != "pending" {
		t.Errorf("the refused withdrawal changed the line anyway: %v", got)
	}
}

// TestCorrectingTheDetailsOfAPositionNotYetAttempted.
func TestCorrectingTheDetailsOfAPositionNotYetAttempted(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/lines/laptop/details",
		`{"config":{"kostenstelle":"0815"},"reason":"wrong cost centre"}`)
	if code != http.StatusOK {
		t.Fatalf("correcting a pending position = %d, want 200 (%s)", code, body)
	}
	line := lineIn(t, orderNow(t, c, ts, id), "laptop")
	cfg := line["config"].(map[string]any)
	if cfg["kostenstelle"] != "0815" {
		t.Errorf("the correction did not take: %v", cfg)
	}
	// Nothing was attempted, so there is no delivery the old answers were true of
	// and no amendment to record.
	if line["amendments"] != nil {
		t.Errorf("a request that was never acted on recorded an amendment: %v", line["amendments"])
	}
}

// TestCorrectingAPositionThatAsksNothingIsRefused.
func TestCorrectingAPositionThatAsksNothingIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/lines/screen/details",
		`{"config":{"kostenstelle":"0815"}}`)
	if code != http.StatusConflict {
		t.Fatalf("correcting a position that asks nothing = %d, want 409 (%s)", code, body)
	}
	if !strings.Contains(string(body), "asks for no details") {
		t.Errorf("the refusal does not say why: %s", body)
	}
}

// TestChangingAPositionInSomebodyElsesOrderIsNotFound.
//
// 404 and not 403, exactly as reading one is: whose orders exist is not something
// these endpoints answer.
func TestChangingAPositionInSomebodyElsesOrderIsNotFound(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	id := aWorkplaceWithParts(t, admin, ts)
	stranger := twoUsers(t, ts, admin, "mallory", "unused")[0]

	for _, path := range []string{"/lines/screen/cancel", "/lines/laptop/details"} {
		code, body := cReq(t, stranger, ts, "POST", "/api/v1/orders/"+id+path, `{"config":{}}`)
		if code != http.StatusNotFound {
			t.Errorf("a stranger reaching %s = %d, want 404 (%s)", path, code, body)
		}
	}
}

// TestAMalformedChangeIsRefusedBeforeAnythingIsRead.
//
// The body is decoded before the order is looked at, so a caller who sent
// nonsense learns that rather than "no order" — which would send them looking for
// an order that is there.
func TestAMalformedChangeIsRefusedBeforeAnythingIsRead(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	for _, path := range []string{"/lines/screen/cancel", "/lines/laptop/details"} {
		code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+path, "{not json")
		if code != http.StatusBadRequest {
			t.Errorf("a malformed body on %s = %d, want 400 (%s)", path, code, body)
		}
	}
}

// TestChangingAPositionOfAnOrderThatDoesNotExist.
func TestChangingAPositionOfAnOrderThatDoesNotExist(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	for _, path := range []string{"/lines/screen/cancel", "/lines/laptop/details"} {
		code, body := cReq(t, c, ts, "POST", "/api/v1/orders/ord_nope"+path, `{"config":{}}`)
		if code != http.StatusNotFound {
			t.Errorf("reaching %s on an order that does not exist = %d, want 404 (%s)",
				path, code, body)
		}
	}
}

// TestCorrectingAPositionTheOrderDoesNotCarry.
//
// 404 and not 409: the position is not there to refuse. A conflict would say the
// position exists and cannot take the change, which is a different sentence and
// sends the reader somewhere else.
func TestCorrectingAPositionTheOrderDoesNotCarry(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/lines/nosuchthing/details",
		`{"config":{"kostenstelle":"0815"}}`)
	if code != http.StatusNotFound {
		t.Fatalf("correcting a position the order does not carry = %d, want 404 (%s)", code, body)
	}
}

// TestMoreDetailsThanOnePositionKeepsAreRefusedOnACorrectionToo.
//
// The same ceiling the order was placed under. Without it, a correction is a way
// around a budget that placing an order respects — the map arrives whole in a
// request body either way.
func TestMoreDetailsThanOnePositionKeepsAreRefusedOnACorrectionToo(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword",
		api.WithLimits(func() limits.Limits {
			l := limits.Default()
			l.OrderLineAnswers = 2
			return l
		}()))
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	id := aWorkplaceWithParts(t, c, ts)

	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/lines/laptop/details",
		`{"config":{"a":"1","b":"2","c":"3"}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("three answers against a ceiling of two = %d, want 400 (%s)", code, body)
	}
	if !strings.Contains(string(body), "the order was placed under") {
		t.Errorf("the refusal does not say it is the same ceiling: %s", body)
	}
}
