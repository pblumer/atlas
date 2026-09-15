package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// The commissioning-load routes, over HTTP
// (ADR-draft-inventory-commissioning-load).
//
// The slice's whole point is that the first run writes nothing until somebody has
// read what it would do, and that what it then writes is marked as knowledge Atlas
// did not produce. Those two are checked end to end, against the inventory rather
// than against the answer.

// aCatalogueWithAVPNProduct creates a catalogue and one product claiming an AD
// group, and returns the product id.
func aCatalogueWithAVPNProduct(t *testing.T, c *http.Client, ts *httptest.Server, targets string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"texts":{"de":"Standard"},"rank":1,"languages":["de"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}

	code, body = cReq(t, c, ts, "POST", "/api/v1/catalog-products", fmt.Sprintf(
		`{"id":"vpn","homeCatalog":%q,"state":"active","texts":{"de":"VPN"},`+
			`"provisionProcess":"p","deprovisionProcess":"d","approval":{"kind":"none"},`+
			`"targets":%s}`, cat.ID, targets))
	if code != http.StatusOK {
		t.Fatalf("save product: %d %s", code, body)
	}
	return cat.ID
}

// anAccountWithMail creates an ordinary account and returns its id.
func anAccountWithMail(t *testing.T, c *http.Client, ts *httptest.Server, username, mail string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/users", fmt.Sprintf(
		`{"username":%q,"email":%q,"password":"correct horse battery","roles":["user"]}`,
		username, mail))
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create user: %d %s", code, body)
	}
	var u struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	return u.ID
}

// postLoad reports one reading and returns the answer.
func postLoad(t *testing.T, c *http.Client, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST", "/api/v1/inventory-load", body)
	if code != http.StatusOK {
		t.Fatalf("POST inventory-load: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode report: %v (%s)", err, raw)
	}
	return out
}

// heldBy reads one principal's inventory through the route a person would.
func heldBy(t *testing.T, c *http.Client, ts *httptest.Server, principal string) []map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/inventory?principal="+principal, "")
	if code != http.StatusOK {
		t.Fatalf("GET inventory: %d %s", code, raw)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	return out.Items
}

