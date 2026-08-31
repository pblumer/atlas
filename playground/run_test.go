package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/playground"
)

// waitFor polls until pred holds or the deadline passes, so a test never sleeps
// on a fixed guess about how fast a batch is.
func waitFor(t *testing.T, what string, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func batchSession(t *testing.T, r *playground.Registry, stubs playground.StubSet) *playground.Session {
	t.Helper()
	s, err := r.Open("", playground.Options{
		ModelXML: fixture(t, "user-task.bpmn"), BaseDir: t.TempDir(),
		StartTime: simStart, Seed: 7, Stubs: stubs,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	return s
}

// A batch runs behind the request that started it, and its progress is readable
// while it does — the whole reason a run is a session rather than a call.
func TestABatchRunsInTheBackgroundAndReportsProgress(t *testing.T) {
	r := newRegistry(t)
	sess := batchSession(t, r, playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: 10 * time.Minute},
	})

	if err := sess.StartRun(playground.Plan{Cases: rows(200)}); err != nil {
		t.Fatalf("start run: %v", err)
	}
	// Asking for progress must not have to wait for the batch to finish.
	if st := sess.RunStatus(); st.State != playground.RunRunning && st.State != playground.RunFinished {
		t.Errorf("state right after starting = %q", st.State)
	}
	waitFor(t, "the batch to finish", func() bool { return sess.RunStatus().State == playground.RunFinished })

	st := sess.RunStatus()
	if st.Cases != 200 || st.Completed != 200 {
		t.Errorf("status = %+v, want 200 cases all completed", st)
	}
	if st.Occurrences == 0 {
		t.Error("a finished batch reports no occurrences")
	}
	if st.Err != "" {
		t.Errorf("finished with an error: %s", st.Err)
	}
}

// A run in flight can be stopped, and what it did up to that point stays
// readable: the point of stopping is to look.
func TestABatchCanBeCancelledAndStillRead(t *testing.T) {
	r := newRegistry(t)
	sess := batchSession(t, r, playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	if err := sess.StartRun(playground.Plan{
		Cases:   rows(5000),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: time.Minute},
	}); err != nil {
		t.Fatalf("start run: %v", err)
	}
	sess.Cancel()
	waitFor(t, "the batch to stop", func() bool {
		st := sess.RunStatus().State
		return st == playground.RunCancelled || st == playground.RunFinished
	})

	var rep playground.Report
	if err := sess.With(func(sb *playground.Sandbox) error {
		var e error
		rep, e = sb.Report()
		return e
	}); err != nil {
		t.Fatalf("report after cancelling: %v", err)
	}
	if rep.Cases > 5000 {
		t.Errorf("report counts %d cases, more than the plan had", rep.Cases)
	}
}

// Two batches at once in one sandbox would interleave two datasets in one
// report, so the second is refused rather than silently merged.
func TestASecondRunIsRefusedWhileOneIsInFlight(t *testing.T) {
	r := newRegistry(t)
	sess := batchSession(t, r, playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	if err := sess.StartRun(playground.Plan{
		Cases:   rows(3000),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: time.Minute},
	}); err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer sess.Cancel()
	if err := sess.StartRun(playground.Plan{Cases: rows(1)}); err == nil {
		t.Error("a second batch should be refused while one is running")
	}
}

// A batch that cannot run says so in its status rather than stopping silently:
// the caller is not watching the goroutine, it is watching the status.
func TestAFailingBatchReportsWhy(t *testing.T) {
	r := newRegistry(t)
	sess := batchSession(t, r, playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	if err := sess.StartRun(playground.Plan{Cases: nil}); err == nil {
		t.Fatal("an empty plan should be refused before anything starts")
	}
	if st := sess.RunStatus(); st.State != playground.RunIdle {
		t.Errorf("state after a refused plan = %q, want idle", st.State)
	}
}

// Closing a session while a batch runs stops it rather than leaving a goroutine
// driving an engine nobody is reading.
func TestClosingASessionStopsItsBatch(t *testing.T) {
	r := newRegistry(t)
	sess := batchSession(t, r, playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	if err := sess.StartRun(playground.Plan{
		Cases:   rows(5000),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: time.Minute},
	}); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := r.Close(sess.ID()); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The driver is gone: the session refuses work and the status stops moving.
	if err := sess.With(func(*playground.Sandbox) error { return nil }); !playground.ErrClosedSession(err) {
		t.Errorf("a closed session accepted work: %v", err)
	}
}
