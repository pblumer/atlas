package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// blockingExec stands in for an interpreter that has stopped answering: it
// reports that a script started and then blocks until the test releases it. The
// real hazard it models is any handler that takes time — a script, an outbound
// connector call — not a fault of this particular language.
type blockingExec struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingExec) Run(context.Context, string, map[string]any) (any, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return "released", nil
}

// TestSlowJobHandlerDoesNotStallTheServer is the property ADR-0150 buys: a job
// handler runs off the run-loop goroutine, so a slow or hung one stalls its own
// instance and nothing else. Before it, the handler ran inside the s.do closure
// that started the instance, holding the single writer (I3) — the goroutine that
// serves *every* request — so a wedged connector took the whole server down with
// it while /healthz kept answering ok.
func TestSlowJobHandlerDoesNotStallTheServer(t *testing.T) {
	exec := &blockingExec{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(exec.release) }) }

	_, x := newPwshServer(t, WithScriptWorker(compiler.PwshJobTypeIndex, exec))
	key := deployGreeting(t, x)

	started := make(chan struct{})
	go func() {
		defer close(started)
		x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	}()
	// Let the handler be running before the second request: that is the window in
	// which the writer used to be held.
	defer func() { release(); <-started }()
	select {
	case <-exec.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the script task never started")
	}

	answered := make(chan int, 1)
	go func() {
		code, _ := x.do(http.MethodGet, "/api/v1/stats", "")
		answered <- code
	}()
	select {
	case code := <-answered:
		if code != http.StatusOK {
			t.Fatalf("GET /api/v1/stats = %d, want 200 while a job handler runs", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GET /api/v1/stats blocked while a job handler ran: the run loop is held by the handler")
	}
}
