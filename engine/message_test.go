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

// numVar builds a number variable with the given canonical decimal text.
func numVar(name, text string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarNumber, Text: text}
}

// catcherProcess: Start → messageCatch("order", key = orderId) → script("caught"→done) → End.
func catcherProcess(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "catcher", 1)
	start := b.AddStartEvent()
	catch := b.AddMessageCatchEvent("order", mustCompile(t, "orderId"))
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

// throwerProcess: Start → messageThrow("order", key = orderId) → End.
func throwerProcess(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "thrower", 1)
	start := b.AddStartEvent()
	throw := b.AddMessageThrowEvent("order", mustCompile(t, "orderId"))
	end := b.AddEndEvent()
	b.Connect(start, throw)
	b.Connect(throw, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestMessageThrowCatchCorrelation is the end-to-end rendezvous: one instance
// waits at a message catch event; a second instance throws the message with a
// matching correlation key, which wakes the first and both run to completion.
func TestMessageThrowCatchCorrelation(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	thrower := throwerProcess(t, 8)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(catcher)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Catcher subscribes and parks at the catch event.
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (catcher): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after catcher start: process=%d element=%d, want 1 and 1 (waiting at catch)", pi, ei)
	}
	catcherKey := model.NewKey(1, 1)

	// A non-matching key must not correlate: the catcher stays put.
	throwerMiss := throwerProcess(t, 9)
	p.Deploy(throwerMiss)
	p.CreateInstance(throwerMiss.Key, numVar("orderId", "999"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (miss): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after non-matching throw: active=%d, want 1 (catcher still waiting)", pi)
	}

	// Matching key: the throw correlates the catcher, both instances finish.
	p.CreateInstance(thrower.Key, numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (thrower): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after correlation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, h.store, catcherKey, "done"); got == nil || got.Text != "caught" {
		t.Fatalf("catcher done = %+v, want \"caught\" (catch event fired)", got)
	}
}

// TestMessagePublishDeliversPayload publishes a message through the processor's
// PublishMessage entry point (the path the HTTP API uses) and checks the payload
// variables land in the correlated instance's scope.
func TestMessagePublishDeliversPayload(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(catcher)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (catcher): %v", err)
	}
	catcherKey := model.NewKey(1, 1)

	// Publishing with a wrong key is an accepted no-op (no buffering).
	p.PublishMessage("order", "0")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (miss publish): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after non-matching publish: active=%d, want 1", pi)
	}

	// Matching publish with a payload variable: the catcher finishes and the
	// payload is written into its scope.
	p.PublishMessage("order", "42", model.VariableValue{Name: "paid", Kind: model.VarBool, Bool: true})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after publish: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, h.store, catcherKey, "paid"); got == nil || got.Kind != model.VarBool || !got.Bool {
		t.Fatalf("payload paid = %+v, want boolean true", got)
	}
	if got := readVar(t, h.store, catcherKey, "done"); got == nil || got.Text != "caught" {
		t.Fatalf("catcher done = %+v, want \"caught\"", got)
	}
}

// TestMessageSubscriptionRecovers proves an open subscription survives a crash:
// after replaying the log into a fresh store the catcher is still waiting, and a
// message published post-recovery correlates the restored subscription and
// completes the instance (invariant I4).
func TestMessageSubscriptionRecovers(t *testing.T) {
	dir := t.TempDir()
	catcher := catcherProcess(t, 7)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(catcher)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(catcher.Key, numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Replay into a fresh, empty store.
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
	p2.Deploy(catcher)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	// Subscription restored: the catcher is still parked at the catch event.
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1", pi, ei)
	}
	// A publish now correlates the recovered subscription.
	p2.PublishMessage("order", "42")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after publish: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, store2, model.NewKey(1, 1), "done"); got == nil || got.Text != "caught" {
		t.Fatalf("done = %+v, want \"caught\"", got)
	}
}

// TestMessageCatchWithoutCorrelationKey covers a message subscription with no
// correlation key (a nil key expression): it correlates to a publish carrying an
// empty key, matching purely by message name.
func TestMessageCatchWithoutCorrelationKey(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "keyless", 1)
	start := b.AddStartEvent()
	catch := b.AddMessageCatchEvent("ping", nil) // nil correlation-key expression
	end := b.AddEndEvent()
	b.Connect(start, catch)
	b.Connect(catch, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after start: active=%d, want 1 (waiting on keyless subscription)", pi)
	}

	p.PublishMessage("ping", "") // empty key matches the keyless subscription
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after publish: active=%d, want 0 (correlated by name)", pi)
	}
}

// activeProcs returns the live process-instance count.
func activeProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}
