package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// A user task says when it was opened. The inbox shows it on every row, and it is
// a fact about the job's creation event: its header timestamp, stamped by
// applyToState and carried by every later re-put of the job, so a claim does not
// reset it and a replay of the log rebuilds the same instant (I4/I6).
func TestAJobRemembersWhenItWasOpened(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)

	cp, jobType, _ := assignmentProcess(t, "", "")
	p := engine.New(1, h.log, h.store, &manualClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)
	jv, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	opened := jv.CreatedAt
	if opened <= 1_000 {
		t.Fatalf("CreatedAt = %d, want the creation event's timestamp (after the clock's start)", opened)
	}

	// Claiming re-puts the job; the time it was opened is not the time it was claimed.
	p.AssignJob(jobKey, "alice")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jv, _, _ = h.store.GetJob(jobKey)
	if jv.Assignee != "alice" {
		t.Fatalf("assignee = %q, want alice", jv.Assignee)
	}
	if jv.CreatedAt != opened {
		t.Errorf("CreatedAt after a claim = %d, want %d — the re-put reset it", jv.CreatedAt, opened)
	}
	if err := h.store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Rebuilt from the log alone, into an empty store: the same instant.
	fresh, err := state.Open(filepath.Join(dir, "state-replayed"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		_ = fresh.Close()
		_ = h.log.Close()
	}()
	q := engine.New(1, h.log, fresh, &manualClock{t: 9_000_000})
	q.Deploy(cp)
	if err := q.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	jv, ok, err = fresh.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob after replay: ok=%v err=%v", ok, err)
	}
	if jv.CreatedAt != opened {
		t.Errorf("CreatedAt after replay = %d, want %d", jv.CreatedAt, opened)
	}
}
