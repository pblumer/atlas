package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// forkedWritersProcess builds the shape that makes write attribution necessary:
// Start → parallel fork → (service task A) & (service task B) → parallel join → End,
// where both branches run at the same time and each writes its own variable. It
// returns the compiled process, the two task element ids, and their job types.
func forkedWritersProcess(t testing.TB) (cp *compiler.CompiledProcess, taskA, taskB, jobA, jobB int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "forkedwriters", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	taskA = b.AddServiceTask("A", 3)
	taskB = b.AddServiceTask("B", 3)
	join := b.AddParallelGateway()
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, taskA)
	b.Connect(fork, taskB)
	b.Connect(taskA, join)
	b.Connect(taskB, join)
	b.Connect(join, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, taskA, taskB, cp.ServiceTask(cp.Node(taskA).Detail).JobType, cp.ServiceTask(cp.Node(taskB).Detail).JobType
}

// activationKeys maps each element id to the element instance key its activation
// minted, so a test can name the element a variable is attributed to.
func activationKeys(t *testing.T, s *state.Store, piKey uint64) map[int32]uint64 {
	t.Helper()
	out := map[int32]uint64{}
	if err := s.ElementReplayHistory(piKey, func(_ int64, _ uint64, v state.ElementReplayValue) error {
		if v.Action == state.ReplayActivated {
			out[v.ElementID] = v.ElementInstanceKey
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementReplayHistory: %v", err)
	}
	return out
}

// variableProducers folds a scope's retained variable changes into name → producing
// element instance, the attribution the timeline reads back.
func variableProducers(t *testing.T, s *state.Store, scopeKey uint64) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	if err := s.VariableSnapshotHistory(scopeKey, func(_ int64, _ uint64, v *model.VariableValue) error {
		out[v.Name] = v.ProducerKey
		return nil
	}); err != nil {
		t.Fatalf("VariableSnapshotHistory: %v", err)
	}
	return out
}

// TestJobOutputsAreAttributedToTheirTask is the regression test for what the
// Operations replay showed on a fork: two tasks running in parallel, each writing one
// variable, and the diagram's in/out card crediting *both* variables to *both* tasks —
// because the only thing history said was what the variables were before and after
// each element, and on a fork the sibling's write falls inside that window. The engine
// now states who wrote each value (ADR-0219), so the
// question is answered rather than inferred.
func TestJobOutputsAreAttributedToTheirTask(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, taskA, taskB, jobA, jobB := forkedWritersProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// A start variable belongs to no element: nothing has run yet when it is written.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "seed", Kind: model.VarString, Text: "s"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobsA, jobsB := activatableJobs(t, h.store, jobA), activatableJobs(t, h.store, jobB)
	if len(jobsA) != 1 || len(jobsB) != 1 {
		t.Fatalf("jobs = %d and %d, want one per branch", len(jobsA), len(jobsB))
	}
	// Both workers report in the same batch, exactly as two parallel branches do.
	p.CompleteJob(jobsA[0], model.VariableValue{Name: "newTicket", Kind: model.VarString, Text: "PAT-9"})
	p.CompleteJob(jobsB[0], model.VariableValue{Name: "tickets", Kind: model.VarNumber, Text: "4"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after completions): %v", err)
	}

	pi := model.NewKey(1, 1)
	keys := activationKeys(t, h.store, pi)
	got := variableProducers(t, h.store, pi)
	want := map[string]uint64{
		"seed":      0,           // written before any element ran
		"newTicket": keys[taskA], // branch A's worker returned it
		"tickets":   keys[taskB], // branch B's worker returned it
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("producers = %v, want %v", got, want)
	}
	if keys[taskA] == 0 || keys[taskA] == keys[taskB] {
		t.Fatalf("the two branches must be distinct element instances, got %d and %d", keys[taskA], keys[taskB])
	}
}

// TestVariableProducerRecovers is the recovery property for the attribution: it is a
// fact frozen into the variable event, so replaying the log rebuilds it identically
// rather than re-deriving it (invariants I4/I6). Without that, a restart would silently
// lose the answer to "which task wrote this" for every instance already on disk.
func TestVariableProducerRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, taskA, taskB, jobA, jobB := forkedWritersProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	p1.CompleteJob(activatableJobs(t, h1.store, jobA)[0],
		model.VariableValue{Name: "newTicket", Kind: model.VarString, Text: "PAT-9"})
	p1.CompleteJob(activatableJobs(t, h1.store, jobB)[0],
		model.VariableValue{Name: "tickets", Kind: model.VarNumber, Text: "4"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1 (after completions): %v", err)
	}

	pi := model.NewKey(1, 1)
	liveKeys := activationKeys(t, h1.store, pi)
	live := variableProducers(t, h1.store, pi)
	if live["newTicket"] != liveKeys[taskA] || live["tickets"] != liveKeys[taskB] {
		t.Fatalf("live producers = %v, want newTicket from %d and tickets from %d",
			live, liveKeys[taskA], liveKeys[taskB])
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if replayed := variableProducers(t, store2, pi); !reflect.DeepEqual(replayed, live) {
		t.Fatalf("replayed producers = %v, want %v", replayed, live)
	}
}

// TestCallActivityResultIsAttributedToTheCallActivity covers the one attribution that
// crosses instances: a child's variables are promoted into the caller while the *child's*
// end event is the element being completed, so the write would otherwise be credited to
// an element of another instance — that is, to nothing the caller's replay can show. The
// caller's call activity is what produced it, and says so.
func TestCallActivityResultIsAttributedToTheCallActivity(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cb := compiler.NewBuilder(8, "child-attr", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddScriptTask(mustCompile(t, "seed"), "childSaw") // reads the propagated caller var
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}

	b := compiler.NewBuilder(9, "caller-attr", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "42"), "seed")
	call := b.AddCallActivity("child-attr", compiler.BindingLatest, true /*propParent*/, true /*propChild*/)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, call)
	b.Connect(call, end)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	callerRoot := model.NewKey(1, 1)
	keys := activationKeys(t, h.store, callerRoot)
	first := firstVariableProducers(t, h.store, callerRoot)
	last := variableProducers(t, h.store, callerRoot)
	if first["seed"] != keys[setup] {
		t.Errorf("seed first written by %d, want the caller's script task %d", first["seed"], keys[setup])
	}
	if first["childSaw"] != keys[call] || last["childSaw"] != keys[call] {
		t.Errorf("childSaw attributed to %d, want the call activity %d", first["childSaw"], keys[call])
	}
	// Propagating *all* of the child's variables back writes the caller's own seed a
	// second time — the child was handed it and hands it back — so the call activity is
	// the last writer of it too. That is what happened, and the out card says so; the
	// old diff hid it only because the value was unchanged.
	if last["seed"] != keys[call] {
		t.Errorf("seed last written by %d, want the call activity %d propagating it back", last["seed"], keys[call])
	}
}

