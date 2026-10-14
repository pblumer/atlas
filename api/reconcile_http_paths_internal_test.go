package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// The reconciliation route's refusals and failure answers (ADR-0334). Every one of
// them must leave the discrepancy journal exactly as it was: a run that was
// refused, or that could not read what it compares against, has concluded
// nothing, and a finding recorded from it would be a conclusion nobody drew.

// reconcileHTTPPathsEstate gives the server a catalogue of one product claiming
// AD's CN=VPN, an account for Ada that the directory knows as oid-ada, and Ada
// recorded as holding the product.
func reconcileHTTPPathsEstate(t *testing.T, s *Server) {
	t.Helper()
	var err error
	s.do(func() {
		if err = s.catalogStore.SaveItem(catalog.Item{ID: "vpn", State: catalog.StateActive,
			ProvisionProcess: "p", DeprovisionProcess: "d",
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}); err != nil {
			return
		}
		err = s.users.Save(User{ID: "usr_ada", Username: "ada", Email: "ada@example.org", DirectoryID: "oid-ada"})
	})
	if err != nil {
		t.Fatalf("seed estate: %v", err)
	}
	recertifyHTTPPathsGrant(t, s, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
}

// reconcileHTTPPathsOpen lists the open findings, optionally for one system.
func reconcileHTTPPathsOpen(t *testing.T, s *Server, query string) []discrepancyRecord {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/reconciliation"+query, nil)
	if code != http.StatusOK {
		t.Fatalf("list findings: %d %s", code, raw)
	}
	var out []discrepancyRecord
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode findings: %v (%s)", err, raw)
	}
	return out
}

// reconcileHTTPPathsPost runs one reconciliation.
func reconcileHTTPPathsPost(t *testing.T, s *Server, body string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/reconciliation", strings.NewReader(body))
}

// TestReconcileHTTPRefusesAReadingItCannotTrust. A body that cannot be read or
// parsed, a reading that does not say which system it came from, and one larger
// than its budget: all refused before anything is compared, and the journal is
// untouched.
func TestReconcileHTTPRefusesAReadingItCannotTrust(t *testing.T) {
	budgets := limits.Default()
	budgets.ReconcileObservations = 1
	srv := newServerWithOptions(t, WithLimits(budgets))
	reconcileHTTPPathsEstate(t, srv)

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/reconciliation", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("unreadable = %d %s, want 400", code, body)
	}
	cases := []struct {
		name, body, want string
		code             int
	}{
		{"truncated JSON", `{"system":`, "invalid JSON body", http.StatusBadRequest},
		{"no system", `{"system":"  ","refs":["CN=VPN"]}`, `name the target system`, http.StatusBadRequest},
		{"over the budget", `{"system":"ad","refs":["CN=VPN"],"observations":[
			{"subject":"oid-bo","ref":"CN=VPN"},{"subject":"oid-cy","ref":"CN=VPN"}]}`,
			"carries 2 observations and the ceiling is 1", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		code, body := reconcileHTTPPathsPost(t, srv, tc.body)
		if code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s = %d %s, want %d containing %q", tc.name, code, body, tc.code, tc.want)
		}
	}
	if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 0 {
		t.Errorf("a refused reading recorded findings: %+v", open)
	}
}

// TestReconcileHTTPAServerShuttingDownSaysSo. "No findings" from a server that is
// stopping would read as a clean estate.
func TestReconcileHTTPAServerShuttingDownSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	h := srv.Handler()
	closeSrv()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/reconciliation",
		strings.NewReader(`{"system":"ad","refs":["CN=VPN"]}`))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("run = %d %s, want 503", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodGet, "/api/v1/reconciliation", nil)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("list = %d %s, want 503", code, body)
	}
}

