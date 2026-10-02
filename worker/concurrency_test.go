package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// jobStub is an engine stand-in that hands out a fixed number of jobs of one type
// and records what the worker asked for. It answers at most perPoll jobs to a poll,
// however many the worker asked for, so a test can make the worker come back for
// more while earlier work is still running. Once the jobs are gone a poll is held
// open for the wait the worker asked for and answered empty, which is what a real
// long poll does with an empty queue.
type jobStub struct {
	t       *testing.T
	perPoll int
	// ignoreMax answers perPoll jobs whatever the worker asked for — a server
	// misbehaving, which a real engine does not do.
	ignoreMax bool

	mu        sync.Mutex
	remaining int
	nextKey   uint64
	asked     []int // maxJobs of every poll, in order
	completed int
}

func newJobStub(t *testing.T, jobs, perPoll int) (*jobStub, *httptest.Server) {
	s := &jobStub{t: t, perPoll: perPoll, remaining: jobs, nextKey: 1}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	return s, ts
}

func (s *jobStub) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/activate"):
		var body struct {
			MaxJobs int   `json:"maxJobs"`
			WaitMs  int64 `json:"waitMs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.asked = append(s.asked, body.MaxJobs)
		n := min(body.MaxJobs, s.perPoll, s.remaining)
		if s.ignoreMax {
			n = min(s.perPoll, s.remaining)
		}
		jobs := make([]string, 0, n)
		for range n {
			jobs = append(jobs, fmt.Sprintf(`{"jobKey":%d,"type":"call","retries":3,"leaseToken":%d}`, s.nextKey, s.nextKey))
			s.nextKey++
		}
		s.remaining -= n
		s.mu.Unlock()
		if n == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Duration(body.WaitMs) * time.Millisecond):
			}
		}
		_, _ = w.Write([]byte(`{"jobs":[` + strings.Join(jobs, ",") + `]}`))
	case strings.HasSuffix(r.URL.Path, "/complete"):
		s.mu.Lock()
		s.completed++
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	default:
		s.t.Errorf("unexpected call to %s", r.URL.Path)
	}
}

func (s *jobStub) snapshot() (asked []int, completed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.asked...), s.completed
}

// gate is a handler that holds every job until released, and counts how many it is
// holding at once.
type gate struct {
	entered chan struct{}
	release chan struct{}

	mu      sync.Mutex
	running int
	peak    int
}

func newGate() *gate {
	return &gate{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (g *gate) exec() worker.Exec {
	return worker.ExecFunc(func(ctx context.Context, _ worker.Job) (map[string]any, error) {
		g.mu.Lock()
		g.running++
		g.peak = max(g.peak, g.running)
		g.mu.Unlock()
		g.entered <- struct{}{}
		select {
		case <-g.release:
		case <-ctx.Done():
		}
		g.mu.Lock()
		g.running--
		g.mu.Unlock()
		return map[string]any{"ok": true}, nil
	})
}

func (g *gate) waitEntered(t *testing.T, n int) {
	t.Helper()
	for i := range n {
		select {
		case <-g.entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d jobs were running at once; the rest were never started", i, n)
		}
	}
}

func (g *gate) peakRunning() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

func runUntilCancelled(t *testing.T, w *worker.Worker) (cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	exited := make(chan error, 1)
	go func() { exited <- w.Run(ctx) }()
	return func() {
		stop()
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("Run returned %v after cancellation, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the worker did not stop when its context was cancelled")
		}
	}
}

// MaxJobs is how many jobs of one type a worker runs at once, not how many it leases
// and then works one after another. A worker that leased three and ran them in turn
// would hold the second and third while their lease ran down, and a slow endpoint
// would cost the sum of its calls instead of the slowest of them.
func TestWorkerRunsJobsOfOneTypeConcurrently(t *testing.T) {
	stub, ts := newJobStub(t, 3, 3)
	g := newGate()
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "rest-1", MaxJobs: 3, Wait: 20 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{"call": g.exec()},
	})
	cancel := runUntilCancelled(t, w)
	defer cancel()

	g.waitEntered(t, 3)
	if peak := g.peakRunning(); peak != 3 {
		t.Errorf("peak concurrency = %d, want 3", peak)
	}
	close(g.release)
	waitFor(t, "every job to be reported", func() bool {
		_, completed := stub.snapshot()
		return completed == 3
	})
}

// A worker leases only what it can start now. With one of two slots busy it asks
// for one job, not two: a job leased into a queue inside the worker is a job no
// other worker can take, whose lease is running down while it waits. And when every
// slot is busy it does not poll at all.
func TestWorkerLeasesOnlyTheSlotsItHasFree(t *testing.T) {
	stub, ts := newJobStub(t, 2, 1)
	g := newGate()
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "rest-1", MaxJobs: 2, Wait: 20 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{"call": g.exec()},
	})
	cancel := runUntilCancelled(t, w)
	defer cancel()

	// The stub answers one job per poll, so the second running job can only have
	// come from a second poll made while the first job was still being worked.
	g.waitEntered(t, 2)
	asked, _ := stub.snapshot()
	if len(asked) != 2 {
		t.Fatalf("polls while both slots filled = %v, want exactly two", asked)
	}
	if asked[0] != 2 || asked[1] != 1 {
		t.Errorf("maxJobs asked = %v, want [2 1]: the second poll is made with one slot busy", asked)
	}

	close(g.release)
	waitFor(t, "both jobs to be reported", func() bool {
		_, completed := stub.snapshot()
		return completed == 2
	})
	// Once the slots free up the worker polls again for both of them.
	waitFor(t, "a poll for the freed slots", func() bool {
		asked, _ := stub.snapshot()
		return len(asked) >= 3 && asked[len(asked)-1] == 2
	})
}

// The default is unchanged: a worker given no MaxJobs runs one job at a time, which
// is what an operator who wrote a --handle command line and never thought about
// concurrency has been getting all along.
func TestWorkerRunsOneJobAtATimeByDefault(t *testing.T) {
	stub, ts := newJobStub(t, 2, 2)
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "cmd-1", Wait: 20 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{"call": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
			return nil, nil
		})},
	})
	cancel := runUntilCancelled(t, w)
	defer cancel()

	waitFor(t, "both jobs to be reported", func() bool {
		_, completed := stub.snapshot()
		return completed == 2
	})
	// The stub would hand out both jobs to one poll; asking for one is what keeps
	// the second from being leased while the first runs.
	asked, _ := stub.snapshot()
	for i, n := range asked {
		if n != worker.DefaultMaxJobs {
			t.Errorf("poll %d asked for %d jobs, want the default of %d", i, n, worker.DefaultMaxJobs)
		}
	}
}

// RunOnce — the one-shot "drain and exit" mode — works what one poll leased at once
// too, and returns only after every one of them has been reported.
func TestRunOnceWorksALeasedBatchConcurrently(t *testing.T) {
	stub, ts := newJobStub(t, 3, 3)
	g := newGate()
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "rest-1", MaxJobs: 3, Wait: 20 * time.Millisecond,
		Handlers: map[string]worker.Exec{"call": g.exec()},
	})
	done := make(chan error, 1)
	go func() { done <- w.RunOnce(context.Background()) }()

	g.waitEntered(t, 3)
	close(g.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunOnce did not return after its jobs finished")
	}
	if _, completed := stub.snapshot(); completed != 3 {
		t.Errorf("completed = %d when RunOnce returned, want all 3 reported", completed)
	}
}

// The bound is on what runs, whatever the server says. A server that answered with
// more jobs than were asked for still gets them all worked — they are leased to this
// worker, and dropping them would park them until their lease ran out — but one at a
// time when the worker has one place, not all at once.
func TestWorkerKeepsItsBoundWhenTheServerSendsTooMany(t *testing.T) {
	stub, ts := newJobStub(t, 3, 3)
	stub.ignoreMax = true
	g := newGate()
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "rest-1", MaxJobs: 1, Wait: 20 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{"call": g.exec()},
	})
	cancel := runUntilCancelled(t, w)
	defer cancel()

	for range 3 {
		g.waitEntered(t, 1)
		g.release <- struct{}{}
	}
	waitFor(t, "all three jobs to be reported", func() bool {
		_, completed := stub.snapshot()
		return completed == 3
	})
	if peak := g.peakRunning(); peak != 1 {
		t.Errorf("peak concurrency = %d with one place, want 1", peak)
	}
}
