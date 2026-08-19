package engine_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// historyTTLProcess builds Start → ServiceTask → End with a per-definition history TTL
// (ADR-0144): how long the instance's *finished* record is kept. The service task lets a
// test choose how the instance ends — completed via its job, or terminated while parked.
func historyTTLProcess(t testing.TB, historyTtlNanos int64) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "history-ttl", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.SetHistoryTtl(historyTtlNanos)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(task).Detail).JobType
}

// scheduledExpiries returns every scheduled history purge in the store — the whole
// due-date index, read with a far-future upper bound (ADR-0146).
func scheduledExpiries(t *testing.T, s *state.Store) map[uint64]int64 {
	t.Helper()
	out := map[uint64]int64{}
	if _, err := s.DueHistoryExpiries(math.MaxInt64, 1_000, func(due int64, piKey uint64) error {
		out[piKey] = due
		return nil
	}); err != nil {
		t.Fatalf("DueHistoryExpiries: %v", err)
	}
	return out
}

// TestHistoryTTLSchedulesPurgeOnCompletion: an instance of a TTL-bearing definition
// finishes, and that terminal event both stamps the purge due date on its history record
// and indexes it by that date — so the retention sweep finds it by asking what is due,
// never by walking the history (ADR-0146).
func TestHistoryTTLSchedulesPurgeOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const ttl = int64(60e9)
	cp, jobType := historyTTLProcess(t, ttl)
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
	// While the instance runs there is nothing to purge: the schedule is a property of a
	// *finished* instance.
	if got := scheduledExpiries(t, h.store); len(got) != 0 {
		t.Fatalf("a running instance scheduled a purge: %v", got)
	}

	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}

	piKey := model.NewKey(1, 1)
	done := completedInstances(t, h.store)
	rec, ok := done[piKey]
	if !ok || rec.State != model.PICompleted {
		t.Fatalf("history[%d] = %+v (ok=%v), want a completed record", piKey, rec, ok)
	}
	want := int64(1_000) + ttl
	if rec.PurgeDueDate != want {
		t.Errorf("PurgeDueDate = %d, want CompletedAt+TTL = %d", rec.PurgeDueDate, want)
	}
	if got := scheduledExpiries(t, h.store); len(got) != 1 || got[piKey] != want {
		t.Fatalf("scheduled expiries = %v, want {%d: %d}", got, piKey, want)
	}
	// Due-ness is what the index selects on: nothing before the date, the instance at it.
	if _, err := h.store.DueHistoryExpiries(want-1, 10, func(int64, uint64) error {
		t.Error("an instance came due before its history TTL elapsed")
		return nil
	}); err != nil {
		t.Fatalf("DueHistoryExpiries: %v", err)
	}
}

// TestHistoryTTLSchedulesPurgeOnTermination: a terminated instance is finished too, so
// it is scheduled exactly like a completed one — the case that matters when an instance
// TTL (ADR-0085) is what ended it.
func TestHistoryTTLSchedulesPurgeOnTermination(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const ttl = int64(30e9)
	cp, _ := historyTTLProcess(t, ttl)
	clk := &fixedClock{t: 5_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := model.NewKey(1, 1)
	p.CancelInstance(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after cancel: %v", err)
	}

	want := int64(5_000) + ttl
	if got := scheduledExpiries(t, h.store); len(got) != 1 || got[piKey] != want {
		t.Fatalf("scheduled expiries after termination = %v, want {%d: %d}", got, piKey, want)
	}
}

// TestNoHistoryTTLSchedulesNothing: a definition that declares no history TTL leaves the
// index empty — retention for it is the operator's server-wide age, resolved by the
// sweep, not a schedule (ADR-0146). Opt-in, exactly like the instance TTL.
func TestNoHistoryTTLSchedulesNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)
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
	jobs := activatableJobs(t, h.store, jobType)
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}

	if got := scheduledExpiries(t, h.store); len(got) != 0 {
		t.Fatalf("a definition without a history TTL scheduled %v", got)
	}
	if rec, ok := completedInstances(t, h.store)[model.NewKey(1, 1)]; !ok || rec.PurgeDueDate != 0 {
		t.Fatalf("history record = %+v (ok=%v), want PurgeDueDate 0", rec, ok)
	}
}

// TestHistoryExpiryRebuildsOnReplay is the recovery half of the schedule: the state
// store is thrown away and rebuilt from the log alone, and the index must come back
// identical — the due date rides the terminal event, so the fold never needs a clock or
// a definition to place it (I4/I6).
func TestHistoryExpiryRebuildsOnReplay(t *testing.T) {
	dir := t.TempDir()
	const ttl = int64(60e9)
	cp, jobType := historyTTLProcess(t, ttl)
	clk := &fixedClock{t: 7_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	p1.CompleteJob(activatableJobs(t, h1.store, jobType)[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1 (complete): %v", err)
	}
	live := scheduledExpiries(t, h1.store)
	h1.close(t)

	// Wipe the materialized state: the log is the source of truth, replay rebuilds it.
	if err := os.RemoveAll(filepath.Join(dir, "state")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	replayed := scheduledExpiries(t, h2.store)
	if len(replayed) != len(live) {
		t.Fatalf("after replay %d scheduled expiries, live had %d", len(replayed), len(live))
	}
	for k, due := range live {
		if replayed[k] != due {
			t.Errorf("after replay expiry[%d] = %d, want %d", k, replayed[k], due)
		}
	}
}
