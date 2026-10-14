package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// triggered deploys the three-operation lifecycle process and returns a processor
// on it. singleton keys the provision start on positionId, the way a product's
// lifecycle process keeps a deprovisioning from racing a provisioning of the same
// position (ADR-0425).
func triggeredLifecycle(t *testing.T, h *harness, singleton bool) *engine.Processor {
	t.Helper()
	b := compiler.NewBuilder(7, "lifecycle", 1)
	var key *expr.Compiled
	if singleton {
		key = mustCompile(t, "positionId")
	}
	for _, op := range []string{"provision", "change", "deprovision"} {
		s := b.AddMessageStartEvent("laptop."+op, key, singleton && op == "provision")
		st := b.AddScriptTask(mustCompile(t, "true"), "ran_"+op)
		park := b.AddMessageCatchEvent("never."+op, nil)
		b.Connect(s, st)
		b.Connect(st, park)
		b.Connect(park, b.AddEndEvent())
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

func trigger(t *testing.T, p *engine.Processor, name, source, id string, vars ...model.VariableValue) engine.TriggerResult {
	t.Helper()
	var res engine.TriggerResult
	p.TriggerStart(7, name, source, id, &res, vars...)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return res
}

func ranOps(t *testing.T, h *harness, key uint64) []string {
	t.Helper()
	var out []string
	for _, op := range []string{"provision", "change", "deprovision"} {
		if readVar(t, h.store, key, "ran_"+op) != nil {
			out = append(out, op)
		}
	}
	return out
}

// TestTriggerStartsOnlyTheNamedStart: a directed trigger starts its definition at
// the message start it names and at nothing else, and answers with the instance.
func TestTriggerStartsOnlyTheNamedStart(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := triggeredLifecycle(t, h, false)

	res := trigger(t, p, "laptop.deprovision", "", "")
	if res.Outcome != engine.TriggerCreated || res.InstanceKey == 0 {
		t.Fatalf("result = %+v, want created with a key", res)
	}
	if got := ranOps(t, h, res.InstanceKey); len(got) != 1 || got[0] != "deprovision" {
		t.Fatalf("branches that ran = %v, want only deprovision", got)
	}
}

// TestTriggerReplaysARepeatedDelivery: the same sender delivering the same trigger
// id twice gets the first instance back and starts nothing; another sender with the
// same id is a different trigger.
func TestTriggerReplaysARepeatedDelivery(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := triggeredLifecycle(t, h, false)

	first := trigger(t, p, "laptop.provision", "hr", "leaver-1")
	again := trigger(t, p, "laptop.provision", "hr", "leaver-1")
	if again.Outcome != engine.TriggerReplayed || again.InstanceKey != first.InstanceKey {
		t.Fatalf("repeat = %+v, want replayed with %d", again, first.InstanceKey)
	}
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("active instances = %d, want 1", n)
	}
	other := trigger(t, p, "laptop.provision", "ticketing", "leaver-1")
	if other.Outcome != engine.TriggerCreated || other.InstanceKey == first.InstanceKey {
		t.Fatalf("other sender = %+v, want a new instance", other)
	}
}

// TestTriggerTwiceInOneBatchStartsOnce: two deliveries of one trigger processed in
// the same batch see each other's receipt.
func TestTriggerTwiceInOneBatchStartsOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := triggeredLifecycle(t, h, false)

	var a, b engine.TriggerResult
	p.TriggerStart(7, "laptop.provision", "hr", "x", &a)
	p.TriggerStart(7, "laptop.provision", "hr", "x", &b)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if a.Outcome != engine.TriggerCreated || b.Outcome != engine.TriggerReplayed || a.InstanceKey != b.InstanceKey {
		t.Fatalf("a=%+v b=%+v, want one created and one replay of it", a, b)
	}
}

// TestTriggerRefusalsAreAnswers: every way a broadcast publish starts nothing
// silently is an answer here.
func TestTriggerRefusalsAreAnswers(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := triggeredLifecycle(t, h, true)

	if res := trigger(t, p, "no.such.start", "", ""); res.Outcome != engine.TriggerNoSuchStart {
		t.Errorf("unknown start = %+v, want no-such-start", res)
	}
	var res engine.TriggerResult
	p.TriggerStart(99, "laptop.provision", "", "", &res)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if res.Outcome != engine.TriggerNoSuchStart {
		t.Errorf("unknown definition = %+v, want no-such-start", res)
	}

	pos := model.VariableValue{Name: "positionId", Kind: model.VarString, Text: "laptop#1"}
	if res := trigger(t, p, "laptop.provision", "", "", pos); res.Outcome != engine.TriggerCreated {
		t.Fatalf("first provision = %+v, want created", res)
	}
	if res := trigger(t, p, "laptop.provision", "", "", pos); res.Outcome != engine.TriggerSingletonTaken {
		t.Errorf("second provision of the same position = %+v, want singleton-taken", res)
	}

	p.SetProcessActive(7, false)
	if res := trigger(t, p, "laptop.deprovision", "", ""); res.Outcome != engine.TriggerInactive {
		t.Errorf("deactivated = %+v, want inactive", res)
	}
}

// TestTriggerReceiptsSurviveARebuildAndArePruned: the receipts are state folded from
// events, so a store rebuilt from the log alone still replays a repeated delivery;
// and a prune drops what is older than its cutoff, on replay as live.
func TestTriggerReceiptsSurviveARebuildAndArePruned(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	p := triggeredLifecycle(t, h, false)
	first := trigger(t, p, "laptop.provision", "hr", "leaver-1")
	h.close(t)

	// Throw the state away: only the log remains.
	if err := os.RemoveAll(filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	h = openHarness(t, dir)
	defer h.close(t)
	p = triggeredLifecycle(t, h, false)
	if res := trigger(t, p, "laptop.provision", "hr", "leaver-1"); res.Outcome != engine.TriggerReplayed || res.InstanceKey != first.InstanceKey {
		t.Fatalf("after a rebuild = %+v, want replayed with %d", res, first.InstanceKey)
	}

	if n, err := h.store.TriggerReceiptCount(); err != nil || n != 1 {
		t.Fatalf("receipts = %d (%v), want 1", n, err)
	}
	p.PruneTriggerReceipts(1 << 62)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n, err := h.store.TriggerReceiptCount(); err != nil || n != 0 {
		t.Fatalf("receipts after prune = %d (%v), want 0", n, err)
	}
	if res := trigger(t, p, "laptop.provision", "hr", "leaver-1"); res.Outcome != engine.TriggerCreated {
		t.Fatalf("after prune = %+v, want a new instance — the receipt is gone", res)
	}
}
