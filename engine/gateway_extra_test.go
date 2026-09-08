package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// unconditionalGatewayProcess builds Start → XOR gateway → (unconditional flow)
// → ScriptTask writing "path" → End. The gateway's single outgoing flow carries
// no condition and is not the default, so it is taken whenever the gateway is
// reached (selectExclusiveFlow's nil-condition branch).
func unconditionalGatewayProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "uncond", 1)
	start := b.AddStartEvent()
	gw := b.AddExclusiveGateway()
	taken := b.AddScriptTask(mustCompile(t, `"taken"`), "path")
	end := b.AddEndEvent()
	b.Connect(start, gw)
	b.Connect(gw, taken) // no condition, not default → unconditional
	b.Connect(taken, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// deadEndGatewayProcess builds Start → XOR gateway → (cond "amount > 100") → high
// with NO default flow. When the condition is false no outgoing flow can be taken,
// which is a modeling error: the gateway parks holding its token and raises an
// incident naming itself (ADR-0273).
func deadEndGatewayProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "deadend", 1)
	start := b.AddStartEvent()
	gw := b.AddExclusiveGateway()
	high := b.AddScriptTask(mustCompile(t, `"high"`), "path")
	endHigh := b.AddEndEvent()
	b.Connect(start, gw)
	fHigh := b.Connect(gw, high)
	b.SetFlowCondition(fHigh, mustCompile(t, "amount > 100"))
	b.Connect(high, endHigh)
	// deliberately no default flow
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// replayInto reopens the log and replays it into a fresh, empty store, returning
// that store (registered for cleanup). It is the recovery half of a live/replay
// assertion: state rebuilt from the log alone must match the live run.
func replayInto(t *testing.T, dir string, cp *compiler.CompiledProcess) *state.Store {
	t.Helper()
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open (replay): %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open (replay): %v", err)
	}
	t.Cleanup(func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	})
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	return store2
}

// TestExclusiveGatewayTakesUnconditionalFlow drives an instance through a gateway
// whose outgoing flow has no condition: it is taken, the script task after it
// runs, and the instance completes. A replay of the log rebuilds the identical
// completed state (invariant I6: the taken branch is re-applied, not re-decided).
func TestExclusiveGatewayTakesUnconditionalFlow(t *testing.T) {
	dir := t.TempDir()
	cp := unconditionalGatewayProcess(t)

	h := openHarness(t, dir)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	scope := model.NewKey(1, 1)
	if got := readVar(t, h.store, scope, "path"); got == nil || got.Text != "taken" {
		t.Fatalf("path = %+v, want \"taken\"", got)
	}
	h.close(t)

	// Replay: the unconditional branch is reproduced from the log alone.
	store2 := replayInto(t, dir, cp)
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after replay: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, store2, scope, "path"); got == nil || got.Text != "taken" {
		t.Fatalf("replayed path = %+v, want \"taken\"", got)
	}
}

// TestExclusiveGatewayNoMatchNoDefault drives an instance into a gateway whose
// only condition is false and which has no default flow.
//
// The gateway does not complete: it parks, holding its token, with one incident
// naming it. That is the audit's F08 contract — the token is where an operator can
// see and resume it, rather than consumed by a gateway that then found nowhere to
// send it. A replay rebuilds the same parked element and the same incident from the
// log alone, because the park is an event and not a decision re-made on recovery.
//
// This test used to assert the opposite (element instances = 0, no incident) and
// was pinning the defect. It is rewritten rather than removed: the situation it
// drives is exactly the one worth covering.
func TestExclusiveGatewayNoMatchNoDefault(t *testing.T) {
	dir := t.TempDir()
	cp := deadEndGatewayProcess(t)

	h := openHarness(t, dir)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// amount 50 fails "amount > 100"; with no default, nothing is taken.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "50"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The gateway kept its token: the instance is active with the parked gateway on
	// it, one incident says why, and no branch variable was set.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after run: process=%d element=%d, want 1 and 1 (the parked gateway)", pi, ei)
	}
	live := incidents(t, h.store)
	if len(live) != 1 {
		t.Fatalf("incidents = %d, want exactly 1", len(live))
	}
	scope := model.NewKey(1, 1)
	if got := readVar(t, h.store, scope, "path"); got != nil {
		t.Fatalf("path = %+v, want nil (no branch taken)", got)
	}
	h.close(t)

	// Replay reproduces the identical parked state, incident included, from the log.
	store2 := replayInto(t, dir, cp)
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1", pi, ei)
	}
	if replayed := incidents(t, store2); len(replayed) != len(live) {
		t.Fatalf("replayed incidents = %d, want %d", len(replayed), len(live))
	}
	if got := readVar(t, store2, scope, "path"); got != nil {
		t.Fatalf("replayed path = %+v, want nil", got)
	}
}
