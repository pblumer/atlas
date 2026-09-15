package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// What a hold leaves behind, end to end (ADR-draft-entitlement-history).
//
// The claim: the remedy must not destroy the evidence of the problem. Everything
// the portal learned to detect is about a *held* right, and every remedy ends the
// hold — so without this, acting on a finding deletes it.

type historyResponse struct {
	Principal string `json:"principal"`
	At        int64  `json:"at"`
	Items     []struct {
		ItemID      string `json:"itemId"`
		Since       int64  `json:"since"`
		EndedAt     int64  `json:"endedAt"`
		Until       int64  `json:"until"`
		OverdueDays int    `json:"overdueDays"`
		Origin      string `json:"origin"`
		OrderID     string `json:"orderId"`
		Reason      string `json:"reason"`
		Held        bool   `json:"held"`
		EndedBy     string `json:"endedBy"`
	} `json:"items"`
	Counts struct {
		Held    int `json:"held"`
		Claimed int `json:"claimed"`
	} `json:"counts"`
	Note string `json:"note"`
}

func historyOf(t *testing.T, ts *httptest.Server, c *http.Client, query string) (int, historyResponse) {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/entitlements/history"+query, "")
	if code != http.StatusOK {
		return code, historyResponse{}
	}
	var got historyResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}
	return code, got
}

// TestAReturnedRightIsStillAnswerableAfterItIsGone.
//
// The whole slice in one sequence: hold it, give it back, and ask what was true
// while it was held. Before this the last step had no answer at all — the
// inventory row was deleted and the order behind it is deleted by retention long
// before anybody asks.
func TestAReturnedRightIsStillAnswerableAfterItIsGone(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", deprovisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy the revocation: %d (%s)", code, b)
	}

	// Nothing has ended, and the answer says so in words rather than as an empty
	// list — an empty list reads as a clean record.
	_, before := historyOf(t, ts, admin, "")
	if len(before.Items) != 0 || before.Note == "" {
		t.Fatalf("history before anything ended = %+v, note %q", before.Items, before.Note)
	}

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}
	_, held := inventoryOf(t, ts, admin, "")
	if len(held.Items) != 1 {
		t.Fatalf("inventory after provisioning = %+v, want the vpn", held.Items)
	}
	heldSince := held.Items[0].Since

	if code, b := returnLine(t, ts, admin, orderID, "vpn"); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report returned: %d (%s)", code, b)
	}

	// The inventory has forgotten it, which is correct — and is exactly the loss
	// this family exists to stop being total.
	if _, got := inventoryOf(t, ts, admin, ""); len(got.Items) != 0 {
		t.Fatalf("inventory after the return = %+v, want empty", got.Items)
	}

	_, after := historyOf(t, ts, admin, "")
	if len(after.Items) != 1 {
		t.Fatalf("history after the return = %+v, want the one ended hold", after.Items)
	}
	row := after.Items[0]
	if row.ItemID != "vpn" {
		t.Errorf("itemId = %q, want vpn", row.ItemID)
	}
	if row.Since != heldSince {
		t.Errorf("since = %d, want the moment the hold began (%d) — the row carries the "+
			"period, not the moment it was written", row.Since, heldSince)
	}
	if row.EndedAt == 0 {
		t.Error("the row has no end; a period with one date is not a period")
	}
	if row.Reason != "returned" || !row.Held {
		t.Errorf("reason = %q held = %v, want a returned hold to be evidence of access",
			row.Reason, row.Held)
	}
	if row.OrderID != orderID {
		t.Errorf("orderId = %q, want %q — the row keeps the id knowing the order itself "+
			"will be deleted, because it still says which request this came through",
			row.OrderID, orderID)
	}
	if row.EndedBy == "" {
		t.Error("nobody is recorded as having ended it, which is half of what an " +
			"access review asks")
	}
	if after.Counts.Held != 1 || after.Counts.Claimed != 0 {
		t.Errorf("counts = %+v, want one held and no claimed", after.Counts)
	}

	// And the question an access review actually asks: what did the record say on
	// a day inside the period.
	_, during := historyOf(t, ts, admin, fmt.Sprintf("?at=%d", heldSince+1))
	if len(during.Items) != 1 || during.Items[0].ItemID != "vpn" {
		t.Fatalf("?at= inside the hold = %+v, want the vpn", during.Items)
	}
	if during.At != heldSince+1 {
		t.Errorf("the moment is not echoed (%d); an answer without its question is one "+
			"somebody will attribute to the wrong day", during.At)
	}
	_, beforeIt := historyOf(t, ts, admin, fmt.Sprintf("?at=%d", heldSince-1))
	if len(beforeIt.Items) != 0 {
		t.Errorf("?at= before the hold began = %+v, want nothing", beforeIt.Items)
	}
	_, afterIt := historyOf(t, ts, admin, fmt.Sprintf("?at=%d", row.EndedAt))
	if len(afterIt.Items) != 0 {
		t.Errorf("?at= at the moment it ended = %+v, want nothing: the interval is "+
			"half-open so a right returned and re-granted is not counted twice", afterIt.Items)
	}
}

