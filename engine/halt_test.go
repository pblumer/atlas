package engine_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestADurabilityFailureHaltsTheEngine: once a batch could not be made durable, the
// engine processes nothing more until it is restarted, and the restart recovers exactly
// what was acknowledged.
//
// Carrying on was the failure this replaces. A failed batch left its commands queued,
// so the next run — any request at all — executed them after their caller had been
// told they failed: on a disk that filled and was freed again, two instances whose
// creation had been refused were created anyway, and a client retrying would have had
// each of them twice. A restart is the one point at which what is durable and what is
// in memory are known to agree, so that is where the engine resumes.
func TestADurabilityFailureHaltsTheEngine(t *testing.T) {
	dir := t.TempDir()
	walDir, stateDir := filepath.Join(dir, "wal"), filepath.Join(dir, "state")
	log, err := wal.Open(wal.Options{Dir: walDir})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(stateDir)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p := engine.New(1, log, store, &manualClock{})
	rec := &recorder{}
	p.SetMetrics(rec)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	cp, _ := linearProcess(t)
	p.Deploy(cp)

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if err := p.Halted(); err != nil {
		t.Fatalf("Halted() = %v before anything failed", err)
	}

	// Another goroutine watches the halt while the writer trips it: the server's
	// storage monitor and its HTTP handlers read it from outside the loop, so the
	// race detector has to see that read as safe.
	seen := make(chan error, 1)
	go func() {
		for {
			if err := p.Halted(); err != nil {
				seen <- err
				return
			}
		}
	}()

	// The disk stops taking writes: Append still buffers, Sync fails.
	if err := log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
	p.CreateInstance(cp.Key)
	err = p.RunUntilIdle()
	if !errors.Is(err, engine.ErrHalted) {
		t.Fatalf("RunUntilIdle after a failed fsync = %v, want ErrHalted", err)
	}
	if err := <-seen; !errors.Is(err, engine.ErrHalted) {
		t.Fatalf("Halted() = %v, want ErrHalted", err)
	}
	attempts := rec.syncFailures
	if attempts != 1 {
		t.Fatalf("sync failures = %d, want 1", attempts)
	}

	// Refused without touching the disk again: the queue still holds a command whose
	// caller was told it failed, and running it now would contradict that answer.
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); !errors.Is(err, engine.ErrHalted) {
		t.Fatalf("RunUntilIdle on a halted engine = %v, want ErrHalted", err)
	}
	if rec.syncFailures != attempts {
		t.Fatalf("a halted engine tried the disk again (%d sync failures, want %d)", rec.syncFailures, attempts)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// The restart recovers the one acknowledged instance and none of the refused ones.
	log2, err := wal.Open(wal.Options{Dir: walDir})
	if err != nil {
		t.Fatalf("restart wal.Open: %v", err)
	}
	// Closed before the temp dir is removed: Windows will not delete an open file.
	defer func() {
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	store2, err := state.Open(stateDir)
	if err != nil {
		t.Fatalf("restart state.Open: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("restart Recover: %v", err)
	}
	if err := p2.Halted(); err != nil {
		t.Fatalf("a restarted engine is still halted: %v", err)
	}
	n, err := store2.TotalActiveInstances()
	if err != nil {
		t.Fatalf("TotalActiveInstances: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered %d active instances, want the 1 that was acknowledged", n)
	}
	p2.CreateInstance(cp.Key)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("a restarted engine refused work: %v", err)
	}
}
