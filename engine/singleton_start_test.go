package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// singletonResponder is keyedResponder with the singleton-start flag toggleable:
// messageStart("request", key = orderId, singleton) → messageCatch("never") → End.
// keyed==false gives a keyless singleton start (its key always evaluates to "").
func singletonResponder(t testing.TB, key uint64, keyed, singleton bool) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "singleton-responder", 1)
	var keyExpr = mustCompile(t, "orderId")
	if !keyed {
		keyExpr = nil
	}
	ms := b.AddMessageStartEvent("request", keyExpr, singleton)
	park := b.AddMessageCatchEvent("never", nil)
	end := b.AddEndEvent()
	b.Connect(ms, park)
	b.Connect(park, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func newSingletonProc(t *testing.T, h *harness, cp *compiler.CompiledProcess) *engine.Processor {
	t.Helper()
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

func publishRequest(t *testing.T, p *engine.Processor, orderId string) {
	t.Helper()
	p.PublishMessage("request", "", numVar("orderId", orderId))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
}

// TestSingletonMessageStartDedupesByKey proves a singleton message start starts at
// most one live instance per correlation key: a second message for the same key
// starts nothing, while a different key starts another (ADR-0082).
func TestSingletonMessageStartDedupesByKey(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := newSingletonProc(t, h, singletonResponder(t, 7, true, true))

	publishRequest(t, p, "42")
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("after first message (key 42): active=%d, want 1", n)
	}
	publishRequest(t, p, "42") // same key: no duplicate
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("after second message (key 42): active=%d, want 1 (singleton)", n)
	}
	publishRequest(t, p, "99") // different key: a new instance
	if n := activeProcs(t, h.store); n != 2 {
		t.Fatalf("after message (key 99): active=%d, want 2", n)
	}
}

// TestNonSingletonMessageStartUnchanged proves the default (no flag) still starts one
// instance per message for the same key — ADR-0035 behavior is preserved.
func TestNonSingletonMessageStartUnchanged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := newSingletonProc(t, h, singletonResponder(t, 7, true, false))

	publishRequest(t, p, "42")
	publishRequest(t, p, "42")
	if n := activeProcs(t, h.store); n != 2 {
		t.Fatalf("non-singleton, two messages same key: active=%d, want 2", n)
	}
}

// TestSingletonMessageStartEmptyKeyAlwaysStarts proves an empty correlation key is
// never treated as singleton (it identifies no entity), so each message starts.
func TestSingletonMessageStartEmptyKeyAlwaysStarts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := newSingletonProc(t, h, singletonResponder(t, 7, false, true)) // keyless singleton

	publishRequest(t, p, "42")
	publishRequest(t, p, "42")
	if n := activeProcs(t, h.store); n != 2 {
		t.Fatalf("keyless singleton, two messages: active=%d, want 2 (empty key not deduped)", n)
	}
}

// TestSingletonMessageStartReopensOnTerminate proves terminating the live instance
// re-opens its key, so the next message starts a fresh one (the counter returned to
// zero via applyToState's decrement, ADR-0082).
func TestSingletonMessageStartReopensOnTerminate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := newSingletonProc(t, h, singletonResponder(t, 7, true, true))

	publishRequest(t, p, "42")
	pi := singleActiveInstance(t, h.store)

	p.CancelInstance(pi)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("after cancel: active=%d, want 0", n)
	}

	publishRequest(t, p, "42") // key re-opened: starts again
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("after re-publishing key 42: active=%d, want 1 (key re-opened)", n)
	}
}

// TestSingletonMessageStartIntraBatch proves the per-batch guard: two messages for the
// same key delivered in one batch (before either create applies) start one instance,
// not two.
func TestSingletonMessageStartIntraBatch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := newSingletonProc(t, h, singletonResponder(t, 7, true, true))

	// Both publishes are enqueued before RunUntilIdle, so they process in one batch —
	// the durable counter is still zero for both; only the per-batch set stops the dup.
	p.PublishMessage("request", "", numVar("orderId", "42"))
	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("two same-key messages in one batch: active=%d, want 1 (intra-batch guard)", n)
	}
}

// TestSingletonMessageStartRecovery proves the liveness counter rebuilds on replay: an
// instance started before a crash still blocks a duplicate start after recovery, since
// applyToState replays the ActiveStartKey increment (I4/I6).
func TestSingletonMessageStartRecovery(t *testing.T) {
	dir := t.TempDir()
	cp := singletonResponder(t, 7, true, true)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := activeProcs(t, h1.store); n != 1 {
		t.Fatalf("before crash: active=%d, want 1", n)
	}
	h1.close(t)

	// Replay into a fresh store, then publish the same key again: the rebuilt counter
	// must still gate the duplicate.
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
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if n := activeProcs(t, store2); n != 1 {
		t.Fatalf("after replay: active=%d, want 1 (instance recovered)", n)
	}
	p2.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (post-replay): %v", err)
	}
	if n := activeProcs(t, store2); n != 1 {
		t.Fatalf("post-replay duplicate for key 42: active=%d, want 1 (counter rebuilt)", n)
	}
}
