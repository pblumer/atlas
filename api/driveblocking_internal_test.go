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

// registerHandler wires an in-process handler for a deployed job type and returns
// the type's interned index.
func registerHandler(t *testing.T, srv *Server, name string, h func(job.Job) error) {
	t.Helper()
	var jobType int32
	srv.do(func() {
		idx, ok := srv.jobTypes.Index(name)
		if !ok {
			t.Errorf("%s was not registered by the deploy", name)
			return
		}
		jobType = idx
	})
	srv.jobRunner.Handle(jobType, func(state.Reader) job.Handler { return h })
}

// TestASlowWorkerDoesNotBlockAnIndependentInstance is the audit's F13 case.
//
// TestAConnectorCallDoesNotHoldTheRunLoop already proves the *run loop* is free
// while a handler waits. It was not the whole story: driving was serialized end to
// end by a mutex that covered the handlers too, so a second request that had to
// drive — starting, completing or cancelling anything at all — waited for the first
// worker's timeout even though the loop itself was idle. The loop being free is no
// comfort to a caller queued behind a dead host.
//
// The mutex now covers claiming and submitting and not the call in between
// (ADR-0274). This starts an instance of an unrelated
// definition while a worker is stuck and requires it to finish on its own.
func TestASlowWorkerDoesNotBlockAnIndependentInstance(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	inCall := make(chan struct{})
	release := make(chan struct{})
	// Released on every exit, including a failing one: a test that ends while the
	// handler is still blocked leaves a goroutine holding the binary open, and the
	// run dies of the package timeout instead of reporting what went wrong.
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	defer letGo()
	stuckDef := deployBPMN(t, h, jobTypeBPMN("stuck", "stuck-task"))
	registerHandler(t, srv, "stuck-task", func(job.Job) error {
		close(inCall)
		<-release
		return nil
	})
	freeDef := deployBPMN(t, h, jobTypeBPMN("free", "free-task"))
	registerHandler(t, srv, "free-task", func(job.Job) error { return nil })

	stuck := make(chan int, 1)
	go func() {
		code, _ := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", stuckDef), "{}", "application/json")
		stuck <- code
	}()
	select {
	case <-inCall:
	case <-time.After(10 * time.Second):
		t.Fatal("the stuck handler never ran")
	}

	// One worker is hanging. Starting an unrelated instance must not wait for it —
	// and starting one drives, which is exactly what the old mutex serialized.
	free := make(chan int, 1)
	go func() {
		code, _ := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", freeDef), "{}", "application/json")
		free <- code
	}()
	select {
	case code := <-free:
		if code != http.StatusOK {
			t.Errorf("the independent instance returned status %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("starting an unrelated instance waited on a hanging worker — the stall F13 reports")
	}

	letGo()
	if code := <-stuck; code != http.StatusOK {
		t.Errorf("the stuck instance eventually returned status %d", code)
	}
}
