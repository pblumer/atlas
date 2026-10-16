package job_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// errorBoundaryProcess deploys Start → work [error boundary catching code] → done, the
// boundary routed to its own end, and returns what a test needs to drive it.
func errorBoundaryProcess(t *testing.T, code string) (*engine.Processor, *state.Store, int32, uint64) {
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

	b := compiler.NewBuilder(9, "boundary", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryErrorEvent(task, code)
	done, recovered := b.AddEndEvent(), b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p, store, cp.ServiceTask(cp.Node(task).Detail).JobType, cp.Key
}

func countActiveAndIncidents(t *testing.T, store *state.Store) (active, incidents int) {
	t.Helper()
	if err := store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error { active++; return nil }); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if err := store.Incidents(func(uint64, *model.IncidentValue) error { incidents++; return nil }); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	return active, incidents
}

// TestAHandlersBPMNErrorIsThrownNotFailed: an in-process handler ends its job in a BPMN
// error by returning one, wrapped or not, exactly as an external worker does through the
// error endpoint (ADR-0089). The error boundary catching its code takes the instance, no
// attempt is spent on a retry, and no incident is raised.
func TestAHandlersBPMNErrorIsThrownNotFailed(t *testing.T) {
	p, store, jobType, defKey := errorBoundaryProcess(t, "UPN_EXISTS")
	r := job.NewRunner(store, p)
	worked := 0
	r.HandleWithOutput(jobType, func(state.Reader) job.OutputHandler {
		return func(job.Job) ([]model.VariableValue, error) {
			worked++
			return nil, fmt.Errorf("run script: %w", &job.BPMNError{Code: "UPN_EXISTS", Message: "userPrincipalName already exists"})
		}
	})
	p.CreateInstance(defKey)
	if err := r.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if worked != 1 {
		t.Errorf("handler ran %d times, want once: a BPMN error spends no retry", worked)
	}
	if active, incidents := countActiveAndIncidents(t, store); active != 0 || incidents != 0 {
		t.Errorf("active=%d incidents=%d, want 0 and 0: the boundary took the instance to its end", active, incidents)
	}
}

// TestAnUncaughtBPMNErrorWaitsOnAnIncidentWithoutRetrying: a code nothing in reach
// catches parks the job on an incident at once, as an external worker's throw does —
// the model has no path for it, and running the handler again would not give it one.
func TestAnUncaughtBPMNErrorWaitsOnAnIncidentWithoutRetrying(t *testing.T) {
	p, store, jobType, defKey := errorBoundaryProcess(t, "UPN_EXISTS")
	r := job.NewRunner(store, p)
	worked := 0
	r.HandleWithOutput(jobType, func(state.Reader) job.OutputHandler {
		return func(job.Job) ([]model.VariableValue, error) {
			worked++
			return nil, &job.BPMNError{Code: "SOMETHING_ELSE"}
		}
	})
	p.CreateInstance(defKey)
	if err := r.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if worked != 1 {
		t.Errorf("handler ran %d times, want once", worked)
	}
	if active, incidents := countActiveAndIncidents(t, store); active != 1 || incidents != 1 {
		t.Errorf("active=%d incidents=%d, want 1 and 1: parked on an incident naming the code", active, incidents)
	}
}

// TestABPMNErrorWithoutACodeIsAFailure: a code is what a catch matches, so an error
// naming none is the handler's fault and fails the job like any other error — retried,
// then an incident — rather than being thrown as nothing.
func TestABPMNErrorWithoutACodeIsAFailure(t *testing.T) {
	p, store, jobType, defKey := errorBoundaryProcess(t, "UPN_EXISTS")
	r := job.NewRunner(store, p)
	worked := 0
	r.HandleWithOutput(jobType, func(state.Reader) job.OutputHandler {
		return func(job.Job) ([]model.VariableValue, error) {
			worked++
			return nil, &job.BPMNError{Code: "  ", Message: "forgot the code"}
		}
	})
	p.CreateInstance(defKey)
	if err := r.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if worked != 3 {
		t.Errorf("handler ran %d times, want 3: every attempt the task grants", worked)
	}
	if active, incidents := countActiveAndIncidents(t, store); active != 1 || incidents != 1 {
		t.Errorf("active=%d incidents=%d, want 1 and 1", active, incidents)
	}
}

// TestBPMNErrorMessage keeps the wording an incident shows for an uncaught throw.
func TestBPMNErrorMessage(t *testing.T) {
	if got := (&job.BPMNError{Code: "X", Message: "why"}).Error(); got != "BPMN error X: why" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&job.BPMNError{Code: "X"}).Error(); got != "BPMN error X" {
		t.Errorf("Error() = %q", got)
	}
}
