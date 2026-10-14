package engine

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// childViewProcess builds a one-task process: a call activity when call is set,
// otherwise a service task the run parks on.
func childViewProcess(t *testing.T, key uint64, name string, call bool) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, name, 1)
	start := b.AddStartEvent()
	var task int32
	if call {
		task = b.AddCallActivity("child", compiler.BindingLatest, true, true)
	} else {
		task = b.AddServiceTask("child-work", 3)
	}
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build %s: %v", name, err)
	}
	return cp
}

// TestCancelSeesChildCreatedInSameBatch: a call activity's child and the
// cancellation of its caller can land in one batch. The teardown must then see
// the child, which means reading the reverse index through the transaction and
// not the committed store.
//
// The committed-only read was deliberate once — ADR-0238 justified it as a
// determinism measure — but the reasoning does not hold: the events the current
// transaction has already applied are a deterministic part of processing this
// command, exactly as they are for ElementInstancesOfProcess, which a parallel
// join has always had to read that way. What the committed view actually bought
// was a blind spot: cancel a caller in the batch that created its child and the
// child survived its parent, holding a live job with nobody left to answer to.
func TestCancelSeesChildCreatedInSameBatch(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	parent := childViewProcess(t, 7, "parent", true)
	child := childViewProcess(t, 8, "child", false)
	p := New(1, log, store, &wbClock{})
	p.Deploy(parent)
	p.Deploy(child)
	p.CreateInstance(parent.Key)

	// Step to exactly the batch boundary where the child's creation command is
	// queued but not yet processed, so the cancel below shares its batch.
	pending := false
	for step := 0; step < 10 && !pending; step++ {
		if len(p.queue) > 0 && p.queue[0].ValueType == model.VTProcessInstance &&
			p.queue[0].Value.process.ParentElementInstanceKey != 0 {
			pending = true
			break
		}
		if err := p.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
	}
	if !pending {
		t.Fatal("fixture never reached a pending child creation")
	}

	var parentKey uint64
	if err := store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == parent.Key {
			parentKey = k
		}
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if parentKey == 0 {
		t.Fatal("fixture has no live parent instance")
	}

	p.CancelInstance(parentKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	instances, elements, jobs := 0, 0, 0
	if err := store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error {
		instances++
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if err := store.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error {
		elements++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if err := store.AllActivatableJobs(func(uint64) error {
		jobs++
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if instances != 0 || elements != 0 || jobs != 0 {
		t.Fatalf("cancel left an orphan: instances=%d elements=%d jobs=%d; want the whole caller/child execution torn down", instances, elements, jobs)
	}
}

// TestCommandInstanceKey pins which queued commands advanceQueue can attribute to
// a process instance. Only the value types that carry a token are answered: those
// are the ones that would rebuild an execution after its instance is gone. A
// command naming no instance (a deployment-wide or operator command) answers 0
// and is therefore never dropped.
func TestCommandInstanceKey(t *testing.T) {
	const pi = uint64(42)
	for _, tc := range []struct {
		name string
		cmd  Command
		want uint64
	}{
		// A process-instance command names no execution to advance: its key does not
		// mean one thing across intents, and a purge must survive a terminated batch.
		{"process instance names none", Command{ValueType: model.VTProcessInstance, Key: pi}, 0},
		{"element carries its instance", Command{
			ValueType: model.VTElementInstance,
			Value:     inflightValue{element: model.ElementInstanceValue{ProcessInstanceKey: pi}},
		}, pi},
		{"job carries its instance", Command{
			ValueType: model.VTJob,
			Value:     inflightValue{job: model.JobValue{ProcessInstanceKey: pi}},
		}, pi},
		{"timer carries its instance", Command{
			ValueType: model.VTTimer,
			Value:     inflightValue{timer: model.TimerValue{ProcessInstanceKey: pi}},
		}, pi},
		{"a message flow names none", Command{ValueType: model.VTMessageFlow, Key: pi}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandInstanceKey(&tc.cmd); got != tc.want {
				t.Errorf("commandInstanceKey = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAdvanceQueueDropsTerminatedInstancesWork pins the queue-level rule the
// child teardown relies on: once an instance is terminated during a batch, the
// commands it still had queued — whether carried over from an earlier batch or
// scheduled as followups during this one — do not survive into the next batch.
// Everything belonging to a live instance, and everything naming no instance at
// all, is kept.
//
// Commands are never persisted and never replayed (I6), so dropping them changes
// what runs next and nothing about what recovery rebuilds.
func TestAdvanceQueueDropsTerminatedInstancesWork(t *testing.T) {
	const dead, live = uint64(1), uint64(2)
	elemFor := func(pi uint64, id int32) Command {
		return Command{
			ValueType: model.VTElementInstance,
			Intent:    model.IntentActivating,
			Value:     inflightValue{element: model.ElementInstanceValue{ProcessInstanceKey: pi, ElementId: id}},
		}
	}

	p := &Processor{}
	// Two commands already consumed by this batch, then a carried-over tail.
	p.queue = []Command{elemFor(dead, 0), elemFor(live, 0), elemFor(dead, 1), elemFor(live, 1)}
	// Followups scheduled during the batch, including one for an instance the batch
	// terminated and one that names no instance at all.
	p.followups = []Command{
		elemFor(dead, 2),
		{ValueType: model.VTJob, Value: inflightValue{job: model.JobValue{ProcessInstanceKey: dead}}},
		{ValueType: model.VTMessageFlow},
		// A purge of the very instance this batch terminated must survive, or its
		// history is never reclaimed.
		{ValueType: model.VTProcessInstance, Intent: model.IntentPurging, Key: dead},
		elemFor(live, 2),
	}
	p.terminatedThisBatch = map[uint64]struct{}{dead: {}}

	p.advanceQueue(2)

	for _, cmd := range p.queue {
		if commandInstanceKey(&cmd) == dead {
			t.Fatalf("terminated instance kept queued work: %+v", cmd)
		}
	}
	// The live instance's tail command and followup survive, as does the command
	// that names no instance — dropping those would stall unrelated work.
	if len(p.queue) != 4 {
		t.Fatalf("queue = %d commands, want 4 (live tail, unattributed followup, purge, live followup): %+v", len(p.queue), p.queue)
	}
	purgeKept := false
	for _, cmd := range p.queue {
		if cmd.ValueType == model.VTProcessInstance && cmd.Intent == model.IntentPurging {
			purgeKept = true
		}
	}
	if !purgeKept {
		t.Fatal("the terminated instance's purge was dropped; its history would never be reclaimed")
	}

	// With nothing terminated the queue is passed through untouched, so the filter
	// costs nothing on the ordinary path.
	p.followups = []Command{elemFor(live, 3)}
	p.terminatedThisBatch = nil
	before := len(p.queue)
	p.advanceQueue(0)
	if len(p.queue) != before+1 {
		t.Fatalf("queue = %d, want %d with no terminations", len(p.queue), before+1)
	}
}

// TestCancelBeforeChildIsCreated is the other order the same window allows: the
// caller is cancelled in the batch *before* the queued child-creation command
// runs. The teardown cannot cascade into a child that does not exist yet, so the
// creation itself has to notice that the call activity it would report back to is
// gone.
func TestCancelBeforeChildIsCreated(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	parent := childViewProcess(t, 7, "parent", true)
	child := childViewProcess(t, 8, "child", false)
	p := New(1, log, store, &wbClock{})
	p.Deploy(parent)
	p.Deploy(child)
	p.CreateInstance(parent.Key)

	// Stop at the boundary where the child's creation command is queued.
	pending := false
	for step := 0; step < 10 && !pending; step++ {
		if len(p.queue) > 0 && p.queue[0].ValueType == model.VTProcessInstance &&
			p.queue[0].Value.process.ParentElementInstanceKey != 0 {
			pending = true
			break
		}
		if err := p.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
	}
	if !pending {
		t.Fatal("fixture never reached a pending child creation")
	}

	var parentKey uint64
	if err := store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == parent.Key {
			parentKey = k
		}
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}

	// Cancel first: the queued creation is now behind a Terminating for its caller.
	p.CancelInstance(parentKey)
	// Put the cancel ahead of the child creation, so the caller is already gone by
	// the time the creation runs.
	last := len(p.queue) - 1
	p.queue[0], p.queue[last] = p.queue[last], p.queue[0]
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	instances, elements, jobs := 0, 0, 0
	if err := store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error {
		instances++
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if err := store.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error {
		elements++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if err := store.AllActivatableJobs(func(uint64) error {
		jobs++
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if instances != 0 || elements != 0 || jobs != 0 {
		t.Fatalf("a child was started for a caller already cancelled: instances=%d elements=%d jobs=%d; want 0/0/0", instances, elements, jobs)
	}
}
