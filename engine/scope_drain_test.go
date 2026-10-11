package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// A scope is drained when no token is left in it — not when no element is active in
// it. The two differ for the length of one batch: an element that completes and takes
// a flow writes its Completed now and its successor's activation as a followup, so the
// token is in neither place the active-child counter can see. A scope that ended a
// branch inside that window used to complete with the token still on its way, and the
// successor was then activated inside an instance that had already reported itself
// done (#1013; first seen as a compensation throw in ADR-0284, but the shape is any
// element that hands its token on while another branch ends).

// lateActivations reads the log in order and returns the element activations written
// for an instance after that instance's Completed event: work done inside an instance
// that had already told everyone it was finished.
func lateActivations(t *testing.T, h *harness) []model.ElementInstanceValue {
	t.Helper()
	done := map[uint64]bool{}
	var late []model.ElementInstanceValue
	if err := h.log.Replay(func(raw []byte) error {
		rec, err := model.ReadRecord(raw)
		if err != nil {
			return err
		}
		if rec.Header.RecordType != model.RecordEvent {
			return nil
		}
		switch v := rec.Value.(type) {
		case *model.ProcessInstanceValue:
			if rec.Header.Intent == model.IntentCompleted {
				done[rec.Header.Key] = true
			}
		case *model.ElementInstanceValue:
			if rec.Header.Intent == model.IntentActivated && done[v.ProcessInstanceKey] {
				late = append(late, *v)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return late
}

// startAndSettle deploys cp on a fresh harness, starts one instance and drives it until
// idle. The caller closes the harness.
func startAndSettle(t *testing.T, cp *compiler.CompiledProcess) (*harness, *engine.Processor) {
	t.Helper()
	h := openHarness(t, t.TempDir())
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return h, p
}

// TestAScopeIsNotDrainedWhileATokenIsBetweenElements forks into a branch that ends at
// once and a branch whose token passes through one element on its way to its end. Every
// element kind that hands its token on over a sequence flow in the same breath is one
// row; each used to let the short branch complete the instance with the long branch's
// token still in transit.
func TestAScopeIsNotDrainedWhileATokenIsBetweenElements(t *testing.T) {
	for i, tc := range []struct {
		name string
		// middle adds the element(s) the long branch passes through and returns the
		// first and the last of them.
		middle func(b *compiler.Builder) (first, last int32)
	}{
		{"task", func(b *compiler.Builder) (int32, int32) { n := b.AddTask(); return n, n }},
		{"none throw", func(b *compiler.Builder) (int32, int32) { n := b.AddNoneThrowEvent(); return n, n }},
		{"signal throw", func(b *compiler.Builder) (int32, int32) { n := b.AddSignalThrowEvent("go"); return n, n }},
		{"escalation throw", func(b *compiler.Builder) (int32, int32) { n := b.AddEscalationThrowEvent("late"); return n, n }},
		{"link throw and catch", func(b *compiler.Builder) (int32, int32) {
			th, ca := b.AddLinkThrowEvent(), b.AddLinkCatchEvent()
			b.Connect(th, ca)
			return th, ca
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := compiler.NewBuilder(uint64(910+i), "in-transit", 1)
			start := b.AddStartEvent()
			fork := b.AddParallelGateway()
			first, last := tc.middle(b)
			longEnd := b.AddEndEvent()
			shortEnd := b.AddEndEvent()
			b.Connect(start, fork)
			b.Connect(fork, first)
			b.Connect(last, longEnd)
			b.Connect(fork, shortEnd)
			cp, err := b.Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			h, _ := startAndSettle(t, cp)
			defer h.close(t)
			if late := lateActivations(t, h); len(late) != 0 {
				t.Errorf("%d activation(s) after the instance completed, first on element %d", len(late), late[0].ElementId)
			}
			if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
				t.Errorf("settled with process=%d element=%d, want 0 and 0", pi, ei)
			}
		})
	}
}

// TestACompensationThrowWaitsForItsOutgoingToken is #1013 as it was reported: the
// compensation handler finishes at once, and the scope used to drain on the handler's
// completion while the throw's own token was still on its way to the end event.
func TestACompensationThrowWaitsForItsOutgoingToken(t *testing.T) {
	b := compiler.NewBuilder(920, "comp-sync", 1)
	start := b.AddStartEvent()
	charge := b.AddTask()
	chargeComp := b.AddBoundaryCompensationEvent(charge)
	refund := b.AddTask()
	b.SetCompensationHandler(chargeComp, refund)
	throw := b.AddCompensationThrowEvent()
	b.SetCompensationActivityRef(throw, charge)
	end := b.AddEndEvent()
	b.Connect(start, charge)
	b.Connect(charge, throw)
	b.Connect(throw, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h, _ := startAndSettle(t, cp)
	defer h.close(t)
	if late := lateActivations(t, h); len(late) != 0 {
		t.Errorf("%d activation(s) after the instance completed, first on element %d", len(late), late[0].ElementId)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Errorf("settled with process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestAnInstanceWithWorkAheadIsNotReportedComplete is what the early drain cost a user:
// the token in transit was headed for a service task, so the instance reported itself
// completed while a job for it was created afterwards — a job no instance would ever
// collect. The instance must stay active at the task, and complete when it does.
func TestAnInstanceWithWorkAheadIsNotReportedComplete(t *testing.T) {
	b := compiler.NewBuilder(930, "work-ahead", 1)
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
	h, p := startAndSettle(t, cp)
	defer h.close(t)

	jobs := activatableJobs(t, h.store, cp.ServiceTask(cp.Node(work).Detail).JobType)
	if pi, _ := counts(t, h.store); pi != 1 || len(jobs) != 1 {
		t.Fatalf("waiting at the task: process=%d jobs=%d, want 1 and 1", pi, len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the task: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if late := lateActivations(t, h); len(late) != 0 {
		t.Errorf("%d activation(s) after the instance completed", len(late))
	}
}
