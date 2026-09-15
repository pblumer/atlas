package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// What a hold leaves behind (ADR-draft-entitlement-history).
//
// The claim under this whole family is that the remedy must not destroy the
// evidence of the problem. These hold the fold to it at the level where it is
// actually decided — everything above is a rendering.

// endedBy reads every hold one principal has closed, most recently first.
func endedBy(t *testing.T, store *state.Store, principal string) []model.EntitlementHistoryValue {
	t.Helper()
	var out []model.EntitlementHistoryValue
	if err := store.EntitlementHistoryOf(principal, func(v *model.EntitlementHistoryValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("EntitlementHistoryOf: %v", err)
	}
	return out
}

// TestAReturnedRightLeavesEverythingTheOrderWouldHaveSaid.
//
// The order behind a right is deleted by retention long before the right ends —
// that is why the inventory is engine state at all. So the row has to carry what
// the order carried, not a reference to it, or the record is a pointer into
// nothing.
func TestAReturnedRightLeavesEverythingTheOrderWouldHaveSaid(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "approve-payment", VariantID: "gold",
		OrderID: "ord_7", Since: 1000, Origin: model.OriginOrdered, Until: 5000,
	})
	p.RevokeEntitlement("usr_alice", "approve-payment", 9000, model.EndReturned, "usr_chef")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := endedBy(t, h.store, "usr_alice")
	if len(got) != 1 {
		t.Fatalf("%d row(s), want the one ended hold: %+v", len(got), got)
	}
	want := model.EntitlementHistoryValue{
		Principal: "usr_alice", ItemID: "approve-payment", VariantID: "gold",
		OrderID: "ord_7", Since: 1000, Until: 5000, Origin: model.OriginOrdered,
		EndedAt: 9000, EndedReason: model.EndReturned, EndedBy: "usr_chef",
	}
	if got[0] != want {
		t.Errorf("the row lost something the order will not be around to supply:\n got %+v\nwant %+v",
			got[0], want)
	}
	// And the live row is gone: a record saying somebody both holds and has
	// returned the same thing is worse than either alone.
	if _, ok, err := h.store.Entitlement("usr_alice", "approve-payment"); err != nil || ok {
		t.Errorf("the hold is still live after being closed (ok=%v err=%v)", ok, err)
	}
}

// TestACorrectionIsRecordedAsAClaimAndNotAsAccess.
//
// handleRevokeDiscrepancy says of itself that it "does not decide that the
// disappearance was correct". The row it produces must not say otherwise, because
// a record kept for years that asserts somebody had access nobody can show they
// had is the direction of wrongness ADR-0334 calls the corrupting one.
func TestACorrectionIsRecordedAsAClaimAndNotAsAccess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", Since: 1000, Origin: model.OriginLegacy,
	})
	p.RevokeEntitlement("usr_alice", "vpn", 9000, model.EndCorrected, "usr_ops")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := endedBy(t, h.store, "usr_alice")
	if len(got) != 1 || got[0].EndedReason != model.EndCorrected {
		t.Fatalf("= %+v, want one corrected row", got)
	}
	if got[0].EndedReason.Held() {
		t.Error("a correction reads as a period of access, which asserts what the " +
			"reconciliation explicitly declined to decide")
	}
}

// TestRevokingTwiceLeavesOneRow.
//
// The load-bearing consequence of reading the hold in the fold rather than
// freezing a copy into the event. Revoking what nobody holds is a documented
// no-op; a second row would invent a hold that never existed, which is not a
// smaller error than no history at all.
func TestRevokingTwiceLeavesOneRow(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", Since: 1000,
	})
	p.RevokeEntitlement("usr_alice", "vpn", 9000, model.EndReturned, "usr_chef")
	p.RevokeEntitlement("usr_alice", "vpn", 9500, model.EndReturned, "usr_chef")
	p.RevokeEntitlement("usr_alice", "nie-gehabt", 9600, model.EndReturned, "usr_chef")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := endedBy(t, h.store, "usr_alice")
	if len(got) != 1 {
		t.Fatalf("%d row(s), want exactly the one hold that existed: %+v", len(got), got)
	}
	if got[0].EndedAt != 9000 {
		t.Errorf("endedAt = %d, want the moment the hold actually ended", got[0].EndedAt)
	}
}

