package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The reconciliation routes, over HTTP (ADR-0334).
//
// The inventory these run against is filled through the commissioning load rather
// than by reaching into the store, deliberately: the two slices meet here, and a
// test that wrote entitlements some other way would prove the comparison works
// against something no installation will ever hold.

// postReconcile runs one comparison and returns the report.
func postReconcile(t *testing.T, c *http.Client, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST", "/api/v1/reconciliation", body)
	if code != http.StatusOK {
		t.Fatalf("POST reconciliation: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode report: %v (%s)", err, raw)
	}
	return out
}

// openFindings reads what still stands.
func openFindings(t *testing.T, c *http.Client, ts *httptest.Server) []map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/reconciliation", "")
	if code != http.StatusOK {
		t.Fatalf("GET reconciliation: %d %s", code, raw)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode findings: %v", err)
	}
	return out
}

// aReconcilableEstate sets up a catalogue with one product claiming one AD group,
// two accounts, and an inventory in which only Ada holds it.
func aReconcilableEstate(t *testing.T, c *http.Client, ts *httptest.Server) (ada, bo string) {
	t.Helper()
	aCatalogueWithAVPNProduct(t, c, ts, `[{"system":"ad","ref":"CN=VPN-Users"}]`)
	ada = anAccountWithMail(t, c, ts, "ada", "ada@example.org")
	bo = anAccountWithMail(t, c, ts, "bo", "bo@example.org")

	rep := postLoad(t, c, ts, `{"apply":true,"system":"ad","observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)
	if rep["applied"] != true {
		t.Fatalf("the inventory was not filled: %+v", rep)
	}
	return ada, bo
}

// TestAReconciliationFindsBothDirectionsAndWritesNothing.
//
// The run is a comparison and only a comparison: it must leave the inventory
// exactly as it found it, whatever it concludes. Everything that changes an access
// right is a separate call by a person.
func TestAReconciliationFindsBothDirectionsAndWritesNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	ada, bo := aReconcilableEstate(t, c, ts)

	// Reality moved: Ada is out of the group, Bo is in it.
	rep := postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[
		{"subject":"bo@example.org","ref":"CN=VPN-Users"}]}`)

	counts, _ := rep["counts"].(map[string]any)
	if counts["unmanaged"] != float64(1) || counts["missing"] != float64(1) {
		t.Errorf("counts = %+v, want one finding in each direction", counts)
	}
	if rep["opened"] != float64(2) {
		t.Errorf("opened = %v, want both findings recorded as new", rep["opened"])
	}

	// The inventory is untouched: Ada still holds it, Bo still does not.
	if held := heldBy(t, c, ts, ada); len(held) != 1 {
		t.Errorf("the comparison changed Ada's inventory: %+v", held)
	}
	if held := heldBy(t, c, ts, bo); len(held) != 0 {
		t.Errorf("the comparison granted Bo something: %+v", held)
	}

	// And both stand in the journal.
	if open := openFindings(t, c, ts); len(open) != 2 {
		t.Errorf("%d finding(s) stand open, want 2", len(open))
	}
}

// TestAFindingIsOnlyRecordedOnceHoweverOftenItIsSeen, and closes when it goes.
//
// This is the transition shape: ten runs over an unchanged disagreement produce one
// record, and the run where it disappears is the one that writes something.
func TestAFindingIsOnlyRecordedOnceHoweverOftenItIsSeen(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aReconcilableEstate(t, c, ts)

	drifted := `{"system":"ad","refs":["CN=VPN-Users"],"observations":[
		{"subject":"bo@example.org","ref":"CN=VPN-Users"}]}`
	postReconcile(t, c, ts, drifted)
	again := postReconcile(t, c, ts, drifted)

	if again["opened"] != float64(0) {
		t.Errorf("a second identical run opened %v finding(s); a journal of samples is a "+
			"journal nobody reads", again["opened"])
	}
	if open := openFindings(t, c, ts); len(open) != 2 {
		t.Errorf("%d findings open after two identical runs, want the same 2", len(open))
	}

	// Reality repaired, by somebody else: Ada back in, Bo out.
	fixed := postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"}]}`)
	if fixed["closed"] != float64(2) {
		t.Errorf("closed = %v, want both findings closed when they went away", fixed["closed"])
	}
	if open := openFindings(t, c, ts); len(open) != 0 {
		t.Errorf("%d finding(s) still open after the disagreement was repaired: %+v", len(open), open)
	}
}

