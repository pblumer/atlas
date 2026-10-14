package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The inventory, end to end: order, provision, hold; return, and stop holding.
//
// This is the claim the third model exists for. Everything before it could be
// answered from the order — what was asked for, what was approved, what came
// back. "What does this person hold today" cannot, because the order that granted
// it is deleted by retention long before the access ends, and this test is the
// only place the two are read as separate things.

type heldResp struct {
	Principal string `json:"principal"`
	Items     []struct {
		ItemID    string `json:"itemId"`
		VariantID string `json:"variantId"`
		Since     int64  `json:"since"`
		Origin    string `json:"origin"`
		OrderID   string `json:"orderId"`
	} `json:"items"`
}

func inventoryOf(t *testing.T, ts *httptest.Server, c *http.Client, query string) (int, heldResp) {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/inventory"+query, "")
	if code != http.StatusOK {
		return code, heldResp{}
	}
	var got heldResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode inventory: %v (%s)", err, body)
	}
	return code, got
}

func TestProvisioningPutsSomethingIntoTheInventoryAndReturningTakesItOut(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", deprovisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy the revocation: %d (%s)", code, b)
	}

	// Ordering is not holding. An order that is merely placed grants nothing, and
	// an inventory that showed it would report access nobody has yet.
	if _, got := inventoryOf(t, ts, admin, ""); len(got.Items) != 0 {
		t.Fatalf("inventory before provisioning = %+v, want empty", got.Items)
	}

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}

	_, got := inventoryOf(t, ts, admin, "")
	if len(got.Items) != 1 {
		t.Fatalf("inventory after provisioning = %+v, want the vpn", got.Items)
	}
	if got.Items[0].ItemID != "vpn" {
		t.Errorf("held = %q, want vpn", got.Items[0].ItemID)
	}
	// Where the knowledge came from, and what produced it. Without the first, the
	// first reconciliation cannot tell what Atlas granted from what it merely
	// found; without the second, a right cannot be traced to the approval behind it.
	if got.Items[0].Origin != "ordered" {
		t.Errorf("origin = %q, want ordered", got.Items[0].Origin)
	}
	if got.Items[0].OrderID != orderID {
		t.Errorf("orderId = %q, want %q", got.Items[0].OrderID, orderID)
	}
	if got.Items[0].Since == 0 {
		t.Error("the hold has no start; an access record that cannot say since when answers nothing")
	}

	// Asking for it back is not having given it back. Until the revocation reports,
	// the target system still has the access, and the record must say so.
	if code, b := returnLine(t, ts, admin, orderID, "vpn"); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	if _, got := inventoryOf(t, ts, admin, ""); len(got.Items) != 1 {
		t.Fatalf("inventory while the revocation runs = %+v, want the vpn still held", got.Items)
	}

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report returned: %d (%s)", code, b)
	}
	if _, got := inventoryOf(t, ts, admin, ""); len(got.Items) != 0 {
		t.Fatalf("inventory after the return = %+v, want empty", got.Items)
	}
}

// An inventory is a list of somebody's access, and reading it is the same act
// whether it is used well or badly. The caller's own is theirs; anybody else's
// needs the role that already administers accounts.
func TestSomebodyElsesInventoryNeedsTheAdminRole(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	stranger := twoUsers(t, ts, admin, "mallory")[0]

	if code, _ := inventoryOf(t, ts, stranger, "?principal=root"); code != http.StatusForbidden {
		t.Errorf("a stranger reading root's inventory = %d, want 403", code)
	}
	// Their own is theirs, and it is empty rather than refused.
	code, got := inventoryOf(t, ts, stranger, "")
	if code != http.StatusOK {
		t.Fatalf("own inventory = %d, want 200", code)
	}
	if len(got.Items) != 0 {
		t.Errorf("a new account holds %+v", got.Items)
	}
	// The subject travels with the answer: a list with no subject is one somebody
	// attributes to the wrong person.
	if got.Principal == "" {
		t.Error("the answer does not say whose inventory it is")
	}
}