// TestTheSameProductHeldTwiceIsTwoRows.
//
// The difference between this family and the inventory, and the reason the key
// carries the end time. An entitlement key is (principal, item) because holding
// something twice is one entitlement; a history row is an *event*, and somebody
// who held a product in one job, gave it back, and held it again in another has
// two periods. A key that collapsed them would delete the first.
func TestTheSameProductHeldTwiceIsTwoRows(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, period := range []struct{ since, ended int64 }{{1000, 2000}, {3000, 4000}} {
		p.GrantEntitlement(model.EntitlementValue{
			Principal: "usr_alice", ItemID: "vpn", Since: period.since,
		})
		p.RevokeEntitlement("usr_alice", "vpn", period.ended, model.EndReturned, "usr_chef")
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := endedBy(t, h.store, "usr_alice")
	if len(got) != 2 {
		t.Fatalf("%d row(s), want both periods: %+v", len(got), got)
	}
	// Most recently ended first: the family only grows, and the question asked of
	// it is almost always about the recent past.
	if got[0].EndedAt != 4000 || got[1].EndedAt != 2000 {
		t.Errorf("order = %d, %d; want the most recently ended first",
			got[0].EndedAt, got[1].EndedAt)
	}
	if got[0].Since != 3000 || got[1].Since != 1000 {
		t.Errorf("each row must carry its own start, got %d and %d",
			got[0].Since, got[1].Since)
	}
}

// TestAGrantAndItsRevocationInOneBatchCloseTheRightHold.
//
// The second case a frozen copy gets wrong. The fold reads through an indexed
// batch, so it observes this transaction's own pending writes — a revocation
// folded alongside the grant it ends closes the hold that was actually granted
// rather than one that was there beforehand.
func TestAGrantAndItsRevocationInOneBatchCloseTheRightHold(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", OrderID: "ord_neu", Since: 7000,
	})
	p.RevokeEntitlement("usr_alice", "vpn", 8000, model.EndReturned, "usr_chef")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	got := endedBy(t, h.store, "usr_alice")
	if len(got) != 1 {
		t.Fatalf("%d row(s), want 1: %+v", len(got), got)
	}
	if got[0].Since != 7000 || got[0].OrderID != "ord_neu" {
		t.Errorf("= since %d order %q, want the hold this batch granted — the fold read "+
			"a value from before its own writes", got[0].Since, got[0].OrderID)
	}
}

// TestAnEndedHoldOutlivesItsRecovery.
//
// The claim that makes this engine state rather than a sidecar. A record that
// could not be rebuilt from the log is not a record — and this family is the one
// built specifically to be read years after everything that produced it is gone.
func TestAnEndedHoldOutlivesItsRecovery(t *testing.T) {
	dir := t.TempDir()

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.GrantEntitlement(model.EntitlementValue{
		Principal: "usr_alice", ItemID: "vpn", OrderID: "ord_1", Since: 1000,
		Origin: model.OriginOrdered, Until: 4000,
	})
	p1.RevokeEntitlement("usr_alice", "vpn", 9000, model.EndCorrected, "usr_ops")
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

	got := endedBy(t, h2.store, "usr_alice")
	want := model.EntitlementHistoryValue{
		Principal: "usr_alice", ItemID: "vpn", OrderID: "ord_1", Since: 1000,
		Until: 4000, Origin: model.OriginOrdered, EndedAt: 9000,
		EndedReason: model.EndCorrected, EndedBy: "usr_ops",
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("after replay = %+v, want %+v — rebuilt from the log alone", got, want)
	}
}
