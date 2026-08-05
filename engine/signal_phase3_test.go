package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// signalGuarded builds Start → host service task (job "guarded") with a signal
// boundary "cancelled" (interrupting or not) routed to an "escalated" end; the
// host's normal path leads to a separate "done" end. A broadcast of "cancelled"
// from any instance fires the boundary — the boundary/end-signal Phase 3 (ADR-0088).
func signalGuarded(t testing.TB, key uint64, interrupting bool) (cp *compiler.CompiledProcess, jobType, doneEnd, escEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "guarded", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("guarded", 3)
	onCancel := b.AddBoundarySignalEvent(host, interrupting, "cancelled")
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(onCancel, escalated)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(host).Detail).JobType, done, escalated
}

// signalEnder builds Start → signal END event "cancelled". The end broadcasts the
// signal to every waiting catch, then ends the instance (no outgoing flow) — the
// send-and-stop counterpart of a signal throw (ADR-0088).
func signalEnder(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "ender", 1)
	start := b.AddStartEvent()
	end := b.AddSignalEndEvent("cancelled")
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// signalStartProc builds a process a broadcast instantiates: signal START
// "cancelled" → script (writes started="yes") → end (ADR-0088).
func signalStartProc(t testing.TB, key uint64) (cp *compiler.CompiledProcess, endEvent int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "onSignal", 1)
	start := b.AddSignalStartEvent("cancelled")
	done := b.AddScriptTask(mustCompile(t, `"yes"`), "started")
	end := b.AddEndEvent()
	b.Connect(start, done)
	b.Connect(done, end)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, end
}

// TestSignalBoundaryInterrupts fires an interrupting signal boundary on a waiting
// service task: a separate instance broadcasts "cancelled", the boundary fires
// cross-instance, the host is terminated, its job canceled, and the escalation
// flow is taken (ADR-0088).
func TestSignalBoundaryInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	guarded, jobType, doneEnd, escEnd := signalGuarded(t, 20, true)
	thrower := signalThrower(t, 21)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(guarded)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(guarded.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (guarded): %v", err)
	}
	// Parked: host job + armed signal boundary — two live element instances.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (host + armed boundary)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("host job missing while parked")
	}

	// A separate instance broadcasts "cancelled": the boundary fires cross-instance.
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an interrupting signal boundary (should be canceled)")
	}
	if v := elementVisits(t, h.store, guarded.Key); v[escEnd] != 1 || v[doneEnd] != 0 {
		t.Errorf("visits escalated=%d done=%d, want 1 and 0", v[escEnd], v[doneEnd])
	}
}

// TestSignalBoundaryNonInterrupting fires a non-interrupting signal boundary: the
// escalation flow runs while the host keeps waiting; completing the host job then
// finishes the instance down the normal path (ADR-0088).
func TestSignalBoundaryNonInterrupting(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	guarded, jobType, doneEnd, escEnd := signalGuarded(t, 22, false)
	thrower := signalThrower(t, 23)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(guarded)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(guarded.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (guarded): %v", err)
	}
	hostJob := singleActivatableJob(t, h.store, jobType)

	// Broadcast "cancelled": the escalation branch runs, the host keeps waiting.
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after non-interrupting fire: procs=%d, want 1 (host still waiting)", pi)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("host job vanished on a non-interrupting signal boundary")
	}
	if v := elementVisits(t, h.store, guarded.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}

	// Completing the host job finishes the instance down the normal path.
	p.CompleteJob(hostJob)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after host completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, guarded.Key); v[doneEnd] != 1 {
		t.Errorf("normal end visits = %d, want 1", v[doneEnd])
	}
}

// TestSignalEndEventBroadcasts proves a signal end event broadcasts the signal to
// every waiting catch and then ends its own instance: a waiting catch fires
// cross-instance and both instances finish (ADR-0088).
func TestSignalEndEventBroadcasts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := signalCatcher(t, 24)
	ender := signalEnder(t, 25)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(catcher)
	p.Deploy(ender)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(catcher.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (catcher): %v", err)
	}
	catcherKey := activeInstanceOf(t, h.store, catcher.Key)

	// The signal end event broadcasts "cancelled" then ends its own instance.
	p.CreateInstance(ender.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (end): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after signal end: process=%d element=%d, want 0 and 0 (catch fired, ender ended)", pi, ei)
	}
	if got := readVar(t, h.store, catcherKey, "done"); got == nil || got.Text != "caught" {
		t.Fatalf("catch = %+v, want \"caught\" (signal end fired the waiting catch)", got)
	}
}

// TestSignalStartInstantiatesAndFiresBoundary is the composite Phase-3 broadcast:
// one throw both instantiates a deployed signal-start process AND fires a waiting
// signal boundary in a separate running instance (ADR-0088).
func TestSignalStartInstantiatesAndFiresBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	onSignal, startEnd := signalStartProc(t, 26)
	guarded, jobType, _, escEnd := signalGuarded(t, 27, true)
	thrower := signalThrower(t, 28)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(onSignal)
	p.Deploy(guarded)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// A guarded instance parks with its host job and armed signal boundary.
	p.CreateInstance(guarded.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (guarded): %v", err)
	}

	// One broadcast: starts a fresh onSignal instance and fires the boundary.
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after broadcast: process=%d element=%d, want 0 and 0", pi, ei)
	}
	// The signal-start process ran to its end (a fresh instance was instantiated).
	if v := elementVisits(t, h.store, onSignal.Key); v[startEnd] != 1 {
		t.Errorf("onSignal end visits = %d, want 1 (broadcast instantiated it)", v[startEnd])
	}
	// The boundary fired in the guarded instance.
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived the interrupting signal boundary")
	}
	if v := elementVisits(t, h.store, guarded.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}
}

// TestSignalStartUndeployStopsInstantiation proves undeploying a signal-start
// definition drops it from the broadcast index: a later broadcast no longer
// instantiates it (ADR-0088), the signal-start counterpart of the message case.
func TestSignalStartUndeployStopsInstantiation(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	onSignal, _ := signalStartProc(t, 31)
	thrower := signalThrower(t, 32)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(onSignal)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Undeploy the signal-start definition before any broadcast.
	p.Undeploy(onSignal.Key)

	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	// The thrower completed; no onSignal instance was created (index dropped).
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after throw: process=%d element=%d, want 0 and 0 (no instantiation)", pi, ei)
	}
	if v := elementVisits(t, h.store, onSignal.Key); len(v) != 0 {
		t.Errorf("onSignal visits = %v, want none (definition undeployed)", v)
	}
}

// TestSignalBoundaryRecovers proves an armed signal boundary's subscription
// survives a crash: after replaying the log into a fresh store the boundary is
// still armed, and a signal broadcast post-recovery fires it (invariant I4).
func TestSignalBoundaryRecovers(t *testing.T) {
	dir := t.TempDir()
	guarded, jobType, _, escEnd := signalGuarded(t, 29, true)
	thrower := signalThrower(t, 30)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(guarded)
	p1.Deploy(thrower)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(guarded.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the boundary subscription must rebuild.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(guarded)
	p2.Deploy(thrower)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	// Host + armed boundary rebuilt from the log.
	if pi, ei := counts(t, store2); pi != 1 || ei != 2 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 2 (boundary subscription rebuilt)", pi, ei)
	}
	// A broadcast now fires the recovered boundary and the instance escalates.
	p2.CreateInstance(thrower.Key)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after throw: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, store2, jobType) {
		t.Error("host job survived the recovered interrupting signal boundary")
	}
	if v := elementVisits(t, store2, guarded.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}
}
