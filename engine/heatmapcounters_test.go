package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// The Operations heatmap (ADR-0496) needs two facts the engine did
// not keep: which sequence flow a token arrived over, counted per definition, and when a
// task was taken up and handed back, kept per instance. Both are folded in applyToState,
// so these tests check them live and after the log is replayed into an empty store
// (state == replay(log), I4/I6).

// TestFlowCountersFollowTheTokens: an arrow is counted when a token arrives over it, not
// when the element it leaves is entered — a token parked on a task has taken the arrow
// into it and not yet the one out of it.
func TestFlowCountersFollowTheTokens(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t) // start -(0)-> work -(1)-> end

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got, want := engineFlowTotals(t, h.store, cp.Key), map[int32]int64{0: 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("flows with both instances parked = %v, want %v", got, want)
	}

	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got, want := engineFlowTotals(t, h.store, cp.Key), map[int32]int64{0: 2, 1: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("flows after one instance finished = %v, want %v", got, want)
	}
}

// TestFlowCountersCountEveryArrivalAtAJoin: a parallel join is reached over each of its
// incoming arrows, one token per branch, and every one of those arrows was taken — the
// join consuming them into one continuation must not leave all but one uncounted.
func TestFlowCountersCountEveryArrivalAtAJoin(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(defKey, "fork-join", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	left := b.AddTask()
	right := b.AddTask()
	join := b.AddParallelGateway()
	end := b.AddEndEvent()
	b.Connect(start, fork) // 0
	b.Connect(fork, left)  // 1
	b.Connect(fork, right) // 2
	b.Connect(left, join)  // 3
	b.Connect(right, join) // 4
	b.Connect(join, end)   // 5
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
	want := map[int32]int64{0: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1}
	if got := engineFlowTotals(t, h.store, cp.Key); !reflect.DeepEqual(got, want) {
		t.Errorf("flows through a fork and join = %v, want every arrow once %v", got, want)
	}
}

// TestFlowCountersCountOnlyTheRaceWinner: an event-based gateway arms every branch at
// once — that is how Atlas runs a deferred choice — but the diagram means one arrow
// taken, the one whose event fired. Counting the arming would draw both branches
// equally hot whatever happened, the misreading ADR-0249 removed from the element
// counts. So an armed branch counts when its race is decided in its favour, and an
// undecided race counts for neither.
func TestFlowCountersCountOnlyTheRaceWinner(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	// start -(0)-> wait -(1)-> reply -(3)-> replyEnd
	//                  -(2)-> timeout -(4)-> timeoutEnd
	cp, _, _ := eventGatewayProcess(t, 92)

	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got, want := engineFlowTotals(t, h.store, cp.Key), map[int32]int64{0: 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("flows while the race is open = %v, want %v (neither branch taken yet)", got, want)
	}

	p.PublishMessage("reply", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got, want := engineFlowTotals(t, h.store, cp.Key), map[int32]int64{0: 1, 1: 1, 3: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("flows after the message won = %v, want %v (the timer branch never taken)", got, want)
	}
}

// TestJobTakeHistoryRecordsClaimsAndLeases: a person claiming and unclaiming a user
// task, and a Worker leasing a service task, failing it and leasing it again, each
// leave the take-up and hand-back in order, against the element instance whose job it
// was. A user task assigned by the model is not "taken" when it opens — nobody has
// started on it — so creation leaves nothing.
func TestJobTakeHistoryRecordsClaimsAndLeases(t *testing.T) {
	t.Run("user task", func(t *testing.T) {
		h := openHarness(t, t.TempDir())
		defer h.close(t)
		cp, jobType := userTaskProcess(t)
		p := engine.New(1, h.log, h.store, &manualClock{})
		p.Deploy(cp)
		if err := p.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		p.CreateInstance(cp.Key)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		jobKey := singleActivatableJob(t, h.store, jobType)
		jv, _, err := h.store.GetJob(jobKey)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		for _, who := range []string{"anna", "", "ben"} {
			p.AssignJob(jobKey, who)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}
		}
		p.CompleteJob(jobKey)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		assertTakes(t, h.store, jv.ProcessInstanceKey, jv.ElementInstanceKey,
			[]byte{state.JobTaken, state.JobReleased, state.JobTaken})
	})

	t.Run("service task", func(t *testing.T) {
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
		jobKey := singleActivatableJob(t, h.store, jobType)
		jv, _, err := h.store.GetJob(jobKey)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		p.ActivateJob(jobKey, "worker-1", 60e9)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		p.FailJob(jobKey, 2, "try again", 0)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		p.ActivateJob(jobKey, "worker-2", 60e9)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		p.CompleteJob(jobKey)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		assertTakes(t, h.store, jv.ProcessInstanceKey, jv.ElementInstanceKey,
			[]byte{state.JobTaken, state.JobReleased, state.JobTaken})
	})
}

// TestHeatmapCountersReplay: rebuilding state from the log into an empty store gives
// the same flow counts and the same take history, timestamps included — both are
// folded from the event alone (I4/I6).
func TestHeatmapCountersReplay(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := userTaskProcess(t)
	clk := &manualClock{}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h.store, jobType)
	jv, _, err := h.store.GetJob(jobs[0])
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	p.AssignJob(jobs[0], "anna")
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	liveFlows := engineFlowTotals(t, h.store, cp.Key)
	liveTakes := takeRows(t, h.store, jv.ProcessInstanceKey)
	if len(liveFlows) == 0 || len(liveTakes) == 0 {
		t.Fatalf("nothing to compare: flows=%v takes=%v", liveFlows, liveTakes)
	}

	fresh, err := state.Open(filepath.Join(t.TempDir(), "replay-state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer fresh.Close()
	p2 := engine.New(1, h.log, fresh, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	if got := engineFlowTotals(t, fresh, cp.Key); !reflect.DeepEqual(got, liveFlows) {
		t.Errorf("replayed flows = %v, want %v", got, liveFlows)
	}
	if got := takeRows(t, fresh, jv.ProcessInstanceKey); !reflect.DeepEqual(got, liveTakes) {
		t.Errorf("replayed takes = %v, want %v", got, liveTakes)
	}
}

type takeRow struct {
	ts         int64
	elementKey uint64
	kind       byte
}

func takeRows(t *testing.T, s *state.Store, pi uint64) []takeRow {
	t.Helper()
	var rows []takeRow
	if err := s.JobTakeHistory(pi, func(ts int64, _ uint64, v state.JobTakeValue) error {
		rows = append(rows, takeRow{ts, v.ElementInstanceKey, v.Kind})
		return nil
	}); err != nil {
		t.Fatalf("JobTakeHistory: %v", err)
	}
	return rows
}

func assertTakes(t *testing.T, s *state.Store, pi, elementKey uint64, want []byte) {
	t.Helper()
	rows := takeRows(t, s, pi)
	var kinds []byte
	for i, r := range rows {
		if r.elementKey != elementKey {
			t.Errorf("take %d names element instance %d, want %d", i, r.elementKey, elementKey)
		}
		if i > 0 && r.ts < rows[i-1].ts {
			t.Errorf("take %d at %d is before take %d at %d", i, r.ts, i-1, rows[i-1].ts)
		}
		kinds = append(kinds, r.kind)
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("take kinds = %v, want %v", kinds, want)
	}
}

func engineFlowTotals(t *testing.T, s *state.Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	if err := s.FlowTakenTotals(def, func(f int32, c int64) error { m[f] += c; return nil }); err != nil {
		t.Fatalf("FlowTakenTotals: %v", err)
	}
	return m
}
