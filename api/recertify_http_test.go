package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The recertification routes, over HTTP (ADR-draft-access-recertification).
//
// The inventory these run against is filled through the commissioning load, for
// the reason the reconciliation tests give: the slices meet here, and a test that
// wrote entitlements another way would prove the campaign works against something
// no installation will ever hold.

// openCampaign opens one and returns the report.
func openCampaign(t *testing.T, c *http.Client, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST", "/api/v1/recertification", body)
	if code != http.StatusCreated {
		t.Fatalf("POST recertification: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode campaign: %v (%s)", err, raw)
	}
	return out
}

// readCampaign reads one back.
func readCampaign(t *testing.T, c *http.Client, ts *httptest.Server, id, query string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/recertification/"+id+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET recertification: %d %s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode campaign: %v", err)
	}
	return out
}

// rowsOf pulls the rows out of a campaign report.
func rowsOf(t *testing.T, rep map[string]any) []map[string]any {
	t.Helper()
	raw, _ := rep["rows"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("a row is not an object: %#v", r)
		}
		out = append(out, m)
	}
	return out
}

func countsOf(t *testing.T, rep map[string]any) map[string]any {
	t.Helper()
	c, ok := rep["counts"].(map[string]any)
	if !ok {
		t.Fatalf("no counts in %+v", rep)
	}
	return c
}