// TestReconcileHTTPAStoreThatCannotBeReadIsAFault. A run that cannot read the
// accounts cannot resolve a single subject, and one that cannot read the journal
// cannot tell a new finding from a standing one; both stop with a 500 rather than
// compare against half a picture.
func TestReconcileHTTPAStoreThatCannotBeReadIsAFault(t *testing.T) {
	t.Run("the accounts", func(t *testing.T) {
		srv := newServerForErrors(t)
		reconcileHTTPPathsEstate(t, srv)
		recertifyHTTPPathsBreakDir(t, srv.users.Dir())

		code, body := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"]}`)
		if code != http.StatusInternalServerError || !strings.Contains(body, "reconcile:") {
			t.Errorf("run = %d %s, want 500", code, body)
		}
		if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 0 {
			t.Errorf("findings recorded without the accounts: %+v", open)
		}
	})

	t.Run("the journal", func(t *testing.T) {
		srv := newServerForErrors(t)
		reconcileHTTPPathsEstate(t, srv)
		recertifyHTTPPathsBreakDir(t, srv.discrepancies.Dir())

		code, body := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"]}`)
		if code != http.StatusInternalServerError || !strings.Contains(body, "read the discrepancy journal") {
			t.Errorf("run = %d %s, want 500 naming the journal", code, body)
		}
		code, body = recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/reconciliation", nil)
		if code != http.StatusInternalServerError || !strings.Contains(body, "discrepancies:") {
			t.Errorf("list = %d %s, want 500 rather than an empty list", code, body)
		}
	})
}

// TestReconcileHTTPTheListNarrowsToOneSystem. An operator working through the
// AD findings must not have to read the SAP ones to find them.
func TestReconcileHTTPTheListNarrowsToOneSystem(t *testing.T) {
	srv := newServerForErrors(t)
	now := time.Now().Unix()
	for _, rec := range []discrepancyRecord{
		{ID: "missing:ad:usr_ada:vpn", Kind: recMissing, System: "ad", Principal: "usr_ada",
			ItemID: "vpn", OpenedAt: now, LastSeenAt: now},
		{ID: "missing:sap:usr_ada:erp", Kind: recMissing, System: "sap", Principal: "usr_ada",
			ItemID: "erp", OpenedAt: now, LastSeenAt: now},
	} {
		seedDiscrepancy(t, srv, rec)
	}

	if all := reconcileHTTPPathsOpen(t, srv, ""); len(all) != 2 {
		t.Fatalf("unfiltered = %+v, want both", all)
	}
	ad := reconcileHTTPPathsOpen(t, srv, "?system=ad")
	if len(ad) != 1 || ad[0].System != "ad" {
		t.Errorf("?system=ad = %+v, want only the AD finding", ad)
	}
	if none := reconcileHTTPPathsOpen(t, srv, "?system=ldap"); len(none) != 0 {
		t.Errorf("?system=ldap = %+v, want nothing", none)
	}
}

// TestReconcileHTTPAContestedReferenceLeavesNothingToActOn. Over the route, a
// reading under a group two products claim records no finding: the journal holds
// nothing for Ada's vpn-a that adopting or deprovisioning could be pointed at.
func TestReconcileHTTPAContestedReferenceLeavesNothingToActOn(t *testing.T) {
	srv := newServerForErrors(t)
	reconcileHTTPPathsEstate(t, srv) // vpn claims CN=VPN, and Ada holds it
	var err error
	srv.do(func() {
		err = srv.catalogStore.SaveItem(catalog.Item{ID: "vpn-too", State: catalog.StateActive,
			ProvisionProcess: "p", DeprovisionProcess: "d",
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}})
	})
	if err != nil {
		t.Fatalf("seed second claimant: %v", err)
	}

	code, raw := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"],"observations":[
		{"subject":"oid-ada","ref":"CN=VPN"}]}`)
	if code != http.StatusOK {
		t.Fatalf("run = %d %s", code, raw)
	}
	var rep reconcileReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.Counts.Unmanaged != 0 || rep.Counts.Ambiguous != 1 || rep.Opened != 0 {
		t.Errorf("counts = %+v opened = %d, want the reference noted and nothing recorded", rep.Counts, rep.Opened)
	}
	if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 0 {
		t.Errorf("journal = %+v, want nothing to act on", open)
	}
	for _, kind := range []string{recUnmanaged, recMissing} {
		for _, item := range []string{"vpn", "vpn-too"} {
			id := discrepancyID(kind, "ad", "usr_ada", item)
			if code, body := reconcileActionsPathsAct(t, srv, id, "adopt"); code != http.StatusNotFound {
				t.Errorf("adopt %s = %d %s, want 404", id, code, body)
			}
		}
	}
}
