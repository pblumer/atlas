package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// feedOf reads the whole feed of partition 1 in position order.
func feedOf(t *testing.T, store *state.Store) []state.FeedEntry {
	t.Helper()
	var out []state.FeedEntry
	if err := store.FeedAfter(1, 0, func(e state.FeedEntry) error {
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatalf("FeedAfter: %v", err)
	}
	return out
}

func kindsOf(entries []state.FeedEntry) []state.FeedKind {
	out := make([]state.FeedKind, len(entries))
	for i, e := range entries {
		out[i] = e.Kind
	}
	return out
}

// TestTheFeedIsFoldedFromTheFactsAndRebuiltFromTheLog: every grant, revocation and
// action outcome lands on the feed in the order the log holds them, the revocation
// carrying the hold it ended — the order it came from included, which its own event
// does not name. A prune drops the rows through its position and remembers the cut,
// and a brand-new store replayed from the same log holds the identical feed and the
// identical cut.
func TestTheFeedIsFoldedFromTheFactsAndRebuiltFromTheLog(t *testing.T) {
	dir := t.TempDir()
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	provision := model.ActionOutcomeValue{OrderID: "ord_1", Position: "vpn", CommandID: "order:ord_1:vpn:provision:1",
		Source: "atlas:order", Action: "provision", Effect: "provision", Outcome: "completed",
		EventType: "vpn.provision.completed", Principal: "usr_ada", ItemID: "vpn", At: 1000}
	p1.GrantEntitlementWithOutcome(model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1",
		Since: 1000, Origin: model.OriginOrdered}, provision)
	ret := provision
	ret.CommandID, ret.Action, ret.Effect, ret.EventType, ret.At = "order:ord_1:vpn:deprovision:1", "deprovision", "deprovision", "vpn.deprovision.completed", 2000
	p1.RevokeEntitlementWithOutcome("usr_ada", "vpn", 2000, model.EndReturned, "usr_ada", ret)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	reset := resetOutcome("completed")
	report(t, p1, reset)

	got := feedOf(t, h1.store)
	want := []state.FeedKind{state.FeedGranted, state.FeedOutcome, state.FeedRevoked, state.FeedOutcome, state.FeedOutcome}
	if k := kindsOf(got); len(k) != len(want) {
		t.Fatalf("feed kinds = %v, want %v", k, want)
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Fatalf("feed kinds = %v, want %v", kindsOf(got), want)
		}
		if i > 0 && got[i].Position <= got[i-1].Position {
			t.Fatalf("row %d at position %d does not follow row %d at %d", i, got[i].Position, i-1, got[i-1].Position)
		}
	}
	if g := got[0].Granted; g == nil || g.Principal != "usr_ada" || g.OrderID != "ord_1" {
		t.Fatalf("the grant row = %+v", g)
	}
	if r := got[2].Revoked; r == nil || r.OrderID != "ord_1" || r.EndedReason != model.EndReturned || r.EndedAt != 2000 {
		t.Fatalf("the revocation row = %+v, want the hold of ord_1 ended as returned", r)
	}
	if o := got[1].Outcome; o == nil || o.CommandID != provision.CommandID {
		t.Fatalf("the provision's outcome row = %+v", o)
	}

	// Prune through the revocation: the two rows after it remain, and the cut is kept.
	cut := got[2].Position
	p1.PruneFeed(cut)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	left := feedOf(t, h1.store)
	if len(left) != 2 || left[0].Position != got[3].Position {
		t.Fatalf("after the prune the feed holds %v", kindsOf(left))
	}
	if through, err := h1.store.FeedPrunedThrough(1); err != nil || through != cut {
		t.Fatalf("pruned through = %d (%v), want %d", through, err, cut)
	}
	// A cut below the one made changes nothing.
	p1.PruneFeed(cut - 1)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if through, _ := h1.store.FeedPrunedThrough(1); through != cut {
		t.Fatalf("an older cut moved the mark to %d", through)
	}
	h1.close(t)

	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("fresh state.Open: %v", err)
	}
	h2 := &harness{dir: dir, log: log, store: store}
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, &manualClock{})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	again := feedOf(t, h2.store)
	if len(again) != len(left) {
		t.Fatalf("replayed feed holds %v, want %v", kindsOf(again), kindsOf(left))
	}
	for i := range left {
		if again[i].Position != left[i].Position || again[i].Kind != left[i].Kind || again[i].At != left[i].At {
			t.Fatalf("replayed row %d = %+v, want %+v", i, again[i], left[i])
		}
	}
	if through, _ := h2.store.FeedPrunedThrough(1); through != cut {
		t.Fatalf("replayed cut = %d, want %d", through, cut)
	}
}