// aCertifiableEstate gives Ada a recorded VPN right and Bo an account with none.
func aCertifiableEstate(t *testing.T, c *http.Client, ts *httptest.Server) (ada, bo string) {
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

// TestACampaignAsksAQuestionPerRightAndAnswersNone.
//
// Opening a campaign must change nothing at all. It reads the inventory and turns
// it into questions; every answer is somebody's, later, one at a time.
func TestACampaignAsksAQuestionPerRightAndAnswersNone(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	ada, _ := aCertifiableEstate(t, c, ts)

	rep := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)

	counts := countsOf(t, rep)
	if counts["rows"] != float64(1) || counts["undecided"] != float64(1) {
		t.Errorf("counts = %+v, want one row and it undecided", counts)
	}
	if counts["kept"] != float64(0) || counts["revoked"] != float64(0) {
		t.Errorf("opening a campaign decided something: %+v", counts)
	}
	rows := rowsOf(t, rep)
	if len(rows) != 1 {
		t.Fatalf("%d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0]["decision"] != nil {
		t.Errorf("a fresh row carries a decision (%v); undecided is the state it starts in",
			rows[0]["decision"])
	}
	if rows[0]["origin"] != "legacy" {
		t.Errorf("origin = %v, want the legacy the load recorded — a reviewer judging an "+
			"`ordered` right and a found one is answering different questions", rows[0]["origin"])
	}

	// The inventory is exactly as it was.
	if held := heldBy(t, c, ts, ada); len(held) != 1 {
		t.Errorf("opening a campaign changed Ada's inventory: %+v", held)
	}
}

// TestSilenceIsNeverADecision.
//
// The whole record hangs on this. A campaign closes with unanswered rows in it, and
// they stay unanswered: closing that certified them would manufacture a signature
// nobody gave, and closing that revoked them would take access away because
// somebody was on holiday.
func TestSilenceIsNeverADecision(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	ada, _ := aCertifiableEstate(t, c, ts)

	opened := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	id := fmt.Sprint(opened["id"])

	code, raw := cReq(t, c, ts, "POST", "/api/v1/recertification/"+id+"/close", "")
	if code != http.StatusOK {
		t.Fatalf("close: %d %s", code, raw)
	}
	var closed map[string]any
	if err := json.Unmarshal(raw, &closed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	counts := countsOf(t, closed)
	if counts["undecided"] != float64(1) {
		t.Errorf("undecided = %v after closing, want the unanswered row still counted as one",
			counts["undecided"])
	}
	if counts["kept"] != float64(0) {
		t.Errorf("closing certified %v row(s) nobody answered", counts["kept"])
	}
	if held := heldBy(t, c, ts, ada); len(held) != 1 {
		t.Errorf("closing took away a right nobody decided about: %+v", held)
	}

	// And a closed campaign takes no more answers: what it recorded is what was
	// decided while it ran.
	rows := rowsOf(t, closed)
	code, raw = cReq(t, c, ts,
		"POST", "/api/v1/recertification/"+id+"/rows/"+fmt.Sprint(rows[0]["id"])+"/keep", "")
	if code != http.StatusConflict {
		t.Errorf("answering a closed campaign: %d %s, want 409", code, raw)
	}
}

// TestAnAttestationIsNotEditable.
//
// A row is evidence of one moment. If a decision could be replaced, the record
// would answer "what did they decide" with whatever was written last — which is
// the one question it exists to answer truthfully.
func TestAnAttestationIsNotEditable(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCertifiableEstate(t, c, ts)

	opened := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	id := fmt.Sprint(opened["id"])
	row := fmt.Sprint(rowsOf(t, opened)[0]["id"])

	code, raw := cReq(t, c, ts, "POST",
		"/api/v1/recertification/"+id+"/rows/"+row+"/keep", `{"note":"still in the team"}`)
	if code != http.StatusOK {
		t.Fatalf("keep: %d %s", code, raw)
	}
	var decided map[string]any
	if err := json.Unmarshal(raw, &decided); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decided["decision"] != "keep" || decided["note"] != "still in the team" {
		t.Errorf("the row does not carry what was decided: %+v", decided)
	}
	if decided["decidedBy"] == nil || decided["decidedBy"] == "" {
		t.Error("the row records no author; an attestation without one is not evidence")
	}

	code, raw = cReq(t, c, ts, "POST",
		"/api/v1/recertification/"+id+"/rows/"+row+"/revoke", "")
	if code != http.StatusConflict {
		t.Errorf("a decided row was decided again: %d %s, want 409", code, raw)
	}
	if !strings.Contains(string(raw), "not editable") {
		t.Errorf("the refusal does not say why: %s", raw)
	}
}

// TestWithdrawingARightRunsTheProductsOwnProcess.
//
// Never a direct worker call. Nothing reaches a target system except through a
// modelled process — a recertification that bypassed that would be a second
// deprovisioning path invisible to the diagram and the audit trail.
func TestWithdrawingARightRunsTheProductsOwnProcess(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCertifiableEstate(t, c, ts)

	opened := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	id := fmt.Sprint(opened["id"])
	row := fmt.Sprint(rowsOf(t, opened)[0]["id"])

	// No process named "d" is deployed, so the run fails — and that is the
	// assertion: the revoke went looking for the product's process rather than
	// reaching into a target system itself.
	code, raw := cReq(t, c, ts, "POST",
		"/api/v1/recertification/"+id+"/rows/"+row+"/revoke", "")
	if code != http.StatusInternalServerError {
		t.Fatalf("revoke with no deployed process: %d %s, want it to fail on the process", code, raw)
	}
	if !strings.Contains(string(raw), "no deployed process") {
		t.Errorf("the failure is not about the product's process: %s", raw)
	}

	// And the row is still undecided, because the act failed. The order matters:
	// recording first would leave an attestation for something that never happened.
	rep := readCampaign(t, c, ts, id, "")
	if counts := countsOf(t, rep); counts["undecided"] != float64(1) {
		t.Errorf("counts = %+v, want the row still unanswered after the act failed", counts)
	}
}

// TestARowUnderAnOpenFindingSaysSo.
//
// Certifying a right two systems currently disagree about is signing a statement
// about something contested. It is marked rather than refused — one finding must
// not block a campaign over an estate — but the reviewer is told before they
// answer.
func TestARowUnderAnOpenFindingSaysSo(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCertifiableEstate(t, c, ts)

	// Reality moved: the group is empty, so Ada's recorded right is missing.
	postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[]}`)

	rep := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	rows := rowsOf(t, rep)
	if len(rows) != 1 {
		t.Fatalf("%d row(s), want 1", len(rows))
	}
	if rows[0]["disputed"] != true {
		t.Errorf("the row does not say the right is contested: %+v. A reviewer certifying it "+
			"would be attesting to something the target system denies", rows[0])
	}
	if rows[0]["disputeKind"] != "missing" {
		t.Errorf("disputeKind = %v, want the direction of the disagreement", rows[0]["disputeKind"])
	}
	if countsOf(t, rep)["disputed"] != float64(1) {
		t.Errorf("the campaign does not count its disputed rows: %+v", countsOf(t, rep))
	}
}

// TestARowIsAnsweredByThePersonItWasAddressedTo.
//
// Authority is per row and not per role: the reviewer is a line manager, which is
// an ordinary user. Signing somebody else's attestation would record a judgement
// they never made.
func TestARowIsAnsweredByThePersonItWasAddressedTo(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	_, bo := aCertifiableEstate(t, admin, ts)

	// Bo reviews Ada's rights. Bo is an ordinary user and nothing else.
	opened := openCampaign(t, admin, ts, fmt.Sprintf(
		`{"name":"Q3 access review","reviewers":{"ada@example.org":%q}}`, bo))
	id := fmt.Sprint(opened["id"])
	rows := rowsOf(t, opened)
	if rows[0]["reviewer"] != bo {
		t.Fatalf("the row was not addressed to Bo: %+v", rows[0])
	}
	row := fmt.Sprint(rows[0]["id"])

	// A third party may not answer it, whatever they think of the right.
	anAccountWithMail(t, admin, ts, "cy", "cy@example.org")
	cy := newClient(t)
	login(t, cy, ts, "cy", "correct horse battery")
	code, raw := cReq(t, cy, ts, "POST", "/api/v1/recertification/"+id+"/rows/"+row+"/keep", "")
	if code != http.StatusForbidden {
		t.Errorf("somebody else signed the attestation: %d %s, want 403", code, raw)
	}

	// Bo may, and `?mine=true` is how Bo finds it without reading the estate.
	boC := newClient(t)
	login(t, boC, ts, "bo", "correct horse battery")
	mine := readCampaign(t, boC, ts, id, "?mine=true")
	if got := len(rowsOf(t, mine)); got != 1 {
		t.Errorf("Bo's own view has %d row(s), want the 1 addressed to them", got)
	}
	if code, raw := cReq(t, boC, ts,
		"POST", "/api/v1/recertification/"+id+"/rows/"+row+"/keep", ""); code != http.StatusOK {
		t.Fatalf("the reviewer could not answer their own row: %d %s", code, raw)
	}
}

// TestAnUnassignedRowBelongsToWhoeverOpenedTheCampaign.
//
// Empty is the answer "nobody", not an error — one person with no manager in the
// directory must not stop the recertification of an estate. But unlike an
// unassigned user task it is not open to everybody: an attestation nobody was asked
// for is still a signature, and somebody has to be accountable for it.
func TestAnUnassignedRowBelongsToWhoeverOpenedTheCampaign(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	aCertifiableEstate(t, admin, ts)

	opened := openCampaign(t, admin, ts, `{"name":"Q3 access review"}`)
	id := fmt.Sprint(opened["id"])
	rows := rowsOf(t, opened)
	if rows[0]["reviewer"] != nil {
		t.Fatalf("the row was addressed to somebody: %+v", rows[0])
	}
	if countsOf(t, opened)["unassigned"] != float64(1) {
		t.Errorf("the campaign does not count what nobody was asked: %+v", countsOf(t, opened))
	}
	row := fmt.Sprint(rows[0]["id"])

	// An ordinary user is not the campaign's owner and may not answer it.
	anAccountWithMail(t, admin, ts, "cy", "cy@example.org")
	cy := newClient(t)
	login(t, cy, ts, "cy", "correct horse battery")
	if code, raw := cReq(t, cy, ts,
		"POST", "/api/v1/recertification/"+id+"/rows/"+row+"/keep", ""); code != http.StatusForbidden {
		t.Errorf("an unassigned row was answerable by anybody: %d %s, want 403", code, raw)
	}
	// The owner may.
	if code, raw := cReq(t, admin, ts,
		"POST", "/api/v1/recertification/"+id+"/rows/"+row+"/keep", ""); code != http.StatusOK {
		t.Errorf("the campaign's owner could not answer the row nobody was asked: %d %s", code, raw)
	}
}

// TestACampaignThatAsksNothingSaysWhy.
//
// An empty campaign and a healthy estate look identical from outside, and the usual
// cause is neither — it is a product id nobody holds.
func TestACampaignThatAsksNothingSaysWhy(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCertifiableEstate(t, c, ts)

	rep := openCampaign(t, c, ts, `{"name":"Mailboxes","items":["mailbox"]}`)
	if countsOf(t, rep)["rows"] != float64(0) {
		t.Fatalf("the campaign found rows for a product nobody holds: %+v", rep)
	}
	reason, _ := rep["reason"].(string)
	if !strings.Contains(reason, "Check the product ids") {
		t.Errorf("reason = %q; an empty campaign that reads as an estate with nothing to certify "+
			"is the one answer nobody should take at face value", reason)
	}
}

// TestACampaignNeedsAName — what an auditor cites a year later.
func TestACampaignNeedsAName(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, raw := cReq(t, c, ts, "POST", "/api/v1/recertification", `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("a nameless campaign was accepted: %d %s", code, raw)
	}
	if !strings.Contains(string(raw), "name") {
		t.Errorf("the refusal does not say what is missing: %s", raw)
	}
}

