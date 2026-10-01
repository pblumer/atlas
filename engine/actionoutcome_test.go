package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

func resetOutcome(outcome string) model.ActionOutcomeValue {
	return model.ActionOutcomeValue{
		OrderID: "ord_1", Position: "mailbox", CommandID: "cmd-1", Source: "atlas:shop",
		Action: "password-reset", Effect: "service", Outcome: outcome,
		EventType: "mailbox.password.reset." + outcome, Principal: "usr_ada",
		ItemID: "mailbox", InstanceKey: 77, At: 1000,
	}
}

func outcomeIn(t *testing.T, store *state.Store, order, position, command string) (*model.ActionOutcomeValue, bool) {
	t.Helper()
	v, ok, err := store.ActionOutcome(order, position, command)
	if err != nil {
		t.Fatalf("ActionOutcome: %v", err)
	}
	return v, ok
}

func report(t *testing.T, p *engine.Processor, v model.ActionOutcomeValue) engine.OutcomeResult {
	t.Helper()
	var res engine.OutcomeResult
	p.ReportActionOutcome(v, &res)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return res
}

// TestAnActionEndsOnce: the first report is the fact; the same report again is
// answered from it and writes nothing; a different ending of the same command is
// refused and leaves the first standing; a report naming no command is refused.
func TestAnActionEndsOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if res := report(t, p, resetOutcome("completed")); res.Answer != engine.OutcomeRecorded {
		t.Fatalf("first report = %+v, want recorded", res)
	}
	again := resetOutcome("completed")
	again.At = 2000
	if res := report(t, p, again); res.Answer != engine.OutcomeReplayed || res.Recorded.At != 1000 {
		t.Fatalf("same report again = %+v, want replayed with the first moment", res)
	}
	if res := report(t, p, resetOutcome("failed")); res.Answer != engine.OutcomeConflict || res.Recorded.Outcome != "completed" {
		t.Fatalf("a different ending = %+v, want a conflict naming the first", res)
	}
	if v, ok := outcomeIn(t, h.store, "ord_1", "mailbox", "cmd-1"); !ok || v.Outcome != "completed" || v.At != 1000 {
		t.Fatalf("stored = %+v (%v), want the first report alone", v, ok)
	}
	blank := resetOutcome("completed")
	blank.CommandID = ""
	if res := report(t, p, blank); res.Answer != engine.OutcomeInvalid {
		t.Fatalf("a report naming no command = %+v, want invalid", res)
	}
}

// TestAnActionOutcomeOutlivesItsRecovery: the outcome rebuilds from the log on an
// empty state store, field for field.
func TestAnActionOutcomeOutlivesItsRecovery(t *testing.T) {
	dir := t.TempDir()
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	want := resetOutcome("completed")
	want.Result = `{"ticket":"INC-1"}`
	report(t, p1, want)
	h1.close(t)

	// The same log, a brand-new state store: everything is rebuilt from the log.
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
	if got, ok := outcomeIn(t, h2.store, "ord_1", "mailbox", "cmd-1"); !ok || *got != want {
		t.Fatalf("after replay into an empty store = %+v (%v), want %+v", got, ok, want)
	}
}

// TestAGrantCarriesTheOutcomeOfItsProvision: a grant that ends a provision writes
// the grant and the provision's outcome together; a revocation that ends a return
// does the same; and a grant refused for naming nobody writes neither.
func TestAGrantCarriesTheOutcomeOfItsProvision(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	provision := model.ActionOutcomeValue{OrderID: "ord_1", Position: "vpn", CommandID: "order:ord_1:vpn:provision:1",
		Source: "atlas:order", Action: "provision", Effect: "provision", Outcome: "completed",
		EventType: "vpn.provision.completed", Principal: "usr_ada", ItemID: "vpn", At: 1000}
	p.GrantEntitlementWithOutcome(model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1",
		Since: 1000, Origin: model.OriginOrdered}, provision)
	ret := provision
	ret.CommandID, ret.Action, ret.Effect, ret.EventType, ret.At = "order:ord_1:vpn:deprovision:1", "deprovision", "deprovision", "vpn.deprovision.completed", 2000
	p.RevokeEntitlementWithOutcome("usr_ada", "vpn", 2000, model.EndReturned, "usr_ada", ret)
	nobody := provision
	nobody.CommandID = "order:ord_2:vpn:provision:1"
	nobody.OrderID = "ord_2"
	p.GrantEntitlementWithOutcome(model.EntitlementValue{ItemID: "vpn", Since: 3000}, nobody)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if _, ok := outcomeIn(t, h.store, "ord_1", "vpn", provision.CommandID); !ok {
		t.Fatal("the provision's outcome was not written beside its grant")
	}
	if _, ok := outcomeIn(t, h.store, "ord_1", "vpn", ret.CommandID); !ok {
		t.Fatal("the return's outcome was not written beside its revocation")
	}
	if len(heldBy(t, h.store, "usr_ada")) != 0 {
		t.Fatal("the revocation did not end the hold")
	}
	if _, ok := outcomeIn(t, h.store, "ord_2", "vpn", nobody.CommandID); ok {
		t.Fatal("a grant refused for naming nobody still wrote its outcome")
	}
}
