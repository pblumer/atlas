package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestInterruptingBoundaryRecordsHostTerminated builds the model that surfaced the
// ghost token — a user task parked on a job, with an interrupting timer boundary
// whose escape route rejoins at a gateway — fires the timer, and checks the durable
// replay history calls the two endings by their right names: the boundary completed
// (its token moves on to the successor it activated), the host was terminated (its
// token dies with it, nothing downstream ever activates from it). A replay that
// cannot tell the two apart parks a token on the interrupted task forever (ADR-0136).
func TestInterruptingBoundaryRecordsHostTerminated(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	const deadline = int64(45 * 60 * 1_000_000_000) // PT45M, as in the reported model
	b := compiler.NewBuilder(900, "quittieren", 1)
	start := b.AddStartEvent()
	host := b.AddUserTask("Quittieren", "", "", "", 0, 0, 3)
	boundary := b.AddBoundaryTimerEvent(host, true, deadline)
	join := b.AddExclusiveGateway()
	end := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, join)
	b.Connect(boundary, join)
	b.Connect(join, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("before the deadline: active=%d, want 1 (parked on the task)", pi)
	}

	clk.t += deadline + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after the deadline: active=%d, want 0 (interrupted and completed)", pi)
	}

	var piKey uint64
	if err := h.store.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		piKey = k
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	type ending struct {
		action byte
		seen   bool
	}
	endings := map[int32]*ending{host: {}, boundary: {}}
	armedSource := int32(0)
	if err := h.store.ElementReplayHistory(piKey, func(_ int64, _ uint64, v state.ElementReplayValue) error {
		if v.Action == state.ReplayActivated {
			if v.ElementID == boundary {
				armedSource = v.SourceFlowID
			}
			return nil
		}
		if e, ok := endings[v.ElementID]; ok {
			e.action, e.seen = v.Action, true
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementReplayHistory: %v", err)
	}
	if e := endings[host]; !e.seen || e.action != state.ReplayTerminated {
		t.Errorf("host ending = %+v, want a recorded ReplayTerminated (%d)", e, state.ReplayTerminated)
	}
	if e := endings[boundary]; !e.seen || e.action != state.ReplayCompleted {
		t.Errorf("boundary ending = %+v, want a recorded ReplayCompleted (%d)", e, state.ReplayCompleted)
	}
	// The boundary is attached to its host, not entered over a sequence flow.
	if armedSource != -1 {
		t.Errorf("armed boundary recorded source flow %d, want -1 (none)", armedSource)
	}
}