// firstVariableProducers is variableProducers keeping the *earliest* write of each name
// rather than the latest, so a test can name the element that introduced a value as well
// as the one that last touched it.
func firstVariableProducers(t *testing.T, s *state.Store, scopeKey uint64) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	if err := s.VariableSnapshotHistory(scopeKey, func(_ int64, _ uint64, v *model.VariableValue) error {
		if _, seen := out[v.Name]; !seen {
			out[v.Name] = v.ProducerKey
		}
		return nil
	}); err != nil {
		t.Fatalf("VariableSnapshotHistory: %v", err)
	}
	return out
}

// TestMessagePayloadIsAttributedToTheCatchEvent covers the other attribution the
// command cannot supply: a published message belongs to no element instance — it comes
// in through the API — so its payload is credited to the catch event that received it,
// which is the element an operator sees the values appear on.
func TestMessagePayloadIsAttributedToTheCatchEvent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "payload-attr", 1)
	start := b.AddStartEvent()
	catch := b.AddMessageCatchEvent("order", mustCompile(t, "orderId"))
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
	p.CreateInstance(cp.Key, model.VariableValue{Name: "orderId", Kind: model.VarNumber, Text: "42"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.PublishMessage("order", "42", model.VariableValue{Name: "paid", Kind: model.VarBool, Bool: true})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}

	pi := model.NewKey(1, 1)
	keys := activationKeys(t, h.store, pi)
	got := variableProducers(t, h.store, pi)
	if got["paid"] != keys[catch] {
		t.Errorf("payload attributed to %d, want the catch event %d", got["paid"], keys[catch])
	}
	if got["orderId"] != 0 {
		t.Errorf("the start variable is attributed to %d, want no element", got["orderId"])
	}
}
