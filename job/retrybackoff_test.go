package job_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// failRecorder is the processor with its FailJob calls written down, so a test can see
// the backoff the runner asked for and not only what became of the job.
type failRecorder struct {
	*engine.Processor
	backoffs []int64
	left     []int32
}

func (f *failRecorder) FailJob(jobKey uint64, retries int32, message string, backoff int64) {
	f.backoffs = append(f.backoffs, backoff)
	f.left = append(f.left, retries)
	f.Processor.FailJob(jobKey, retries, message, backoff)
}

// TestAFailedJobWaitsItsBackoffInsteadOfRetryingAtOnce is the regression for the
// devenv's replication incidents: add-group-owner right after create-group answered 404
// because Graph had not yet replicated the group, and the in-process runner spent the
// task's three retries within one second (backoff 0), raising an incident for a fault
// that would have cleared by itself. With a backoff installed the first failure holds
// the job off the activatable index: the drive ends after one attempt, and nothing is an
// incident yet (ADR-0111, amendment 2026-11-04).
func TestAFailedJobWaitsItsBackoffInsteadOfRetryingAtOnce(t *testing.T) {
	p, store, jobType, defKey := setup(t) // the service task has retries=3
	rec := &failRecorder{Processor: p}
	runner := job.NewRunner(store, rec)
	runner.SetRetryBackoff(job.DefaultRetryBackoff)
	calls := 0
	runner.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error { calls++; return errors.New("HTTP 404: Request_ResourceNotFound") }
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1: the retry must wait for its backoff", calls)
	}
	if len(rec.backoffs) != 1 || rec.backoffs[0] != int64(15*time.Second) || rec.left[0] != 2 {
		t.Fatalf("FailJob calls: backoffs %v with retries left %v, want one call of 15s with 2 left",
			rec.backoffs, rec.left)
	}
	incidents := 0
	if err := store.Incidents(func(uint64, *model.IncidentValue) error { incidents++; return nil }); err != nil {
		t.Fatalf("Incidents scan: %v", err)
	}
	if incidents != 0 {
		t.Fatalf("incidents = %d, want 0 while retries remain", incidents)
	}
	if pi, ei := active(t, store); pi != 1 || ei != 1 {
		t.Fatalf("process=%d element=%d, want the token still waiting at the task", pi, ei)
	}
}

// TestTheLastRetryWaitsLongestAndTheIncidentDoesNotWait pins the default: fifteen
// seconds while two or more retries remain, a minute before the last one, and no delay
// for a failure that leaves none — that one is an incident, and an operator should see
// it at once.
func TestTheLastRetryWaitsLongestAndTheIncidentDoesNotWait(t *testing.T) {
	for left, want := range map[int32]int64{
		5:  int64(15 * time.Second),
		2:  int64(15 * time.Second),
		1:  int64(time.Minute),
		0:  0,
		-1: 0,
	} {
		if got := job.DefaultRetryBackoff(left); got != want {
			t.Errorf("DefaultRetryBackoff(%d) = %v, want %v", left, time.Duration(got), time.Duration(want))
		}
	}
}

// TestARunnerWithoutBackoffStillRetriesAtOnce keeps the library default: a runner
// nobody installed a backoff on behaves as before, so the many drives that expect a
// failing handler to reach its incident in one call are unaffected.
func TestARunnerWithoutBackoffStillRetriesAtOnce(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	rec := &failRecorder{Processor: p}
	runner := job.NewRunner(store, rec)
	runner.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return errors.New("boom") } })

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	for i, b := range rec.backoffs {
		if b != 0 {
			t.Fatalf("FailJob call %d had backoff %v, want 0 without SetRetryBackoff", i, time.Duration(b))
		}
	}
	if len(rec.backoffs) != 3 {
		t.Fatalf("FailJob called %d times, want 3 (all retries at once)", len(rec.backoffs))
	}
}
