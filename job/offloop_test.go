package job_test

import (
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// A round's handlers run concurrently, which is what removes the amplification the
// serial runner had: fifty parked jobs against a slow host used to cost fifty
// timeouts one after another. Now a round costs the slowest of them, not their sum.
func TestWorkRunsHandlersConcurrently(t *testing.T) {
	const n = 8
	r := job.NewRunner(nil, nil)
	started := make(chan struct{}, n)
	release := make(chan struct{})
	r.Handle(1, func(state.Reader) job.Handler {
		return func(job.Job) error {
			started <- struct{}{}
			<-release
			return nil
		}
	})

	jobs := make([]job.Job, n)
	for i := range jobs {
		jobs[i] = job.Job{Key: uint64(i + 1), Type: 1}
	}
	done := make(chan []job.Outcome, 1)
	go func() { done <- r.Work(jobs, nil) }()

	// Every handler is in flight before any of them is allowed to finish. Serially
	// this deadlocks; concurrently it does not.
	for range n {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("handlers did not run concurrently")
		}
	}
	close(release)
	if got := <-done; len(got) != n {
		t.Errorf("%d outcomes, want %d", len(got), n)
	}
}

// Each handler is built for the round it runs in, bound to that round's read view.
// That is what lets a handler keep reading "the store" while no longer running on
// the goroutine that owns it.
func TestHandlersAreBuiltPerRoundWithTheRoundsReader(t *testing.T) {
	r := job.NewRunner(nil, nil)
	var mu sync.Mutex
	var seen []state.Reader
	r.Handle(1, func(rd state.Reader) job.Handler {
		mu.Lock()
		seen = append(seen, rd)
		mu.Unlock()
		return func(job.Job) error { return nil }
	})

	view := &fakeReader{}
	r.Work([]job.Job{{Key: 1, Type: 1}, {Key: 2, Type: 1}}, view)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("the handler was built %d times for one round, want once", len(seen))
	}
	if seen[0] != state.Reader(view) {
		t.Errorf("the handler was built with %v, want the round's read view", seen[0])
	}
}

// A handler's error becomes a failure outcome rather than aborting the round: one
// failing job must not stop the others, which is what the serial runner promised
// and what a concurrent one has to keep promising.
func TestOneFailingHandlerDoesNotStopTheRound(t *testing.T) {
	r := job.NewRunner(nil, nil)
	r.Handle(1, func(state.Reader) job.Handler {
		return func(j job.Job) error {
			if j.Key == 2 {
				return errBoom
			}
			return nil
		}
	})
	got := r.Work([]job.Job{{Key: 1, Type: 1}, {Key: 2, Type: 1}, {Key: 3, Type: 1}}, nil)
	if len(got) != 3 {
		t.Fatalf("%d outcomes, want 3", len(got))
	}
	byKey := map[uint64]job.Outcome{}
	for _, o := range got {
		byKey[o.Job.Key] = o
	}
	if byKey[2].Err == nil {
		t.Error("the failing job produced no error outcome")
	}
	for _, k := range []uint64{1, 3} {
		if byKey[k].Err != nil {
			t.Errorf("job %d failed because another did: %v", k, byKey[k].Err)
		}
	}
}

// A job whose type nothing handles is not this runner's work and must be left
// alone — it belongs to an external worker.
func TestWorkSkipsUnhandledTypes(t *testing.T) {
	r := job.NewRunner(nil, nil)
	r.Handle(1, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	got := r.Work([]job.Job{{Key: 1, Type: 1}, {Key: 2, Type: 99}}, nil)
	if len(got) != 1 || got[0].Job.Key != 1 {
		t.Errorf("outcomes = %+v, want only the handled job", got)
	}
}