// TestAReportingLoadRecordsNothing.
//
// The first load reads a whole target system against an empty inventory, which is
// the run where a defect in the rules reaches everybody at once. So it writes
// nothing until somebody has read what it would do — and "nothing" is checked
// against the inventory, not against the report that says so.
func TestAReportingLoadRecordsNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	aCatalogueWithAVPNProduct(t, c, ts, `[{"system":"ad","ref":"CN=VPN-Users"}]`)
	ada := anAccountWithMail(t, c, ts, "ada", "ada@example.org")

	rep := postLoad(t, c, ts, `{"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)

	if rep["applied"] != false || rep["mode"] != "report-only" {
		t.Errorf("report says applied=%v mode=%v", rep["applied"], rep["mode"])
	}
	if reason, _ := rep["reason"].(string); !strings.Contains(reason, "asked for a report") {
		t.Errorf("reason = %q, want it to say nothing was written and why", reason)
	}
	counts, _ := rep["counts"].(map[string]any)
	if counts["grant"] != float64(1) {
		t.Errorf("counts = %+v, want one right decided", counts)
	}
	if held := heldBy(t, c, ts, ada); len(held) != 0 {
		t.Errorf("a reporting load wrote %d record(s) into the inventory: %+v", len(held), held)
	}

	// And the state route says so: never applied, but reported.
	code, raw := cReq(t, c, ts, "GET", "/api/v1/inventory-load?system=ad", "")
	if code != http.StatusOK {
		t.Fatalf("GET state: %d %s", code, raw)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if st["everApplied"] != false {
		t.Error("everApplied is true after a run that wrote nothing")
	}
	if st["lastReportedAt"] == nil {
		t.Error("the state records nothing about the preview, so a system that has been " +
			"previewing for a week looks exactly like one nobody ever pointed at")
	}
}

// TestAnAppliedLoadRecordsRightsAsNotOurs: what it writes must say where the
// knowledge came from.
//
// The whole reason the origin exists is that a reconciliation reading this later has
// to tell what Atlas granted from what it merely found. A right recorded as
// `ordered` because the load was careless is an assumption filed as evidence.
func TestAnAppliedLoadRecordsRightsAsNotOurs(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCatalogueWithAVPNProduct(t, c, ts, `[{"system":"ad","ref":"CN=VPN-Users"}]`)
	ada := anAccountWithMail(t, c, ts, "ada", "ada@example.org")

	rep := postLoad(t, c, ts, `{"apply":true,"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)
	if rep["applied"] != true || rep["mode"] != "applied" {
		t.Fatalf("the load did not write: %+v", rep)
	}

	held := heldBy(t, c, ts, ada)
	if len(held) != 1 {
		t.Fatalf("the inventory holds %d record(s) after the load: %+v", len(held), held)
	}
	if held[0]["itemId"] != "vpn" {
		t.Errorf("recorded item = %v, want vpn", held[0]["itemId"])
	}
	if held[0]["origin"] != "legacy" {
		t.Errorf("origin = %v, want legacy: Atlas did not grant this and must not say it did",
			held[0]["origin"])
	}
	if _, claimsAnOrder := held[0]["orderId"]; claimsAnOrder {
		t.Errorf("the record names an order (%v); nothing here produced this right",
			held[0]["orderId"])
	}

	// Running the same load again is quiet rather than destructive: nothing new, and
	// the record that exists is left exactly as it is.
	again := postLoad(t, c, ts, `{"apply":true,"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)
	if again["applied"] != false {
		t.Errorf("a repeated load wrote again: %+v", again)
	}
	counts, _ := again["counts"].(map[string]any)
	if counts["held"] != float64(1) {
		t.Errorf("counts = %+v, want the right reported as already recorded", counts)
	}
	if after := heldBy(t, c, ts, ada); len(after) != 1 || after[0]["since"] != held[0]["since"] {
		t.Errorf("the repeat changed the record: %+v then %+v", held, after)
	}
}

// TestALoadNamesWhatTheCatalogueDoesNotModel.
//
// This is the part of the answer nothing else in the estate can produce: the rights
// that are granted and that nobody decided to offer. It is rolled up by reference,
// because eight hundred lines saying the same thing is one fact.
func TestALoadNamesWhatTheCatalogueDoesNotModel(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCatalogueWithAVPNProduct(t, c, ts, `[{"system":"ad","ref":"CN=VPN-Users"}]`)
	anAccountWithMail(t, c, ts, "ada", "ada@example.org")
	anAccountWithMail(t, c, ts, "bob", "bob@example.org")

	rep := postLoad(t, c, ts, `{"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=AllStaff"},
		{"subject":"bob@example.org","ref":"CN=AllStaff"},
		{"subject":"ghost@example.org","ref":"CN=VPN-Users"}]}`)

	unmapped, _ := rep["unmapped"].([]any)
	if len(unmapped) != 1 {
		t.Fatalf("unmapped = %+v, want the one reference nobody models", rep["unmapped"])
	}
	row, _ := unmapped[0].(map[string]any)
	if row["ref"] != "CN=AllStaff" || row["subjects"] != float64(2) {
		t.Errorf("unmapped row = %+v, want CN=AllStaff held by 2", row)
	}

	counts, _ := rep["counts"].(map[string]any)
	if counts["noSubject"] != float64(1) {
		t.Errorf("counts = %+v, want the person with no account reported rather than dropped", counts)
	}
}

// TestALoadWithoutASystemIsRefused: the system name is half the match, so a load
// without one would report every right in the batch as unmodelled and look like a
// catalogue problem.
func TestALoadWithoutASystemIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/inventory-load",
		`{"observations":[{"subject":"a","ref":"b"}]}`)
	if code != http.StatusBadRequest {
		t.Errorf("a load with no system was accepted: %d %s", code, body)
	}
	if !strings.Contains(string(body), "system") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
	if code, body := cReq(t, c, ts, "GET", "/api/v1/inventory-load", ""); code != http.StatusBadRequest {
		t.Errorf("the state route answered without a system: %d %s", code, body)
	}
}

// TestALoadBatchAboveTheBudgetIsRefusedWhole.
//
// Truncating would be worse here than almost anywhere else: the load's own record
// would then say the system was loaded, while part of it silently was not — which is
// exactly the gap in the evidence the whole exercise exists to close.
func TestALoadBatchAboveTheBudgetIsRefusedWhole(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	over := int(limits.Default().InventoryObservations) + 1
	rows := make([]string, 0, over)
	for i := range over {
		rows = append(rows, fmt.Sprintf(`{"subject":"p%d","ref":"r"}`, i))
	}
	code, body := cReq(t, c, ts, "POST", "/api/v1/inventory-load",
		`{"system":"ad","observations":[`+strings.Join(rows, ",")+`]}`)

	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a batch of %d was not refused: %d %s", over, code, body)
	}
	if !strings.Contains(string(body), "ATLAS_LIMIT_INVENTORY_OBSERVATIONS") {
		t.Errorf("the refusal does not name the budget to raise: %s", body)
	}
	if !strings.Contains(string(body), "pages") {
		t.Errorf("the refusal does not say what to do instead: %s", body)
	}
}

// TestTheLoadRoutesAreConfinedToTheirOwnScope: a credential that reads a target
// system's memberships must not also be able to deploy or to create accounts.
//
// The separation from the directory scope is the point. The mirror creates accounts
// and this records what they hold; one credential able to do both could invent a
// person and then hand them the estate's rights in two calls.
func TestTheLoadRoutesAreConfinedToTheirOwnScope(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/api-tokens",
		`{"name":"loader","scope":"inventory","roles":["operator"]}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("mint token: %d %s", code, body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, body)
	}

	withToken := func(method, path, payload string) int {
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(payload))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := newClient(t).Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	if got := withToken("GET", "/api/v1/inventory-load?system=ad", ""); got != http.StatusOK {
		t.Errorf("the load scope cannot reach its own state route: %d", got)
	}
	for _, beyond := range []struct{ method, path string }{
		{"POST", "/api/v1/directory-sync"},
		{"GET", "/api/v1/users"},
		{"POST", "/api/v1/deployments"},
	} {
		if got := withToken(beyond.method, beyond.path, "{}"); got != http.StatusForbidden {
			t.Errorf("%s %s answered %d for a load credential; the scope's value is that "+
				"the answer to \"what else could this do if it leaked\" is two lines long",
				beyond.method, beyond.path, got)
		}
	}
}