// The basket's second resolution, through the whole stack: the server reads the
// inventory when an order is placed, and orders what is already held as skipped.
//
// This is the one path where the entitlement family is read by something other
// than a screen. A portal marking is advice a browser can ignore; this decides
// whether a provisioning process runs against a target system a second time.
func TestOrderingSomethingAlreadyHeldIsSkippedRatherThanProvisionedTwice(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	catID, orderID := aCatalogueWithAnOrder(t, ts, admin)

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}

	code, body := cReq(t, admin, ts, "GET", "/api/v1/catalogs/"+catID+"/releases", "")
	if code != http.StatusOK {
		t.Fatalf("read releases: %d (%s)", code, body)
	}
	var releases []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &releases); err != nil || len(releases) == 0 {
		t.Fatalf("decode releases: %v (%s)", err, body)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+releases[0].ID+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place the second order: %d (%s)", code, body)
	}
	var second struct {
		ID    string `json:"id"`
		Lines []struct {
			ItemID string `json:"itemId"`
			Status string `json:"status"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	// The request is recorded, not dropped: "you asked for this and already had
	// it" is a different sentence from silence, and only a line can say it.
	if len(second.Lines) != 1 {
		t.Fatalf("the second order carries %d lines, want the vpn recorded as asked for", len(second.Lines))
	}
	if second.Lines[0].Status != "skipped" {
		t.Errorf("the second order's vpn is %q, want skipped — provisioning it again "+
			"would reach the target system twice", second.Lines[0].Status)
	}
	// And nothing new is held: the same right, not two.
	if _, got := inventoryOf(t, ts, admin, ""); len(got.Items) != 1 {
		t.Errorf("inventory = %+v, want the one vpn", got.Items)
	}
}

// aTwoItemRelease publishes a catalogue with two products, so an inventory can
// have more than one row and the ordering of the answer means something.
func aTwoItemRelease(t *testing.T, ts *httptest.Server, admin *http.Client) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}
	for _, id := range []string{"konto", "notebook"} {
		product := `{"id":"` + id + `","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"` + id + `"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save %s: %d (%s)", id, code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["konto","notebook"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	return rel.ID
}

// userID looks a username up, because an entitlement names a principal by id and
// never by name — a rename must not orphan somebody's access.
func userID(t *testing.T, ts *httptest.Server, admin *http.Client, username string) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("list users: %d (%s)", code, body)
	}
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("decode users: %v (%s)", err, body)
	}
	for _, u := range users {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("no user %q in %s", username, body)
	return ""
}

// An administrator reads a colleague's inventory, which is what a leaver process
// and an audit are. The rights are the *recipient's*: the administrator placed the
// order, and holds nothing themselves as a result.
func TestAnAdministratorReadsSomebodyElsesInventory(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "bob")
	bob := userID(t, ts, admin, "bob")
	relID := aTwoItemRelease(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+relID+`","items":["konto","notebook"],"recipient":"`+bob+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("place: %d (%s)", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	for _, item := range []string{"konto", "notebook"} {
		if code, b := cReq(t, admin, ts, "POST",
			fmt.Sprintf("/api/v1/orders/%s/lines/%s", ord.ID, item), `{"status":"done"}`); code != http.StatusOK {
			t.Fatalf("provision %s: %d (%s)", item, code, b)
		}
	}

	_, got := inventoryOf(t, ts, admin, "?principal="+bob)
	if got.Principal != bob {
		t.Errorf("answer is about %q, want %q", got.Principal, bob)
	}
	if len(got.Items) != 2 {
		t.Fatalf("bob holds %+v, want both", got.Items)
	}
	// Newest first: somebody opening this is asking what changed.
	if got.Items[0].Since < got.Items[1].Since {
		t.Errorf("rows are oldest first: %+v", got.Items)
	}
	// And the orderer holds nothing. An integration manager who orders for a
	// colleague does not thereby hold a laptop.
	if _, mine := inventoryOf(t, ts, admin, ""); len(mine.Items) != 0 {
		t.Errorf("the orderer holds %+v, want nothing", mine.Items)
	}
}

// With authentication off there is nobody to be, so there is no default subject.
// The answer says so rather than returning an empty list somebody would read as
// "this person holds nothing".
func TestWithNobodySignedInTheInventoryNeedsANamedPrincipal(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, "GET", "/api/v1/inventory", "", ""); code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", code, body)
	}
	if code, body := doReq(t, ts, "GET", "/api/v1/inventory?principal=usr_ada", "", ""); code != http.StatusOK {
		t.Fatalf("naming one = %d, want 200: %s", code, body)
	}
}
