package job_test

import (
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestACompletionThatStatesAnOutcomeIsRecordedWithIt: a handler that completes with
// how an action ended (a shop send task, ADR-0429 §4) reaches the engine through the
// completion that carries the outcome, so the outcome is written in the batch that
// completes the job and is stamped with the instance that ran it. A handler that
// states nothing completes as before and writes no outcome.
func TestACompletionThatStatesAnOutcomeIsRecordedWithIt(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	stated := model.ActionOutcomeValue{OrderID: "ord_1", Position: "mailbox", CommandID: "c-1",
		Source: "atlas:shop", Action: "storage-extend", Effect: "change", Outcome: "completed",
		EventType: "mailbox.storage-extend.completed", ItemID: "mailbox", At: 1000}
	r.HandleCompleting(jobType, func(state.Reader) job.CompletingHandler {
		return func(job.Job) (job.Completion, error) {
			v := stated
			return job.Completion{Outcome: &v}, nil
		}
	})

	var pi uint64
	p.CreateInstanceReporting(defKey, &pi)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs, err := r.Claim()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim = %d job(s), %v; want the one job", len(jobs), err)
	}
	r.Submit(r.Work(jobs, store))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if _, ok, _ := store.GetJob(jobs[0].Key); ok {
		t.Fatal("the job that stated an outcome was not completed")
	}
	got, ok, err := store.ActionOutcome("ord_1", "mailbox", "c-1")
	if err != nil || !ok {
		t.Fatalf("the stated outcome was not recorded: ok=%v err=%v", ok, err)
	}
	if got.Outcome != "completed" || got.Source != "atlas:shop" {
		t.Errorf("recorded %q from %q, want completed from atlas:shop", got.Outcome, got.Source)
	}
	if got.InstanceKey != pi {
		t.Errorf("recorded against instance %d, want the one that ran the job, %d", got.InstanceKey, pi)
	}
}
