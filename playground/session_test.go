package playground_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

func newRegistry(t *testing.T) *playground.Registry {
	t.Helper()
	r := playground.NewRegistry(time.Hour, 4)
	t.Cleanup(r.CloseAll)
	return r
}

func openSession(t *testing.T, r *playground.Registry, fixtureName string) *playground.Session {
	t.Helper()
	s, err := r.Open(playground.Options{
		ModelXML: fixture(t, fixtureName), BaseDir: t.TempDir(),
		StartTime: simStart, Stubs: playground.DefaultStubs(),
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	return s
}

// A session hands its sandbox out only on its own goroutine, so concurrent
// callers cannot touch one partition at the same time (invariant I3).
func TestSessionSerializesConcurrentCallers(t *testing.T) {
	r := newRegistry(t)
	s := openSession(t, r, "two-tasks.bpmn")

	var wg sync.WaitGroup
	inside := 0
	peak := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.With(func(sb *playground.Sandbox) error {
				inside++
				if inside > peak {
					peak = inside
				}
				_, err := sb.StartCase()
				inside--
				return err
			})
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Errorf("%d callers were inside the sandbox at once; the session must serialize them", peak)
	}
}

// Work handed to a closed session is refused rather than silently dropped.
func TestClosedSessionRefusesWork(t *testing.T) {
	r := newRegistry(t)
	s := openSession(t, r, "sequence.bpmn")
	if err := r.Close(s.ID()); err != nil {
		t.Fatalf("close: %v", err)
	}

	ran := false
	err := s.With(func(*playground.Sandbox) error { ran = true; return nil })
	if err == nil {
		t.Error("a closed session accepted work")
	}
	if ran {
		t.Error("the closure ran on a closed session")
	}
	if _, ok := r.Get(s.ID()); ok {
		t.Error("a closed session is still in the registry")
	}
	// Closing twice is what a TTL sweep racing an explicit close does.
	if err := r.Close(s.ID()); err == nil {
		t.Error("closing an unknown session should be reported")
	}
}

// Pause is asked for from outside the goroutine that is running, and stops the
// run at the next occurrence rather than mid-flight.
func TestPauseStopsARunAndResumeLetsItFinish(t *testing.T) {
	r := newRegistry(t)
	s := openSession(t, r, "two-tasks.bpmn")

	var caseKey uint64
	if err := s.With(func(sb *playground.Sandbox) error {
		k, err := sb.StartCase()
		caseKey = k
		return err
	}); err != nil {
		t.Fatalf("start case: %v", err)
	}

	s.Pause()
	if !s.Paused() {
		t.Error("session does not report itself paused")
	}
	var prog playground.Progress
	if err := s.With(func(sb *playground.Sandbox) error {
		p, err := sb.Run(s.Budget(playground.DefaultBudget()))
		prog = p
		return err
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if prog.Occurrences != 0 || prog.Quiescent {
		t.Errorf("progress = %+v while paused, want no occurrences and no quiescence", prog)
	}

	s.Resume()
	if s.Paused() {
		t.Error("session still reports itself paused after Resume")
	}
	if err := s.With(func(sb *playground.Sandbox) error {
		p, err := sb.Run(s.Budget(playground.DefaultBudget()))
		prog = p
		return err
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !prog.Quiescent {
		t.Errorf("progress = %+v after Resume, want the run to finish", prog)
	}

	var state model.ProcessInstanceState
	if err := s.With(func(sb *playground.Sandbox) error {
		c, err := sb.Case(caseKey)
		state = c.State
		return err
	}); err != nil {
		t.Fatalf("case: %v", err)
	}
	if state != model.PICompleted {
		t.Errorf("state = %v, want completed", state)
	}
}

// An abandoned session is reclaimed: a sandbox is a live engine and a temp
// directory, and nobody is coming back for it.
func TestRegistryReapsIdleSessions(t *testing.T) {
	r := playground.NewRegistry(time.Minute, 4)
	t.Cleanup(r.CloseAll)
	s, err := r.Open(playground.Options{
		ModelXML: fixture(t, "sequence.bpmn"), BaseDir: t.TempDir(), StartTime: simStart,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	dir := ""
	if err := s.With(func(sb *playground.Sandbox) error { dir = sb.Dir(); return nil }); err != nil {
		t.Fatalf("with: %v", err)
	}

	if n := r.Reap(time.Now()); n != 0 {
		t.Errorf("reaped %d sessions that are still in use", n)
	}
	if n := r.Reap(time.Now().Add(2 * time.Minute)); n != 1 {
		t.Errorf("reaped %d sessions, want 1 after the TTL elapsed", n)
	}
	if _, ok := r.Get(s.ID()); ok {
		t.Error("a reaped session is still in the registry")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a reaped session left %s behind (err=%v)", dir, err)
	}
}

// Using a session keeps it alive; the TTL runs from the last use, not from the
// start.
func TestUsingASessionKeepsItAlive(t *testing.T) {
	r := playground.NewRegistry(time.Minute, 4)
	t.Cleanup(r.CloseAll)
	s := openSession(t, r, "sequence.bpmn")

	if err := s.With(func(*playground.Sandbox) error { return nil }); err != nil {
		t.Fatalf("with: %v", err)
	}
	if n := r.Reap(s.LastUsed().Add(30 * time.Second)); n != 0 {
		t.Error("a session used 30 seconds ago was reaped under a one-minute TTL")
	}
	if _, ok := r.Get(s.ID()); !ok {
		t.Error("the session went missing")
	}
}

// The cap is the resource bound: a server cannot be talked into holding an
// unbounded number of live engines.
func TestRegistryRefusesBeyondItsCap(t *testing.T) {
	r := playground.NewRegistry(time.Hour, 2)
	t.Cleanup(r.CloseAll)
	for i := 0; i < 2; i++ {
		if _, err := r.Open(playground.Options{
			ModelXML: fixture(t, "sequence.bpmn"), BaseDir: t.TempDir(),
		}); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}
	_, err := r.Open(playground.Options{ModelXML: fixture(t, "sequence.bpmn"), BaseDir: t.TempDir()})
	if err == nil {
		t.Fatal("the registry opened a third session over a cap of two")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("error %q should say what the limit was", err)
	}
}

// A model the compiler refuses never becomes a session.
func TestRegistryDoesNotKeepAFailedOpen(t *testing.T) {
	r := newRegistry(t)
	if _, err := r.Open(playground.Options{ModelXML: []byte("nonsense"), BaseDir: t.TempDir()}); err == nil {
		t.Fatal("a model that does not compile should not open a session")
	}
	if n := r.Len(); n != 0 {
		t.Errorf("registry holds %d sessions after a failed open, want 0", n)
	}
}
