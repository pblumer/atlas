package job_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// setup builds an engine over a fresh wal+store with a deployed
// Start → ServiceTask → End process, and returns it with the task's job type.
func setup(t *testing.T) (*engine.Processor, *state.Store, int32, uint64) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })

	b := compiler.NewBuilder(7, "linear", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p, store, jobType, cp.Key
}

func active(t *testing.T, s *state.Store) (pi, ei int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err = s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

func TestRunnerDrivesToCompletion(t *testing.T) {
	p, store, jobType, defKey := setup(t)

	var got []job.Job
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(state.Reader) job.Handler {
		return func(j job.Job) error {
			got = append(got, j)
			return nil
		}
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("handler called %d times, want 1", len(got))
	}
	if got[0].Type != jobType {
		t.Errorf("job type = %d, want %d", got[0].Type, jobType)
	}
	if got[0].Retries != 3 {
		t.Errorf("retries = %d, want 3", got[0].Retries)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

func TestPollOnceDispatchesActivatableJob(t *testing.T) {
	p, store, jobType, defKey := setup(t)

	runner := job.NewRunner(store, p)
	calls := 0
	runner.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { calls++; return nil } })

	// Run to the waiting job, then a single poll dispatches it.
	p.CreateInstance(defKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	n, err := runner.PollOnce()
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 1 || calls != 1 {
		t.Fatalf("dispatched=%d calls=%d, want 1 and 1", n, calls)
	}

	// The completion is queued; processing it finishes the instance.
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("process=%d element=%d, want 0 and 0", pi, ei)
	}

	// Nothing left to dispatch.
	if n, err := runner.PollOnce(); err != nil || n != 0 {
		t.Fatalf("second poll: n=%d err=%v, want 0 nil", n, err)
	}
}

// TestHandlerRetryThenSucceeds covers the transient-failure path: a handler that
// fails once then succeeds is failed with a retry left (not aborted, not an
// incident), re-activated, and completes on the retry — the instance runs through.
func TestHandlerRetryThenSucceeds(t *testing.T) {
	p, store, jobType, defKey := setup(t) // the service task has retries=3
	runner := job.NewRunner(store, p)
	calls := 0
	runner.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error {
			calls++
			if calls == 1 {
				return errors.New("transient")
			}
			return nil
		}
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if calls < 2 {
		t.Fatalf("handler called %d times, want it retried after the transient failure", calls)
	}
	// It completed on the retry: nothing left active, no incident.
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after retry: process=%d element=%d, want 0 and 0 (completed)", pi, ei)
	}
	incidents := 0
	if err := store.Incidents(func(uint64, *model.IncidentValue) error { incidents++; return nil }); err != nil {
		t.Fatalf("Incidents scan: %v", err)
	}
	if incidents != 0 {
		t.Fatalf("incidents = %d, want 0 (transient failure recovered)", incidents)
	}
}

// TestHandlerErrorRaisesIncident: a handler that keeps failing does NOT abort the
// drive (ADR-0061). The job is failed until its retries run out, then an incident
// parks the token — so one bad job (e.g. a business rule task whose decision model
// isn't deployed) can't poison the run loop and fail every future deploy. Drive
// returns nil, the instance stays parked at the element, and an incident exists.
func TestHandlerErrorRaisesIncident(t *testing.T) {
	p, store, jobType, defKey := setup(t) // the service task has retries=3

	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return errors.New("boom") } })

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive error = %v, want nil (failure routed to an incident, not surfaced)", err)
	}
	// The token is still parked at the element, now blocked by an incident.
	if pi, ei := active(t, store); pi != 1 || ei != 1 {
		t.Fatalf("after failing handler: process=%d element=%d, want 1 and 1", pi, ei)
	}
	incidents := 0
	if err := store.Incidents(func(uint64, *model.IncidentValue) error { incidents++; return nil }); err != nil {
		t.Fatalf("Incidents scan: %v", err)
	}
	if incidents != 1 {
		t.Fatalf("incidents = %d, want 1 (retries exhausted → one incident)", incidents)
	}
	// The parked job is off the activatable index, so a second drive is a no-op.
	if err := runner.Drive(); err != nil {
		t.Fatalf("second Drive: %v", err)
	}
}
