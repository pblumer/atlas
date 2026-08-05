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

// dualSignalCatcher: Start → parallel split → two signal catches on the SAME name,
// each into a script task then its own end. One instance of it parks with TWO
// subscriptions, so a single broadcast must fire both — 1:n within one instance.
func dualSignalCatcher(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "dual-catcher", 1)
	start := b.AddStartEvent()
	split := b.AddParallelGateway()
	catchA := b.AddSignalCatchEvent("cancelled")
	catchB := b.AddSignalCatchEvent("cancelled")
	doneA := b.AddScriptTask(mustCompile(t, `"gotA"`), "a")
	doneB := b.AddScriptTask(mustCompile(t, `"gotB"`), "b")
	endA := b.AddEndEvent()
	endB := b.AddEndEvent()
	b.Connect(start, split)
	b.Connect(split, catchA)
	b.Connect(split, catchB)
	b.Connect(catchA, doneA)
	b.Connect(catchB, doneB)
	b.Connect(doneA, endA)
	b.Connect(doneB, endB)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// signalCatcher: Start → signal catch "cancelled" → script → End. A single waiting
// subscription, used for the cross-instance case.
func signalCatcher(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "catcher", 1)
	start := b.AddStartEvent()
	catch := b.AddSignalCatchEvent("cancelled")
	done := b.AddScriptTask(mustCompile(t, `"caught"`), "done")
	end := b.AddEndEvent()
	b.Connect(start, catch)
	b.Connect(catch, done)
	b.Connect(done, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// signalThrower: Start → signal throw "cancelled" → End. The throw broadcasts the
// signal, carrying the throwing instance's variables as payload.
func signalThrower(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "thrower", 1)
	start := b.AddStartEvent()
	throw := b.AddSignalThrowEvent("cancelled")
	end := b.AddEndEvent()
	b.Connect(start, throw)
	b.Connect(throw, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// activeInstanceOf returns the key of an active process instance of the given
// definition. Element and process instances share one key generator, so an
// instance's key is not predictable from creation order; this looks it up while
// the instance is still live (variables survive completion, so keys captured here
// stay valid for later reads).
func activeInstanceOf(t *testing.T, s *state.Store, defKey uint64) uint64 {
	t.Helper()
	var found uint64
	err := s.ActiveProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == defKey {
			found = key
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if found == 0 {
		t.Fatalf("no active instance of definition %d", defKey)
	}
	return found
}

// TestSignalBroadcastFiresAllCatches is the broadcast rendezvous: a signal throw
// fires every waiting catch of the same name at once — two in ONE instance (via a
// parallel split) and one in a SECOND instance — proving 1:n delivery both within
// and across instances. The throw's variables land in every catcher's scope.
func TestSignalBroadcastFiresAllCatches(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	dual := dualSignalCatcher(t, 7)
	single := signalCatcher(t, 8)
	thrower := signalThrower(t, 9)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(dual)
	p.Deploy(single)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// One dual-catcher instance parks with two subscriptions; one single-catcher
	// instance parks with one. Three catches waiting across two instances.
	p.CreateInstance(dual.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (dual): %v", err)
	}
	dualKey := activeInstanceOf(t, h.store, dual.Key)
	p.CreateInstance(single.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (single): %v", err)
	}
	singleKey := activeInstanceOf(t, h.store, single.Key)
	if pi, ei := counts(t, h.store); pi != 2 || ei != 3 {
		t.Fatalf("before throw: process=%d element=%d, want 2 and 3 (two instances, three parked catches)", pi, ei)
	}

	// The throw broadcasts "cancelled" carrying reason="storm". Every catch fires,
	// both instances finish, and the payload lands in each catcher's scope.
	p.CreateInstance(thrower.Key, model.VariableValue{Name: "reason", Kind: model.VarString, Text: "storm"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after broadcast: process=%d element=%d, want 0 and 0 (all catches fired, thrower completed)", pi, ei)
	}
	if got := readVar(t, h.store, dualKey, "a"); got == nil || got.Text != "gotA" {
		t.Fatalf("dual catch A = %+v, want \"gotA\"", got)
	}
	if got := readVar(t, h.store, dualKey, "b"); got == nil || got.Text != "gotB" {
		t.Fatalf("dual catch B = %+v, want \"gotB\" (both same-instance catches fired on one broadcast)", got)
	}
	if got := readVar(t, h.store, singleKey, "done"); got == nil || got.Text != "caught" {
		t.Fatalf("single catch = %+v, want \"caught\" (cross-instance catch fired)", got)
	}
	// The broadcast payload reached every instance's scope.
	if got := readVar(t, h.store, dualKey, "reason"); got == nil || got.Text != "storm" {
		t.Fatalf("dual payload reason = %+v, want \"storm\"", got)
	}
	if got := readVar(t, h.store, singleKey, "reason"); got == nil || got.Text != "storm" {
		t.Fatalf("single payload reason = %+v, want \"storm\"", got)
	}
}

// TestSignalThrowNoListener proves a broadcast with no waiting catch is a legal
// no-op: the throwing instance runs straight through and completes, nothing hangs
// (a signal is fire-and-forget, no buffering — ADR-0088).
func TestSignalThrowNoListener(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	thrower := signalThrower(t, 9)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after throw with no listener: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestSignalSubscriptionRecovers proves an open signal subscription survives a
// crash: after replaying the log into a fresh store the catcher is still waiting,
// and a signal thrown post-recovery fires the restored subscription and completes
// the instance (invariant I4).
func TestSignalSubscriptionRecovers(t *testing.T) {
	dir := t.TempDir()
	single := signalCatcher(t, 8)
	thrower := signalThrower(t, 9)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(single)
	p1.Deploy(thrower)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(single.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the subscription must rebuild from the log.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(single)
	p2.Deploy(thrower)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	// Subscription restored: the catcher is still parked at the catch event.
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (subscription rebuilt)", pi, ei)
	}
	// A throw now fires the recovered subscription and the instance finishes.
	p2.CreateInstance(thrower.Key)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after throw: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, store2, model.NewKey(1, 1), "done"); got == nil || got.Text != "caught" {
		t.Fatalf("done = %+v, want \"caught\"", got)
	}
}
