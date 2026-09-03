package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// newOffLoopServer builds a real server over a temp dir. The off-loop read path
// is about the run loop, so nothing here may be faked: the test needs the actual
// loop goroutine to observe that it stays free.
func newOffLoopServer(t *testing.T) (*Server, func()) {
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
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Close is idempotent here on purpose: the shutdown test closes the server
	// itself, and the cleanup must not close the quit channel a second time.
	var once sync.Once
	closeSrv := func() { once.Do(srv.Close) }
	t.Cleanup(func() { closeSrv(); _ = store.Close(); _ = log.Close() })
	return srv, closeSrv
}

// TestReadOffLoopLeavesTheRunLoopFree is the property the whole helper exists for:
// while the query body runs, the loop must still be taking work. If the body ran
// on the loop, the dispatch below could never be served — the loop's queue is
// unbuffered, so a closure dispatched from the loop goroutine has nobody to hand
// it to — and the wait would run out.
//
// The deadline is a failure guard, not a timing assumption: on the intended path
// the dispatch is served immediately and the test does not wait at all. It is the
// only way to assert "did not block" without asserting on a duration.
func TestReadOffLoopLeavesTheRunLoopFree(t *testing.T) {
	srv, _ := newOffLoopServer(t)

	ran := false
	err := srv.readOffLoop(func(v *state.ReadView, _ defIndex) error {
		if v == nil {
			t.Error("no read view handed to the query")
		}
		ran = true
		served := make(chan struct{})
		go func() {
			srv.do(func() {})
			close(served)
		}()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("the run loop was still held while the off-loop read ran")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("readOffLoop: %v", err)
	}
	if !ran {
		t.Fatal("the query body never ran")
	}
}

// TestReadOffLoopViewIgnoresLaterWrites checks the second guarantee: the view is a
// snapshot, so a scan sees one coherent state. Without it a long scan reads rows
// written after it started, and a caller folding those rows reports a state that
// never existed as a whole.
func TestReadOffLoopViewIgnoresLaterWrites(t *testing.T) {
	srv, _ := newOffLoopServer(t)

	err := srv.readOffLoop(func(v *state.ReadView, _ defIndex) error {
		before, err := v.ActiveProcessInstanceCount()
		if err != nil {
			return err
		}
		// Write through the loop while the view is open. The store sees it; the
		// view, taken before it, must not.
		srv.do(func() {
			tx := srv.store.NewTransaction()
			if err := tx.PutProcessInstance(9999, &model.ProcessInstanceValue{ProcessDefKey: 1}); err != nil {
				t.Errorf("PutProcessInstance: %v", err)
				return
			}
			if err := tx.Commit(); err != nil {
				t.Errorf("Commit: %v", err)
			}
		})

		after, err := v.ActiveProcessInstanceCount()
		if err != nil {
			return err
		}
		if after != before {
			t.Errorf("the view saw %d active instances after a concurrent write, want the %d it was taken with", after, before)
		}
		live, err := srv.store.ActiveProcessInstanceCount()
		if err != nil {
			return err
		}
		if live != before+1 {
			t.Errorf("the live store shows %d active instances, want %d — the write under test did not land", live, before+1)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("readOffLoop: %v", err)
	}
}

// TestReadOffLoopReportsAClosingLoop covers the shutdown path: Do does not run the
// closure once the loop is stopping, so no view is taken and the caller must be
// told rather than handed an empty answer that reads like "nothing matched".
func TestReadOffLoopReportsAClosingLoop(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()

	called := false
	err := srv.readOffLoop(func(*state.ReadView, defIndex) error {
		called = true
		return nil
	})
	if err != errLoopClosing {
		t.Fatalf("err = %v, want %v", err, errLoopClosing)
	}
	if called {
		t.Fatal("the query body ran even though no view was taken")
	}
}

// TestDefMetaVersionTagWithoutAModel covers the definition whose compiled model is
// not loaded: it has no version tag to report, and asking for one must not
// dereference the missing model.
func TestDefMetaVersionTagWithoutAModel(t *testing.T) {
	if got := (defMeta{ProcessID: "p"}).VersionTag(); got != "" {
		t.Errorf("VersionTag with no compiled model = %q, want the empty string", got)
	}
}

// TestSearchInstancesDuringShutdown covers the off-loop read path's shutdown
// branch through a handler: with the loop gone there is no view to answer from, so
// the request must say the server is going away rather than return an empty result
// set that reads like "nothing matched".
func TestSearchInstancesDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()

	w := httptest.NewRecorder()
	srv.handleSearchInstances(w, httptest.NewRequest("GET", "/api/v1/instances/search?q=a=b", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// TestListAndTimelineDuringShutdown covers the other two handlers converted to the
// off-loop read path. Each takes its view through the loop, so once the loop is
// gone there is nothing to answer from — and an empty list or an empty timeline
// would read as a true answer about the instance rather than as "ask again".
func TestListAndTimelineDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()

	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		req     *http.Request
	}{
		{"list", srv.handleListInstances, httptest.NewRequest("GET", "/api/v1/instances", nil)},
		{"timeline", srv.handleInstanceTimeline, httptest.NewRequest("GET", "/api/v1/instances/1/timeline", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			if tc.name == "timeline" {
				req.SetPathValue("key", "1")
			}
			w := httptest.NewRecorder()
			tc.handler(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}
