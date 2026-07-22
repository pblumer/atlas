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
// with NO default flow. When the condition is false the gateway takes no flow
// (selectExclusiveFlow returns -1), so the branch simply ends without reaching an
// end event — a modeling error that becomes an incident in a later milestone.
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
// only condition is false and which has no default flow: no outgoing flow is
// taken. The gateway completes but nothing follows, leaving the instance active
// with no element instances and no "path" variable — and a replay reproduces that
// same stuck state exactly.
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

	// The gateway consumed its token and took no flow: the instance is still
	// active but has no live element instances, and no branch variable was set.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 1 and 0", pi, ei)
	}
	scope := model.NewKey(1, 1)
	if got := readVar(t, h.store, scope, "path"); got != nil {
		t.Fatalf("path = %+v, want nil (no branch taken)", got)
	}
	h.close(t)

	// Replay reproduces the identical stuck state from the log.
	store2 := replayInto(t, dir, cp)
	if pi, ei := counts(t, store2); pi != 1 || ei != 0 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 0", pi, ei)
	}
	if got := readVar(t, store2, scope, "path"); got != nil {
		t.Fatalf("replayed path = %+v, want nil", got)
	}
}
