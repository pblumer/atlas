package engine_test

import "testing"

// TestACanceledJobIsCountedAsCanceled: the job-lifecycle counts are how an operator
// tells work that was withdrawn from work that failed or was finished. Cancelling an
// instance with an open job must show up as exactly one cancellation, and as nothing
// else.
func TestACanceledJobIsCountedAsCanceled(t *testing.T) {
	p, rec, store, done := instrumented(t)
	defer done()

	cp, jobType := linearProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var piKey uint64
	for _, k := range activatableJobs(t, store, jobType) {
		jv, ok, err := store.GetJob(k)
		if err != nil || !ok {
			t.Fatalf("GetJob(%d): ok=%v err=%v", k, ok, err)
		}
		piKey = jv.ProcessInstanceKey
	}
	if piKey == 0 {
		t.Fatal("the instance never reached its service task")
	}

	p.CancelInstance(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	got := rec.jobTotals()
	if got.Created != 1 || got.Canceled != 1 || got.Completed != 0 || got.Failed != 0 {
		t.Fatalf("job totals = %+v, want one created and one canceled", got)
	}
}
