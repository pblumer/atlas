package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// TestEntitlementHistoryNeedsSomebodyToAskAbout: with nobody signed in and nobody
// named, there is no subject, and answering with an empty history would read as a
// clean record for a person nobody identified.
func TestEntitlementHistoryNeedsSomebodyToAskAbout(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/entitlements/history", "", "")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "no principal") {
		t.Errorf("no subject: %d (%s), want 400", code, body)
	}
}

// TestEntitlementHistoryAtAMomentCountsWhatWasHeldAndWhatWasOnlyClaimed asks what one
// person held on a given day. A right granted after that day is not part of the
// answer; a right still held that began before it is; and a hold the record later
// corrected — the system never really had it — is counted as claimed rather than
// held. The list is bounded by the budget and says what it left out.
func TestEntitlementHistoryAtAMomentCountsWhatWasHeldAndWhatWasOnlyClaimed(t *testing.T) {
	l := limits.Default()
	l.HistoryReport = 1
	srv := newServerWithOptions(t, WithLimits(l))
	conflictsPathsGrant(t, srv,
		model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", Since: 1000},
		model.EntitlementValue{Principal: "usr_ada", ItemID: "mail", Since: 1500},
		model.EntitlementValue{Principal: "usr_ada", ItemID: "wiki", Since: 5000},
	)
	srv.do(func() { srv.proc.RevokeEntitlement("usr_ada", "vpn", 3000, model.EndCorrected, "usr_ops") })
	if err := srv.drive(); err != nil {
		t.Fatalf("drive revocation: %v", err)
	}

	code, body := serveInternal(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/entitlements/history?principal=usr_ada&at=%d", 2000), "", "")
	if code != http.StatusOK {
		t.Fatalf("history: %d (%s)", code, body)
	}
	var rep historyResp
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if rep.Principal != "usr_ada" || rep.Counts.Held != 1 || rep.Counts.Claimed != 1 {
		t.Errorf("report = %+v, want usr_ada with one right held and one only claimed at that moment", rep)
	}
	if rep.Omitted != 1 || len(rep.Items) != 1 {
		t.Errorf("omitted=%d listed=%d, want one listed and one said to be left out", rep.Omitted, len(rep.Items))
	}
	for _, it := range rep.Items {
		if strings.Contains(fmt.Sprintf("%+v", it), "wiki") {
			t.Errorf("a right granted after the moment was listed: %+v", it)
		}
	}
}
