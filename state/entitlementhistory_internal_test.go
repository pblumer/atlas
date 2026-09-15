package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// Closing a hold, at the layer that does it (ADR-draft-entitlement-history).
//
// The engine has its own tests for the fold, but coverage is measured per
// package: a function in this package that only another package's tests reach
// counts as untested here, and rightly — the store's contract is the store's to
// hold.

func heldRow(principal, itemID string, since int64) *model.EntitlementValue {
	return &model.EntitlementValue{
		Principal: principal, ItemID: itemID, VariantID: "gold", OrderID: "ord_7",
		Since: since, Until: since + 500, Origin: model.OriginOrdered,
	}
}

func endedOf(t *testing.T, s *Store, principal string) []model.EntitlementHistoryValue {
	t.Helper()
	var out []model.EntitlementHistoryValue
	if err := s.EntitlementHistoryOf(principal, func(v *model.EntitlementHistoryValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("EntitlementHistoryOf: %v", err)
	}
	return out
}

// TestEndingAHoldWritesTheRowAndRemovesTheLive.
//
// The two together, because either alone is a lie: a delete without the row
// loses the hold, and a row without the delete reports somebody as both holding
// and having returned the same thing.
func TestEndingAHoldWritesTheRowAndRemovesTheLive(t *testing.T) {
	s := openStore(t)

	tx := s.NewTransaction()
	if err := tx.PutEntitlement(heldRow("usr_ada", "approve-payment", 1000)); err != nil {
		t.Fatalf("PutEntitlement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	tx = s.NewTransaction()
	closed, err := tx.EndEntitlement("usr_ada", "approve-payment", 9000,
		model.EndReturned, "usr_chef")
	if err != nil {
		t.Fatalf("EndEntitlement: %v", err)
	}
	if !closed {
		t.Fatal("closing a hold that was there reported nothing to close")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	got := endedOf(t, s, "usr_ada")
	want := model.EntitlementHistoryValue{
		Principal: "usr_ada", ItemID: "approve-payment", VariantID: "gold",
		OrderID: "ord_7", Since: 1000, Until: 1500, Origin: model.OriginOrdered,
		EndedAt: 9000, EndedReason: model.EndReturned, EndedBy: "usr_chef",
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("history = %+v, want %+v — the row carries what the order will not "+
			"be around to supply", got, want)
	}
	if _, ok, err := s.Entitlement("usr_ada", "approve-payment"); err != nil || ok {
		t.Errorf("the hold is still live after being closed (ok=%v err=%v)", ok, err)
	}
}

// TestClosingWhatNobodyHeldWritesNothing.
//
// Revoking something nobody was recorded as holding is a documented no-op, and
// the history has to follow it: a row for a hold that never existed is not a
// smaller error than no history at all.
func TestClosingWhatNobodyHeldWritesNothing(t *testing.T) {
	s := openStore(t)

	tx := s.NewTransaction()
	closed, err := tx.EndEntitlement("usr_ada", "nie-gehabt", 9000, model.EndReturned, "usr_chef")
	if err != nil {
		t.Fatalf("EndEntitlement: %v", err)
	}
	if closed {
		t.Error("closing a hold nobody had reported that it closed one")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	if got := endedOf(t, s, "usr_ada"); len(got) != 0 {
		t.Errorf("history = %+v, want nothing", got)
	}
}

// TestOnePrincipalsHistoryStopsAtTheirOwn.
//
// The 0x00 separator's whole job. Without it a principal whose id is a prefix of
// another's would scan the other's rows into their own answer — which in this
// family means one person's access history reported as somebody else's.
func TestOnePrincipalsHistoryStopsAtTheirOwn(t *testing.T) {
	s := openStore(t)

	tx := s.NewTransaction()
	for _, who := range []string{"usr_ada", "usr_adam"} {
		if err := tx.PutEntitlement(heldRow(who, "vpn", 1000)); err != nil {
			t.Fatalf("PutEntitlement %s: %v", who, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	tx = s.NewTransaction()
	for _, who := range []string{"usr_ada", "usr_adam"} {
		if _, err := tx.EndEntitlement(who, "vpn", 9000, model.EndReturned, "usr_chef"); err != nil {
			t.Fatalf("EndEntitlement %s: %v", who, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	for _, who := range []string{"usr_ada", "usr_adam"} {
		got := endedOf(t, s, who)
		if len(got) != 1 || got[0].Principal != who {
			t.Errorf("%s's history = %+v, want only their own", who, got)
		}
	}
}

// TestTheMostRecentlyEndedComesFirst.
//
// The family only grows and the question asked of it is almost always about the
// recent past, so the scan runs descending. A caller that had to sort would be
// sorting a list the key already ordered.
func TestTheMostRecentlyEndedComesFirst(t *testing.T) {
	s := openStore(t)

	for _, at := range []int64{2000, 4000, 3000} {
		tx := s.NewTransaction()
		if err := tx.PutEntitlement(heldRow("usr_ada", "vpn", at-100)); err != nil {
			t.Fatalf("PutEntitlement: %v", err)
		}
		if _, err := tx.EndEntitlement("usr_ada", "vpn", at, model.EndReturned, "usr_chef"); err != nil {
			t.Fatalf("EndEntitlement: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		_ = tx.Close()
	}

	got := endedOf(t, s, "usr_ada")
	if len(got) != 3 {
		t.Fatalf("%d row(s), want three periods — the same product held and given back "+
			"three times is three rows, which is the difference between this family and "+
			"the inventory: %+v", len(got), got)
	}
	if got[0].EndedAt != 4000 || got[1].EndedAt != 3000 || got[2].EndedAt != 2000 {
		t.Errorf("order = %d, %d, %d; want the most recently ended first",
			got[0].EndedAt, got[1].EndedAt, got[2].EndedAt)
	}
}
