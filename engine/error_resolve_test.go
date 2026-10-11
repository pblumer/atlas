package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// uncaughtWorkerErrorProcess builds Start → serviceTask("work") → End with no error
// handler anywhere, so a worker's thrown error reaches the root uncaught (ADR-0089).
func uncaughtWorkerErrorProcess(t testing.TB, key uint64) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "workeruncaught", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(task).Detail).JobType
}

// replayed opens dir's log with a fresh state store and replays it into a processor
// that knows cp: the state recovery rebuilds from the events alone (I4).
func replayed(t *testing.T, dir string, cp *compiler.CompiledProcess) (*engine.Processor, *state.Store) {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	s, err := state.Open(filepath.Join(dir, "state-replayed"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = l.Close() })
	p := engine.New(1, l, s, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	return p, s
}

// onlyIncident returns the single open incident, failing unless there is exactly one.
func onlyIncident(t *testing.T, s *state.Store) model.IncidentValue {
	t.Helper()
	incs := incidents(t, s)
	if len(incs) != 1 {
		t.Fatalf("incidents = %d, want exactly 1", len(incs))
	}
	for _, v := range incs {
		return v
	}
	panic("unreachable")
}

// An uncaught worker error is a job incident, not a job-less one: the job stays, its
// retries spent, so resolving it hands the job back to the workers exactly as resolving
// a failed job does (ADR-0061). Before, the job was canceled and the incident pointed at
// nothing — resolving it cleared the incident and left the token on the task with no
// job and no incident, a stuck instance nothing reported.
func TestAnUncaughtWorkerErrorLeavesItsJobForTheRetry(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := uncaughtWorkerErrorProcess(t, 80)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	job := singleActivatableJob(t, h.store, jobType)
	p.ActivateJob(job, "w1", 60e9)
	p.ThrowJobError(job, "OOPS")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}

	inc := onlyIncident(t, h.store)
	if inc.JobKey != job {
		t.Errorf("incident job = %d, want the job that threw (%d)", inc.JobKey, job)
	}
	if inc.Reason != model.IncidentUnhandledError {
		t.Errorf("incident reason = %d, want IncidentUnhandledError", inc.Reason)
	}
	if inc.Message != "unhandled error 'OOPS'" {
		t.Errorf("incident message = %q, want it to name the code", inc.Message)
	}
	jv, ok, err := h.store.GetJob(job)
	if err != nil || !ok {
		t.Fatalf("job after an uncaught error: present=%v err=%v, want it kept for the retry", ok, err)
	}
	if jv.Retries != 0 || jv.LeaseExpiresAt != 0 {
		t.Errorf("job retries=%d lease=%d, want 0 and 0 (spent, and given back)", jv.Retries, jv.LeaseExpiresAt)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("the job is still offered to workers while its incident is open")
	}

	p.ResolveIncident(inc.ElementInstanceKey, 2)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("incidents after resolve = %d, want 0", n)
	}
	if got := singleActivatableJob(t, h.store, jobType); got != job {
		t.Fatalf("activatable job after resolve = %d, want the same job %d", got, job)
	}
	if jv, _, _ := h.store.GetJob(job); jv.Retries != 2 {
		t.Errorf("retries after resolve = %d, want the 2 the resolve gave", jv.Retries)
	}

	p.CompleteJob(job)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the retried job completed: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// The kept job and its incident are state built from events, so a replay of the log
// rebuilds both, and resolving after the restart retries the job just the same (I4).
func TestAnUncaughtWorkerErrorRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := uncaughtWorkerErrorProcess(t, 81)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	job := singleActivatableJob(t, h1.store, jobType)
	p1.ThrowJobError(job, "OOPS")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	live := onlyIncident(t, h1.store)
	h1.close(t)

	p2, store2 := replayed(t, dir, cp)
	if got := onlyIncident(t, store2); got != live {
		t.Fatalf("incident after replay = %+v, want the live one %+v", got, live)
	}
	if _, ok, _ := store2.GetJob(job); !ok {
		t.Fatal("the kept job is gone after replay")
	}
	p2.ResolveIncident(live.ElementInstanceKey, 1)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve after replay): %v", err)
	}
	if got := singleActivatableJob(t, store2, jobType); got != job {
		t.Fatalf("activatable job after resolve = %d, want %d", got, job)
	}
}

// An error end event nothing catches parks on an incident marked as an unhandled
// error. Resolving it throws the error again: caught now if a handler has come into
// reach, otherwise parked again on a fresh incident. Before, resolving cleared the
// incident and did nothing else, leaving the token on the end event behind no
// incident at all.
func TestResolvingAnUncaughtErrorEndThrowsItAgain(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	b := compiler.NewBuilder(82, "errenduncaught", 1)
	start := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(start, errEnd)
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
	first := onlyIncident(t, h.store)
	if first.Reason != model.IncidentUnhandledError || first.JobKey != 0 {
		t.Fatalf("incident reason=%d job=%d, want IncidentUnhandledError and no job", first.Reason, first.JobKey)
	}

	clk.t = 2_000
	p.ResolveIncident(first.ElementInstanceKey, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	again := onlyIncident(t, h.store)
	if again.ElementInstanceKey != first.ElementInstanceKey || again.RaisedAt != 2_000 {
		t.Errorf("incident after resolve = %+v, want a fresh one on the same end event raised at 2000", again)
	}
	if again.Message != "unhandled error 'BOOM'" {
		t.Errorf("incident message = %q, want the code named again", again.Message)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after the re-throw: process=%d element=%d, want the instance still parked on the end event", pi, ei)
	}
	h.close(t)

	// The re-throw is events like the first throw, so a replay rebuilds the fresh
	// incident and nothing else.
	_, replayedStore := replayed(t, dir, cp)
	if got := onlyIncident(t, replayedStore); got != again {
		t.Errorf("incident after replay = %+v, want the re-thrown one %+v", got, again)
	}
}

// An incident an older atlas raised on an uncaught error end carries no reason. The
// end event's node type says what it is all the same, so resolving it throws the error
// again too.
func TestResolvingAnOlderUncaughtErrorEndThrowsItAgain(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(83, "errendolder", 1)
	start := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(start, errEnd)
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
	// Rewrite the incident as a record from before the reason existed decodes.
	older := onlyIncident(t, h.store)
	older.Reason = model.IncidentUnclassified
	tx := h.store.NewTransaction()
	if err := tx.PutIncident(&older); err != nil {
		t.Fatalf("PutIncident: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	p.ResolveIncident(older.ElementInstanceKey, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if again := onlyIncident(t, h.store); again.Reason != model.IncidentUnhandledError {
		t.Errorf("incident after resolve = %+v, want a fresh unhandled-error incident", again)
	}
}
