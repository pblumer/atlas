package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestRecoveryRefusesAContinuationItCannotRead: the continuation is the work the
// engine still owed when it stopped (ADR-0271), and recovery
// re-runs it. A continuation that will not decode is therefore not an empty queue —
// it is an unknown one, and starting with an unknown queue is starting an engine
// that has quietly dropped work nobody can name.
//
// So it fails, loudly, the same way a log whose prefix is missing does. The test
// writes a continuation that claims one command and carries none, which is what a
// truncated or foreign frame looks like from the decoder's side.
func TestRecoveryRefusesAContinuationItCannotRead(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)

	cp, _ := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// One more continuation, after the engine's own: a count of one and no command.
	if err := h.log.AppendContinuation([]byte{1, 0, 0, 0}); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := h.log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	h.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log2.Close()
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store2.Close()

	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	err = p2.Recover()
	if err == nil {
		t.Fatal("recovery accepted a continuation it could not decode")
	}
	if !strings.Contains(err.Error(), "continuation") {
		t.Errorf("Recover error = %q, want it to name the continuation", err)
	}
}
