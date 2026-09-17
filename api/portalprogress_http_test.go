package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// Where somebody's own position stands, without an operations surface
// (ADR-draft-position-progress).
//
// A position already carries a link into the process working on it, and that link
// is an operator's: every route that finds or opens an instance is RoleOperator,
// and the instance view shows the whole engine state of the instance — variables
// included, and a process holding two people's data holds both. So the orderer
// gets a different answer rather than the same answer through a wider door: one
// route, gated on owning the order, that says which step the position is sitting
// on and nothing else.

// progressResp is the whole of that answer. Declared here, in the test, so the
// shape is asserted against what a caller reads rather than against the struct
// that produced it.
type progressResp struct {
	State string   `json:"state"`
	Steps []string `json:"steps"`
}

// aServerWithAnOrderFor stands up an authenticated server carrying the shipped
// system processes, a one-product catalogue, and one order placed by the
// administrator for the named recipient.
//
// The recipient is the reader in every case below: an order is readable by the
// person who placed it and by the person it is for, and the second is the one
// that has nothing to do with a role.
func aServerWithAnOrderFor(t *testing.T, recipient string) (
	ts *httptest.Server, admin *http.Client, reader *http.Client, orderID string) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"`+recipient+`","password":"password1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create %s: %d (%s)", recipient, code, body)
	}
	who := idOf(t, body)
	reader = newClient(t)
	if login(t, reader, ts, recipient, "password1") != http.StatusOK {
		t.Fatalf("%s login failed", recipient)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	for _, p := range []string{"vpn", "laptop"} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
			`{"id":"`+p+`","homeCatalog":"`+cat+`","state":"active","texts":{"de":"`+p+`"},`+
				`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		); code != http.StatusOK {
			t.Fatalf("save %s: %d (%s)", p, code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["vpn","laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer the products: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["vpn","laptop"],"recipient":"`+who+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return ts, admin, reader, idOf(t, body)
}