// TestTheCampaignsAreListedNewestFirst.
//
// The list is how a reviewer reaches a campaign at all: they are told an access
// review is running, not given its id. Newest first because the one being answered
// is almost always the most recent, and a reviewer scrolling past four closed
// quarters to reach this one is a reviewer who answers faster than they read.
func TestTheCampaignsAreListedNewestFirst(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	aCertifiableEstate(t, c, ts)

	openCampaign(t, c, ts, `{"name":"Q2 access review"}`)
	openCampaign(t, c, ts, `{"name":"Q3 access review"}`)

	code, raw := cReq(t, c, ts, "GET", "/api/v1/recertification", "")
	if code != http.StatusOK {
		t.Fatalf("GET recertification: %d %s", code, raw)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("%d campaign(s), want both: %+v", len(list), list)
	}
	// Headers only: a list that carried every row of every campaign would send an
	// estate's worth of access down a route whose question is "which campaigns exist".
	if list[0]["rows"] != nil {
		t.Errorf("the list carries rows; it answers which campaigns exist, not what is in them")
	}
	if got := fmt.Sprint(list[0]["name"]); !strings.Contains(got, "Q3") {
		t.Errorf("first listed campaign is %q, want the newest", got)
	}
}

// TestARightAlreadyGoneIsRecordedRatherThanDeprovisioned.
//
// A campaign is a snapshot and the estate moves under it. A decision about a right
// that is no longer there is still a decision somebody made — so it is recorded,
// and nothing is started: a deprovisioning against nothing would park an instance
// raising an incident about a fact rather than a fault.
func TestARightAlreadyGoneIsRecordedRatherThanDeprovisioned(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")
	ada, _ := aCertifiableEstate(t, c, ts)

	opened := openCampaign(t, c, ts, `{"name":"Q3 access review"}`)
	id := fmt.Sprint(opened["id"])
	row := fmt.Sprint(rowsOf(t, opened)[0]["id"])

	// Between the question and the answer, the right goes: a reconciliation finds
	// that the target system does not have it, and somebody revokes the record Atlas
	// could not substantiate.
	postReconcile(t, c, ts, `{"system":"ad","refs":["CN=VPN-Users"],"observations":[]}`)
	finding := findingOfKind(t, openFindings(t, c, ts), "missing")
	if code, raw := cReq(t, c, ts,
		"POST", "/api/v1/reconciliation/"+finding+"/revoke", ""); code != http.StatusOK {
		t.Fatalf("revoking the unsubstantiated record: %d %s", code, raw)
	}
	if held := heldBy(t, c, ts, ada); len(held) != 0 {
		t.Fatalf("the right is still recorded: %+v", held)
	}

	// The reviewer answers the question they were asked, which is now about
	// something that is not there. No deployed process exists, so a run would fail —
	// and it does not, which is the assertion.
	code, raw := cReq(t, c, ts, "POST",
		"/api/v1/recertification/"+id+"/rows/"+row+"/revoke", "")
	if code != http.StatusOK {
		t.Fatalf("withdrawing a right that had already gone: %d %s", code, raw)
	}
	var decided map[string]any
	if err := json.Unmarshal(raw, &decided); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decided["outcome"] != "already-gone" {
		t.Errorf("outcome = %v, want the record to say that nothing was started",
			decided["outcome"])
	}
	if decided["decision"] != "revoke" {
		t.Errorf("the judgement was not recorded: %+v. It is still a decision somebody made",
			decided)
	}
}
