package api

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// TestAConnectorCallDoesNotHoldTheRunLoop is the assertion ADR-0157 step 6 exists
// for, and the one nothing checked before: while an in-process handler is making
// its outbound call, the single writer must still be serving everything else.
//
// Until this change the handler ran inside s.do, so a slow host held the run loop —
// and with it every request, every timer tick — for the length of the call. ADR-0149
// could only bound that with a ten-second budget; this removes it.
func TestAConnectorCallDoesNotHoldTheRunLoop(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	// A handler that blocks until the test lets it go, standing in for a slow host.
	inCall := make(chan struct{})
	release := make(chan struct{})
	defKey := deployBPMN(t, h, jobTypeBPMN("slow", "slow-task"))
	var jobType int32
	srv.do(func() {
		idx, ok := srv.jobTypes.Index("slow-task")
		if !ok {
			t.Error("slow-task was not registered by the deploy")
			return
		}
		jobType = idx
	})
	srv.jobRunner.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error {
			close(inCall)
			<-release
			return nil
		}
	})

	// Starting the instance runs straight into that handler. The request waits for it
	// — that is the contract — so it is made from another goroutine.
	started := make(chan int, 1)
	go func() {
		code, _ := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json")
		started <- code
	}()

	select {
	case <-inCall:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler never ran")
	}

	// The call is in flight. The engine must still answer.
	served := make(chan int, 1)
	go func() {
		code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/stats", "", "")
		served <- code
	}()
	select {
	case code := <-served:
		if code != http.StatusOK {
			t.Errorf("a request served during a worker call got status %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run loop was held for the duration of a worker call — the stall step 6 removes")
	}

	close(release)
	if code := <-started; code != http.StatusOK {
		t.Errorf("start instance: status=%d", code)
	}
}

// The round's handlers run concurrently, so a burst of parked jobs costs the
// slowest of them rather than their sum — the amplification ADR-0156 measured,
// where fifty jobs against a dead host cost fifty consecutive timeouts one after
// another.
//
// The jobs are parked *before* any handler exists, because a claim only takes job
// types something is registered for. That is what puts all five into one round,
// which is the unit this is about.
func TestABurstOfJobsIsWorkedConcurrently(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	defKey := deployBPMN(t, h, jobTypeBPMN("burst", "burst-task"))
	const n = 5
	for range n {
		startInstance(t, h, defKey, "{}")
	}

	var (
		jobType int32
		known   bool
	)
	srv.do(func() { jobType, known = srv.jobTypes.Index("burst-task") })
	if !known {
		t.Fatal("burst-task was not registered by the deploy")
	}

	inCall := make(chan struct{}, n)
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	// Release on the way out too: a failed assertion must not leave handlers parked,
	// or the server's shutdown waits on them and hides the real failure.
	t.Cleanup(letGo)

	srv.jobRunner.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error {
			inCall <- struct{}{}
			<-release
			return nil
		}
	})

	done := make(chan error, 1)
	go func() { done <- srv.drive() }()

	for i := range n {
		select {
		case <-inCall:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d handlers were in flight at once — the round is still serial", i, n)
		}
	}
	letGo()
	if err := <-done; err != nil {
		t.Errorf("drive: %v", err)
	}
}
