package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The inventory is engine state, and this is what that buys: a fact that rebuilds
// from the log after everything that produced it is gone.

// heldBy reads everything one principal holds, in item order.
func heldBy(t *testing.T, store *state.Store, principal string) []model.EntitlementValue {
	t.Helper()
	var out []model.EntitlementValue
	if err := store.EntitlementsOf(principal, func(v *model.EntitlementValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("EntitlementsOf: %v", err)
	}
	return out
}

func TestAnEntitlementIsWrittenAndRead(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", VariantID: "gross",
		OrderID: "ord_1", Since: 1000, Origin: model.OriginOrdered,
	})
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_bruno", ItemID: "vpn", Since: 1000, Origin: model.OriginLegacy,
	})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := heldBy(t, h.store, "usr_alice")
	if len(got) != 1 {
		t.Fatalf("alice holds %d, want 1", len(got))
	}
	if got[0].ItemID != "vpn" || got[0].VariantID != "gross" || got[0].OrderID != "ord_1" {
		t.Errorf("= %+v", got[0])
	}
	if got[0].Since != 1000 || got[0].Origin != model.OriginOrdered {
		t.Errorf("= %+v, want the moment and the origin the command carried", got[0])
	}

	// One principal's inventory is their own. The separator between the principal
	// and the item is what keeps an id that is a prefix of another's out of the
	// other's answer.
	if n := len(heldBy(t, h.store, "usr_bruno")); n != 1 {
		t.Errorf("bruno holds %d, want 1", n)
	}
	if n := len(heldBy(t, h.store, "usr_al")); n != 0 {
		t.Errorf("a principal whose id is a prefix of alice's reads %d of her entitlements", n)
	}

	// Where the knowledge came from is kept, because it is the difference between
	// evidence and an assumption.
	if b := heldBy(t, h.store, "usr_bruno"); b[0].Origin != model.OriginLegacy {
		t.Errorf("origin = %v, want legacy", b[0].Origin)
	}
}

func TestRevokingRemovesOnlyWhatItNames(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, item := range []string{"vpn", "laptop"} {
		p.GrantEntitlement(model.EntitlementValue{
			Principal: "usr_alice", ItemID: item, Since: 1000,
		})
	}
	p.RevokeEntitlement("usr_alice", "vpn")
	// Revoking what nobody holds is the state the caller asked for, not an error —
	// a reconciliation that removes a privilege twice must not fail.
	p.RevokeEntitlement("usr_alice", "nie-gehabt")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := heldBy(t, h.store, "usr_alice")
	if len(got) != 1 || got[0].ItemID != "laptop" {
		t.Fatalf("= %+v, want only the laptop", got)
	}
	if _, ok, err := h.store.Entitlement("usr_alice", "vpn"); err != nil || ok {
		t.Errorf("the revoked entitlement is still readable (ok=%v err=%v)", ok, err)
	}
}

// TestAnEntitlementOutlivesItsRecovery is the claim the whole placement rests on.
//
// An entitlement is engine state because it outlives the instance that produced
// it, by years — and the instance is eligible for retention deletion long before
// the access ends. A fact that could not be rebuilt from the log after that is not
// a record, so this restarts the engine on the same directory and reads it back.
func TestAnEntitlementOutlivesItsRecovery(t *testing.T) {
	dir := t.TempDir()

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", OrderID: "ord_1",
		Since: 1000, Origin: model.OriginOrdered,
	})
	p1.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "laptop", Since: 1000,
	})
	p1.RevokeEntitlement("usr_alice", "laptop")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, &manualClock{})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	got := heldBy(t, h2.store, "usr_alice")
	if len(got) != 1 {
		t.Fatalf("after recovery alice holds %d, want 1: %+v", len(got), got)
	}
	if got[0].ItemID != "vpn" || got[0].OrderID != "ord_1" || got[0].Since != 1000 {
		t.Errorf("= %+v, want the grant rebuilt exactly", got[0])
	}
	// The revocation is replayed too: a fold that only ever added would report
	// everybody as holding everything they ever held.
	if _, ok, _ := h2.store.Entitlement("usr_alice", "laptop"); ok {
		t.Error("a revoked entitlement came back on recovery")
	}
}

// TestAnEntitlementNamingNobodyIsRefused: both halves of "who holds what" are
// required, and a record missing one is a fact about nothing that a reader would
// nonetheless count.
func TestAnEntitlementNamingNobodyIsRefused(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{ItemID: "vpn", Since: 1000})
	p.GrantEntitlement(model.EntitlementValue{Principal: "usr_alice", Since: 1000})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n, err := h.store.EntitlementCount(); err != nil || n != 0 {
		t.Errorf("the inventory holds %d (err %v); neither command named both halves", n, err)
	}
}