// TestARunWithoutAScopeIsRefused — the refusal that keeps the endpoint honest.
func TestARunWithoutAScopeIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/reconciliation",
		`{"system":"ad","observations":[{"subject":"a","ref":"b"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("a run naming no scope was accepted: %d %s", code, body)
	}
	for _, want := range []string{"refs", "absence", "complete"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refusal does not mention %q, so it does not say why a scope is "+
				"required: %s", want, body)
		}
	}
}

// TestAdoptingAnUnmanagedRightRecordsWhereItCameFrom.
//
// `adopted`, never `legacy`: both mean Atlas did not grant it, and they differ in
// who said so. An audit that could not tell them apart could not tell a
// pre-existing estate from privileges that grew afterwards.
func TestAdoptingAnUnmanagedRightRecordsWhereItCameFrom(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	_, bo := aReconcilableEstate(t, c, ts)

	postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[
		{"subject":"ada@example.org","ref":"CN=VPN-Users"},
		{"subject":"bo@example.org","ref":"CN=VPN-Users"}]}`)

	id := findingOfKind(t, openFindings(t, c, ts), "unmanaged")
	code, body := cReq(t, c, ts, "POST",
		"/api/v1/reconciliation/"+id+"/adopt", "")
	if code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}

	held := heldBy(t, c, ts, bo)
	if len(held) != 1 || held[0]["itemId"] != "vpn" {
		t.Fatalf("Bo's inventory after adopting = %+v", held)
	}
	if held[0]["origin"] != "adopted" {
		t.Errorf("origin = %v, want adopted: Atlas did not grant this and must not say it did",
			held[0]["origin"])
	}
	if open := openFindings(t, c, ts); len(open) != 0 {
		t.Errorf("the finding is still open after being adopted: %+v", open)
	}
}

// TestRevokingAMissingRightStopsAtlasAssertingIt.
func TestRevokingAMissingRightStopsAtlasAssertingIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	ada, _ := aReconcilableEstate(t, c, ts)

	postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[]}`)
	id := findingOfKind(t, openFindings(t, c, ts), "missing")

	code, body := cReq(t, c, ts, "POST", "/api/v1/reconciliation/"+id+"/revoke", "")
	if code != http.StatusOK {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if held := heldBy(t, c, ts, ada); len(held) != 0 {
		t.Errorf("Atlas still asserts a right the target system does not have: %+v", held)
	}
}

// TestAnActionRefusesTheWrongDirection.
//
// The two directions of a disagreement do not take the same remedy, and a handler
// that accepted either would adopt a right nobody holds or revoke a record that
// does not exist.
func TestAnActionRefusesTheWrongDirection(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aReconcilableEstate(t, c, ts)

	postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[
		{"subject":"bo@example.org","ref":"CN=VPN-Users"}]}`)
	open := openFindings(t, c, ts)

	missing := findingOfKind(t, open, "missing")
	code, body := cReq(t, c, ts, "POST", "/api/v1/reconciliation/"+missing+"/adopt", "")
	if code != http.StatusConflict {
		t.Errorf("adopting a missing finding answered %d, want a refusal: %s", code, body)
	}

	unmanaged := findingOfKind(t, open, "unmanaged")
	code, body = cReq(t, c, ts, "POST", "/api/v1/reconciliation/"+unmanaged+"/revoke", "")
	if code != http.StatusConflict {
		t.Errorf("revoking an unmanaged finding answered %d, want a refusal: %s", code, body)
	}

	// And acting twice on one finding is refused with what happened to it, so the
	// caller is not sent back to the list to work out why.
	if code, _ := cReq(t, c, ts, "POST", "/api/v1/reconciliation/"+unmanaged+"/adopt", ""); code != http.StatusOK {
		t.Fatalf("the first adopt failed: %d", code)
	}
	code, body = cReq(t, c, ts, "POST", "/api/v1/reconciliation/"+unmanaged+"/adopt", "")
	if code != http.StatusConflict || !strings.Contains(string(body), "adopted") {
		t.Errorf("a second adopt answered %d %s; it has to say how the finding was closed",
			code, body)
	}
}

// TestTheActionsAreNotReachableByAWorkerCredential.
//
// A scheduled process may compare — that writes a journal entry and nothing else.
// It may not adopt, revoke or deprovision: each of those either changes what Atlas
// asserts about somebody's access or takes access away, and neither belongs behind
// a credential a model carries.
func TestTheActionsAreNotReachableByAWorkerCredential(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/api-tokens",
		`{"name":"reconciler","scope":"inventory","roles":["operator"]}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("mint token: %d %s", code, body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, body)
	}

	call := func(path string) int {
		req, err := http.NewRequest("POST", ts.URL+path, strings.NewReader(`{"system":"ad","refs":["x"]}`))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := newClient(t).Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	if got := call("/api/v1/reconciliation"); got != http.StatusOK {
		t.Errorf("the run is not reachable with the inventory scope: %d", got)
	}
	for _, act := range []string{"adopt", "revoke", "deprovision"} {
		if got := call("/api/v1/reconciliation/anything/" + act); got != http.StatusForbidden {
			t.Errorf("%s answered %d for a worker credential; every action needs a person", act, got)
		}
	}
}

// findingOfKind picks one open finding of a kind, failing loudly when there is
// none — a test that silently reconciled nothing would pass while checking nothing.
func findingOfKind(t *testing.T, open []map[string]any, kind string) string {
	t.Helper()
	for _, f := range open {
		if f["kind"] == kind {
			return fmt.Sprint(f["id"])
		}
	}
	t.Fatalf("no open finding of kind %q among %+v", kind, open)
	return ""
}
