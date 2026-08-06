package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestSetVariablesOnLiveInstance covers the operator override path (ADR-0095): a
// running instance waiting at its service task has one seeded variable overwritten
// and one new variable created, both landing in the instance root scope. It
// exercises SetVariables and handleVariablesModify's create-vs-update choice
// directly in the engine, without the HTTP layer.
func TestSetVariablesOnLiveInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t) // waits at a service task, so the instance stays live

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "100"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	pi := model.NewKey(1, 1)
	// 0 scope means the instance root; overwrite amount and add reviewer.
	p.SetVariables(pi, 0,
		model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "200"},
		model.VariableValue{Name: "reviewer", Kind: model.VarString, Text: "patrick"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}

	if got := readVar(t, h.store, pi, "amount"); got == nil || got.Kind != model.VarNumber || got.Text != "200" {
		t.Fatalf("amount = %+v, want number 200 (overwritten)", got)
	}
	if got := readVar(t, h.store, pi, "reviewer"); got == nil || got.Kind != model.VarString || got.Text != "patrick" {
		t.Fatalf("reviewer = %+v, want string patrick (created)", got)
	}
	// The instance is still parked on its service task — setting variables does not
	// advance the token.
	if piN, ei := counts(t, h.store); piN != 1 || ei != 1 {
		t.Fatalf("after set: process=%d element=%d, want 1 and 1 (still waiting)", piN, ei)
	}
	// Both changes are recorded in the variable timeline as the audit trail: the
	// seeded amount=100, then the override amount=200, then reviewer=patrick.
	if got, want := variableSnapshots(t, h.store, pi), []string{"amount=100", "amount=200", "reviewer=patrick"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("variable timeline = %v, want %v", got, want)
	}
}

// TestSetVariablesNoOpOnFinishedInstance proves the guard: an instance that has
// already finished cannot have variables set on it (there is nothing to correct),
// so the write is a safe no-op and adds no variable to the lingering root scope.
func TestSetVariablesNoOpOnFinishedInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	pi := model.NewKey(1, 1)
	if piN, _ := counts(t, h.store); piN != 0 {
		t.Fatalf("instance still active = %d, want 0 (finished)", piN)
	}

	p.SetVariables(pi, 0, model.VariableValue{Name: "late", Kind: model.VarString, Text: "nope"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if got := readVar(t, h.store, pi, "late"); got != nil {
		t.Fatalf("late = %+v, want nil (no write onto a finished instance)", got)
	}
}

// TestSetVariablesRejectsForeignScope proves the scope guard: a scopeKey that is
// not a live element instance of the instance is rejected, so a stray key cannot
// orphan a variable under a scope no FEEL lookup reaches.
func TestSetVariablesRejectsForeignScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	pi := model.NewKey(1, 1)
	bogus := model.NewKey(1, 9999) // no element instance has this key

	p.SetVariables(pi, bogus, model.VariableValue{Name: "x", Kind: model.VarString, Text: "y"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if got := readVar(t, h.store, bogus, "x"); got != nil {
		t.Fatalf("x under bogus scope = %+v, want nil (foreign scope rejected)", got)
	}
	if got := readVar(t, h.store, pi, "x"); got != nil {
		t.Fatalf("x under root scope = %+v, want nil (never written)", got)
	}
}

// TestSetVariablesRecovers is the recovery property for the operator override
// (invariants I4/I6, ADR-0095): a variable set live is written as an ordinary
// variable event, so replaying the log into a fresh store rebuilds exactly the same
// value — the override is a durable fact read back from the log, never re-applied by
// re-running the command (commands are not replayed).
func TestSetVariablesRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, _ := linearProcess(t)
	clock := &manualClock{}

	// Live run: park at the service task, then override a seeded variable.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "100"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	pi := model.NewKey(1, 1)
	p1.SetVariables(pi, 0, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "200"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (set): %v", err)
	}
	live := readVar(t, h1.store, pi, "amount")
	if live == nil || live.Text != "200" {
		t.Fatalf("live amount = %+v, want 200", live)
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
	if replayed := readVar(t, store2, pi, "amount"); replayed == nil || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed amount = %+v, want %+v", replayed, live)
	}
}
