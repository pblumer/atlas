package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// Reading the inventory, at the layer that holds it (ADR-0312).
//
// Coverage is measured per package, and these three had none of their own: every
// caller lives in api/, so the store's contract was being held by somebody else's
// tests. That is the wrong way round — the asymmetry these functions exist to
// carry is the store's, and it is the kind of thing a refactor in api/ can stop
// exercising without anybody noticing.
//
// The asymmetry: the key is principal-then-item, so "what does Alice hold" is a
// prefix scan and "who holds VPN access" is not answerable without walking the
// family. Both directions are here, because the day somebody adds a by-item index
// the difference between them is what has to keep holding.

// putHolds writes a set of live entitlements in one transaction.
func putHolds(t *testing.T, s *Store, rows ...*model.EntitlementValue) {
	t.Helper()
	tx := s.NewTransaction()
	defer func() { _ = tx.Close() }()
	for _, r := range rows {
		if err := tx.PutEntitlement(r); err != nil {
			t.Fatalf("PutEntitlement: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// heldBy collects what one principal holds, as item ids in the order the scan
// produced them.
func heldBy(t *testing.T, s *Store, principal string) []string {
	t.Helper()
	var out []string
	if err := s.EntitlementsOf(principal, func(v *model.EntitlementValue) error {
		out = append(out, v.ItemID)
		return nil
	}); err != nil {
		t.Fatalf("EntitlementsOf: %v", err)
	}
	return out
}

// TestOnePersonsInventoryIsTheirsAlone, and in item order.
//
// The order is not decoration: the inventory is read to be shown, and a list that
// came back in a different order on every read would look like it had changed.
func TestOnePersonsInventoryIsTheirsAlone(t *testing.T) {
	s := openStore(t)
	putHolds(t, s,
		heldRow("usr_ada", "vpn", 1000),
		heldRow("usr_ada", "laptop", 1000),
		heldRow("usr_bob", "vpn", 1000),
	)

	if got := heldBy(t, s, "usr_ada"); len(got) != 2 || got[0] != "laptop" || got[1] != "vpn" {
		t.Errorf("ada holds %v, want laptop then vpn — hers, in item order", got)
	}
	if got := heldBy(t, s, "usr_bob"); len(got) != 1 || got[0] != "vpn" {
		t.Errorf("bob holds %v, want only his own", got)
	}
	// Somebody who holds nothing is an empty answer, not an error: an account that
	// has never ordered is the ordinary case on the day it is created.
	if got := heldBy(t, s, "usr_nobody"); len(got) != 0 {
		t.Errorf("an account that holds nothing came back with %v", got)
	}
}

// TestTheWholeInventoryWalksByPrincipalThenItem.
//
// The population-sized query, and the one thing worth asserting about it beyond
// "it returns everything": the order. It is what makes a reconciliation run over
// an estate reproducible, and it falls out of the key rather than being sorted —
// so a change to the key shape shows up here.
func TestTheWholeInventoryWalksByPrincipalThenItem(t *testing.T) {
	s := openStore(t)
	putHolds(t, s,
		heldRow("usr_bob", "vpn", 1000),
		heldRow("usr_ada", "vpn", 1000),
		heldRow("usr_ada", "laptop", 1000),
	)

	var seen [][2]string
	if err := s.Entitlements(func(v *model.EntitlementValue) error {
		seen = append(seen, [2]string{v.Principal, v.ItemID})
		return nil
	}); err != nil {
		t.Fatalf("Entitlements: %v", err)
	}
	want := [][2]string{{"usr_ada", "laptop"}, {"usr_ada", "vpn"}, {"usr_bob", "vpn"}}
	if len(seen) != len(want) {
		t.Fatalf("the walk found %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("position %d is %v, want %v — the walk is not by principal then item", i, seen[i], want[i])
		}
	}
}

// TestRevokingSomethingNobodyHoldsIsNotAnError.
//
// Deleting an absent hold is the state the caller asked for. Refusing it would
// make a reconciliation that removes a privilege twice into a failure — and a
// reconciliation that runs twice is the normal consequence of at-least-once
// delivery, not a fault to report.
func TestRevokingSomethingNobodyHoldsIsNotAnError(t *testing.T) {
	s := openStore(t)
	putHolds(t, s, heldRow("usr_ada", "vpn", 1000), heldRow("usr_ada", "laptop", 1000))

	tx := s.NewTransaction()
	if err := tx.DeleteEntitlement("usr_ada", "vpn"); err != nil {
		t.Fatalf("DeleteEntitlement: %v", err)
	}
	// The same one again, and one that was never there.
	if err := tx.DeleteEntitlement("usr_ada", "vpn"); err != nil {
		t.Errorf("revoking twice failed: %v", err)
	}
	if err := tx.DeleteEntitlement("usr_nobody", "vpn"); err != nil {
		t.Errorf("revoking what nobody held failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	if got := heldBy(t, s, "usr_ada"); len(got) != 1 || got[0] != "laptop" {
		t.Errorf("ada holds %v after one revocation, want only laptop", got)
	}
	// And a delete leaves no history row: ending a hold is EndEntitlement's job,
	// and a plain delete is the reconciliation removing something that should never
	// have been recorded rather than closing a period somebody had.
	if ended := endedOf(t, s, "usr_ada"); len(ended) != 0 {
		t.Errorf("a plain delete wrote %d history rows; it is not a closed hold", len(ended))
	}
}