// startApprovalFor starts the shipped approval process the way the fulfilment
// orchestrator starts it: with the order it belongs to and the position it is
// working on.
func startApprovalFor(t *testing.T, ts *httptest.Server, admin *http.Client, orderID, position string) {
	t.Helper()
	start := `{"processId":"atlas-genehmigung-fix","variables":{
		"orderId":"` + orderID + `","itemId":"vpn","positionId":"` + position + `",
		"approvalRef":"root","provisionProcess":"prov",
		"recipient":"usr_empfaenger","orderer":"root"}}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start the position's process: %d (%s)", code, b)
	}
}

// progressOf asks the route and returns the raw body with it, because two of the
// cases below are about what the body does *not* carry.
func progressOf(t *testing.T, c *http.Client, ts *httptest.Server, orderID, position string) (
	int, progressResp, []byte) {
	t.Helper()
	// Escaped, because a position key joins the product and the variant with "#"
	// and an unescaped one would be a fragment: the server would never see the
	// variant half at all (ADR-0384).
	code, body := cReq(t, c, ts, "GET",
		"/api/v1/portal/orders/"+orderID+"/lines/"+url.PathEscape(position)+"/progress", "")
	var out progressResp
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode progress: %v (%s)", err, body)
		}
	}
	return code, out, body
}

// TestTheOrdererSeesWhichStepTheirPositionIsOn.
//
// The whole point, and the reason the step is a *name*: "Genehmigen" is what the
// person waiting is waiting for, and "Genehmigen_1" is a string out of a modelling
// tool that means nothing to them.
func TestTheOrdererSeesWhichStepTheirPositionIsOn(t *testing.T) {
	ts, admin, reader, ord := aServerWithAnOrderFor(t, "alice")
	startApprovalFor(t, ts, admin, ord, "vpn")

	code, got, body := progressOf(t, reader, ts, ord, "vpn")
	if code != http.StatusOK {
		t.Fatalf("the recipient's own position = %d (%s), want 200", code, body)
	}
	if got.State != "active" {
		t.Errorf("state = %q, want active (%s)", got.State, body)
	}
	if !amongTheSteps(got.Steps, "Genehmigen") {
		t.Errorf("steps = %v, want the approval's own step among them (%s)", got.Steps, body)
	}

	// A name out of the document, and the case that proves it: the shipped model
	// calls that task "Benachrichtigen" and names it "Genehmiger benachrichtigen".
	// The task above cannot prove it — its id and its name are the same word — and a
	// guard that only read that one would stay green with the names never looked up.
	if !amongTheSteps(got.Steps, "Genehmiger benachrichtigen") {
		t.Errorf("steps = %v, want the step named as the model names it (%s)", got.Steps, body)
	}
	if amongTheSteps(got.Steps, "Benachrichtigen") {
		t.Errorf("steps = %v carries a BPMN id, which means nothing to the person "+
			"whose order it is", got.Steps)
	}

	// And nothing that holds no token of its own. Both deadlines are armed on the
	// approval task right now; neither is where the position is, and "Ihre
	// Bestellung steht bei: Eskalationsfrist" is a sentence nobody can act on.
	for _, armed := range []string{"Erinnerungsfrist", "Eskalationsfrist"} {
		if amongTheSteps(got.Steps, armed) {
			t.Errorf("steps = %v names %q, which is attached beside the step rather "+
				"than being one", got.Steps, armed)
		}
	}

	// And it is the position's process, not the order's. The fulfilment
	// orchestration is running against the same order and carries the same order
	// id; what tells the two apart is the position, and a route that answered with
	// the first instance carrying the order id would say "Startbereite Positionen
	// holen" to somebody asking where their VPN is.
	for _, orchestration := range []string{"Startbereite Positionen holen", "Position gemeldet"} {
		if amongTheSteps(got.Steps, orchestration) {
			t.Errorf("steps = %v, which is the order's orchestration rather than the "+
				"process working on this position", got.Steps)
		}
	}
}

// TestSomebodyElsesOrderIsNotThereToFind.
//
// 404 and not 403: a refusal confirms the order exists, and whose orders exist is
// not something this route answers. It is the same answer reading the order gives,
// for the same reason.
func TestSomebodyElsesOrderIsNotThereToFind(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")
	startApprovalFor(t, ts, admin, ord, "vpn")
	stranger := twoUsers(t, ts, admin, "mallory")[0]

	code, _, body := progressOf(t, stranger, ts, ord, "vpn")
	if code != http.StatusNotFound {
		t.Fatalf("a stranger reading somebody else's position = %d (%s), want 404", code, body)
	}
}

// TestTheProgressCarriesNoProcessVariable.
//
// The route exists to say *where*, not *what*. The process it reads holds the
// recipient, the orderer and the order id, and none of that is this route's to
// hand back — the caller already knows their own order, and a route that widened
// once widens again.
func TestTheProgressCarriesNoProcessVariable(t *testing.T) {
	ts, admin, reader, ord := aServerWithAnOrderFor(t, "alice")
	startApprovalFor(t, ts, admin, ord, "vpn")

	code, _, body := progressOf(t, reader, ts, ord, "vpn")
	if code != http.StatusOK {
		t.Fatalf("progress = %d (%s), want 200", code, body)
	}
	for _, leaked := range []string{"usr_empfaenger", "orderer", "approvalRef", "provisionProcess"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("the answer carries %q: %s", leaked, body)
		}
	}
}

// TestAPositionNothingIsWorkingOnSaysSo.
//
// Not an error, and not silence. A position nothing is running for is the ordinary
// case for most of an order's life — before it is reached, and after it is done —
// and the word for it is "none" rather than a 404 that reads as "no such position".
func TestAPositionNothingIsWorkingOnSaysSo(t *testing.T) {
	ts, admin, reader, ord := aServerWithAnOrderFor(t, "alice")
	startApprovalFor(t, ts, admin, ord, "vpn")

	code, got, body := progressOf(t, reader, ts, ord, "laptop")
	if code != http.StatusOK {
		t.Fatalf("a position with no process = %d (%s), want 200", code, body)
	}
	if got.State != "none" {
		t.Errorf("state = %q, want none (%s)", got.State, body)
	}
	if len(got.Steps) != 0 {
		t.Errorf("steps = %v, want none (%s)", got.Steps, body)
	}
}

// TestNamingAProductOrderedTwiceIsRefused.
//
// The position key is what a caller names, and naming the product instead is
// answered wherever the order carries one position of it. Two is refused rather
// than guessed: the two are a black phone and a silver one, and answering about
// whichever line the walk reached first is how a screen starts lying quietly.
func TestNamingAProductOrderedTwiceIsRefused(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := twoColourCatalogue(t, ts, admin)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["phone"],"variants":{"phone":["black","silver"]}}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	ord := idOf(t, body)

	code, _, body = progressOf(t, admin, ts, ord, "phone")
	if code != http.StatusConflict {
		t.Fatalf("naming a product the order carries twice = %d (%s), want 409", code, body)
	}
	if !strings.Contains(string(body), "phone#black") {
		t.Errorf("the refusal does not name the positions to ask about instead: %s", body)
	}

	// And each position on its own is answered.
	if code, _, body := progressOf(t, admin, ts, ord, "phone#black"); code != http.StatusOK {
		t.Fatalf("one named position = %d (%s), want 200", code, body)
	}
}

// amongTheSteps reports whether the answer names this step. Spelled out rather
// than reaching for a shared helper: the package already has a contains over
// substrings, and a step that merely *contains* "Genehmigen" is not the step.
func amongTheSteps(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
