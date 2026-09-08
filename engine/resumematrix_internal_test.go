package engine

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// runShape is what a run is judged by: the durable outcome an observer can see.
type runShape struct {
	instances, elements, jobs, incidents int
}

func (s runShape) String() string {
	return fmt.Sprintf("instances=%d elements=%d jobs=%d incidents=%d", s.instances, s.elements, s.jobs, s.incidents)
}

func shapeOf(t *testing.T, store *state.Store) runShape {
	t.Helper()
	var s runShape
	if err := store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error {
		s.instances++
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if err := store.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error {
		s.elements++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if err := store.AllActivatableJobs(func(uint64) error {
		s.jobs++
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if err := store.Incidents(func(uint64, *model.IncidentValue) error {
		s.incidents++
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	return s
}

// resumeFixture is a process that takes several batches to reach a wait state and
// passes through a subprocess, a parallel fork and join, and a call activity, so
// the boundaries it is cut at are not all the same shape.
func resumeFixture(t *testing.T) (parent, child *compiler.CompiledProcess) {
	t.Helper()
	cb := compiler.NewBuilder(21, "resume-child", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddServiceTask("child-work", 3)
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	child, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}

	b := compiler.NewBuilder(20, "resume-parent", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	is := b.AddStartEvent()
	fork := b.AddParallelGateway()
	a := b.AddServiceTask("branch-a", 3)
	bb := b.AddServiceTask("branch-b", 3)
	join := b.AddParallelGateway()
	ie := b.AddEndEvent()
	b.Connect(is, fork)
	b.Connect(fork, a)
	b.Connect(fork, bb)
	b.Connect(a, join)
	b.Connect(bb, join)
	b.Connect(join, ie)
	b.PopScope()
	call := b.AddCallActivity("resume-child", compiler.BindingLatest, true, true)
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, call)
	b.Connect(call, end)
	parent, err = b.Build()
	if err != nil {
		t.Fatalf("parent Build: %v", err)
	}
	return parent, child
}

func openAt(t *testing.T, dir, stateName string) (*Processor, *wal.Log, *state.Store) {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	s, err := state.Open(filepath.Join(dir, stateName))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return New(1, l, s, &wbClock{}), l, s
}

// TestInterruptAtEveryBatchBoundaryResumes is F01's acceptance criterion: cut the
// run at every batch boundary there is, restart, and finish. The outcome must be
// the one an uninterrupted run reaches — every time, and whether the state store
// survived the stop or has to be rebuilt from the log alone.
//
// This is the property "state == replay(WAL)" does not give. That equation is
// about the facts; it says nothing about the work still owed, and an instance can
// satisfy it perfectly while standing still forever because the commands that
// would have carried it forward existed only in memory.
func TestInterruptAtEveryBatchBoundaryResumes(t *testing.T) {
	parent, child := resumeFixture(t)

	// The uninterrupted run, for reference.
	dir := t.TempDir()
	p, l, s := openAt(t, dir, "state")
	p.Deploy(parent)
	p.Deploy(child)
	p.CreateInstance(parent.Key)
	batches := 0
	for len(p.queue) > 0 {
		if err := p.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
		batches++
	}
	want := shapeOf(t, s)
	s.Close()
	l.Close()
	if batches < 4 {
		t.Fatalf("fixture settled in %d batches; too few boundaries to prove anything", batches)
	}
	t.Logf("uninterrupted: %d batches, %s", batches, want)

	for cut := 1; cut <= batches; cut++ {
		for _, keepState := range []bool{true, false} {
			name := fmt.Sprintf("cut=%d/state=kept", cut)
			if !keepState {
				name = fmt.Sprintf("cut=%d/state=rebuilt", cut)
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				p, l, s := openAt(t, dir, "state")
				p.Deploy(parent)
				p.Deploy(child)
				p.CreateInstance(parent.Key)
				for i := 0; i < cut && len(p.queue) > 0; i++ {
					if err := p.processBatch(); err != nil {
						t.Fatalf("processBatch: %v", err)
					}
				}
				// Everything in memory is lost here: the queue, the followups, the lot.
				s.Close()
				l.Close()

				stateName := "state"
				if !keepState {
					stateName = "rebuilt-state"
				}
				p, l, s = openAt(t, dir, stateName)
				defer l.Close()
				defer s.Close()
				p.Deploy(parent)
				p.Deploy(child)
				if err := p.Recover(); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				if err := p.RunUntilIdle(); err != nil {
					t.Fatalf("RunUntilIdle: %v", err)
				}
				if got := shapeOf(t, s); got != want {
					t.Fatalf("resumed after %d of %d batches: %s; the uninterrupted run reaches %s",
						cut, batches, got, want)
				}
			})
		}
	}
}
