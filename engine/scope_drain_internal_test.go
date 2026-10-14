package engine

import (
	"fmt"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/wal"
)

// workAheadFixture forks into a branch that ends at once and a branch whose token
// passes a task on its way to a service task: the shape whose instance used to report
// itself completed while the service task's job was still to be created (#1013).
func workAheadFixture(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(40, "work-ahead", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	a := b.AddTask()
	work := b.AddServiceTask("work-ahead", 3)
	longEnd := b.AddEndEvent()
	shortEnd := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, a)
	b.Connect(a, work)
	b.Connect(work, longEnd)
	b.Connect(fork, shortEnd)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// completedEarly reports whether the log holds an element activation for an instance
// after that instance's Completed event.
func completedEarly(t *testing.T, l *wal.Log) bool {
	t.Helper()
	done := map[uint64]bool{}
	early := false
	if err := l.Replay(func(raw []byte) error {
		rec, err := model.ReadRecord(raw)
		if err != nil || rec.Header.RecordType != model.RecordEvent {
			return err
		}
		switch v := rec.Value.(type) {
		case *model.ProcessInstanceValue:
			if rec.Header.Intent == model.IntentCompleted {
				done[rec.Header.Key] = true
			}
		case *model.ElementInstanceValue:
			if rec.Header.Intent == model.IntentActivated && done[v.ProcessInstanceKey] {
				early = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return early
}

// TestAScopeDrainResumesAtEveryBatchBoundary cuts the work-ahead run at every batch
// boundary and resumes it, with the state store kept and rebuilt from the log alone.
// Whether a scope may drain now depends on the queue as well as on state, and the
// queue is exactly what a stop loses — unless the continuation restores it
// (ADR-0271). Every resumed run must wait at the service task with its instance still
// active, and no run may write an activation after its instance completed.
func TestAScopeDrainResumesAtEveryBatchBoundary(t *testing.T) {
	cp := workAheadFixture(t)
	want := runShape{instances: 1, elements: 1, jobs: 1}

	dir := t.TempDir()
	p, l, s := openAt(t, dir, "state")
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	batches := 0
	for len(p.queue) > 0 {
		if err := p.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
		batches++
	}
	if got := shapeOf(t, s); got != want {
		t.Fatalf("uninterrupted run: %s, want %s", got, want)
	}
	if completedEarly(t, l) {
		t.Fatal("uninterrupted run: an activation was written after its instance completed")
	}
	s.Close()
	l.Close()

	for cut := 1; cut <= batches; cut++ {
		for _, keepState := range []bool{true, false} {
			name := fmt.Sprintf("cut=%d/state=kept", cut)
			if !keepState {
				name = fmt.Sprintf("cut=%d/state=rebuilt", cut)
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				p, l, s := openAt(t, dir, "state")
				p.Deploy(cp)
				p.CreateInstance(cp.Key)
				for i := 0; i < cut && len(p.queue) > 0; i++ {
					if err := p.processBatch(); err != nil {
						t.Fatalf("processBatch: %v", err)
					}
				}
				s.Close()
				l.Close()

				stateName := "state"
				if !keepState {
					stateName = "rebuilt-state"
				}
				p, l, s = openAt(t, dir, stateName)
				defer l.Close()
				defer s.Close()
				p.Deploy(cp)
				if err := p.Recover(); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				if err := p.RunUntilIdle(); err != nil {
					t.Fatalf("RunUntilIdle: %v", err)
				}
				if got := shapeOf(t, s); got != want {
					t.Fatalf("resumed after %d of %d batches: %s, want %s", cut, batches, got, want)
				}
				if completedEarly(t, l) {
					t.Fatalf("resumed after %d of %d batches: an activation was written after its instance completed", cut, batches)
				}
			})
		}
	}
}