// TestWhatIsStillHeldAnswersAPastDayToo.
//
// A route that listed only ended holds would answer "what had ended by then"
// while appearing to answer "what did they hold". A right granted before the
// moment and never given back is the most ordinary way to hold something on a day.
func TestWhatIsStillHeldAnswersAPastDayToo(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}
	_, held := inventoryOf(t, ts, admin, "")
	if len(held.Items) != 1 {
		t.Fatalf("inventory = %+v, want the vpn", held.Items)
	}

	_, got := historyOf(t, ts, admin, fmt.Sprintf("?at=%d", held.Items[0].Since+1))
	if len(got.Items) != 1 || got.Items[0].ItemID != "vpn" {
		t.Fatalf("?at= = %+v, want the still-held vpn", got.Items)
	}
	if got.Items[0].Reason != "held" || !got.Items[0].Held {
		t.Errorf("reason = %q held = %v, want a live hold marked as still held",
			got.Items[0].Reason, got.Items[0].Held)
	}
	if got.Items[0].EndedAt != 0 {
		t.Errorf("endedAt = %d on a hold that has not ended — filling it with the moment "+
			"asked about turns \"still held\" into \"ended that day\" for whoever reads "+
			"this in five years", got.Items[0].EndedAt)
	}
}

// TestSomebodyElsesAccessHistoryNeedsTheAdminRole.
//
// The inventory's rule, and if anything sharper: a person's access history is
// their inventory plus everything it ever was.
func TestSomebodyElsesAccessHistoryNeedsTheAdminRole(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	stranger := twoUsers(t, ts, admin, "mallory")[0]

	if code, _ := historyOf(t, ts, stranger, "?principal=root"); code != http.StatusForbidden {
		t.Errorf("a stranger reading root's access history = %d, want 403", code)
	}
	if code, _ := historyOf(t, ts, stranger, ""); code != http.StatusOK {
		t.Errorf("own history = %d, want 200 — empty rather than refused", code)
	}
}

// TestAMomentThatIsNotAMomentIsRefused.
//
// Rather than silently read as zero, which would turn "what did they hold on the
// day I mistyped" into "everything that has ever ended" with no sign anything
// went wrong.
func TestAMomentThatIsNotAMomentIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for _, bad := range []string{"gestern", "0", "-5", "2026-13-45"} {
		if code, _ := cReq(t, admin, ts, "GET",
			"/api/v1/entitlements/history?at="+bad, ""); code != http.StatusBadRequest {
			t.Errorf("?at=%q = %d, want 400", bad, code)
		}
	}
	// And an RFC 3339 moment is accepted, because a person pastes a date and a
	// modelled process renders one.
	if code, _ := cReq(t, admin, ts, "GET",
		"/api/v1/entitlements/history?at=2026-03-03T12:00:00Z", ""); code != http.StatusOK {
		t.Errorf("an RFC 3339 moment = %d, want 200", code)
	}
}
