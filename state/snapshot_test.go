package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestSnapshotIsReadableAsAStore: a snapshot is a real, openable state store holding
// the writes committed before it — the property an engine recovery checkpoint rests on
// (ADR-0131). Ordinary commits are NoSync, so this also pins that Snapshot's flush
// makes them durable in the snapshot's files rather than leaving them behind.
func TestSnapshotIsReadableAsAStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := model.NewKey(1, 1)
	tx := s.NewTransaction()
	if err := tx.SetLastAppliedPosition(77); err != nil {
		t.Fatalf("SetLastAppliedPosition: %v", err)
	}
	if err := tx.PutProcessInstance(key, &model.ProcessInstanceValue{ProcessDefKey: 9, State: model.PIActive}); err != nil {
		t.Fatalf("PutProcessInstance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close tx: %v", err)
	}

	snapDir := filepath.Join(dir, "snapshot")
	if err := s.Snapshot(snapDir); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// The snapshot opens as a store and carries the committed state.
	restored, err := Open(snapDir)
	if err != nil {
		t.Fatalf("Open snapshot: %v", err)
	}
	defer restored.Close()
	pos, err := restored.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if pos != 77 {
		t.Fatalf("snapshot applied position = %d, want 77", pos)
	}
	pi, ok, err := restored.ProcessInstance(key)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance in snapshot: ok=%v err=%v", ok, err)
	}
	if pi.ProcessDefKey != 9 || pi.State != model.PIActive {
		t.Fatalf("snapshot instance = %+v, want def 9 active", pi)
	}

	// Writes made after the snapshot are not in it — a snapshot is a point in time.
	tx2 := s.NewTransaction()
	if err := tx2.SetLastAppliedPosition(99); err != nil {
		t.Fatalf("SetLastAppliedPosition 2: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit 2: %v", err)
	}
	if err := tx2.Close(); err != nil {
		t.Fatalf("Close tx2: %v", err)
	}
	if pos, err = restored.LastAppliedPosition(); err != nil || pos != 77 {
		t.Fatalf("snapshot position after a later live write = %d (err %v), want a stable 77", pos, err)
	}
}

// TestSnapshotRejectsExistingDestination: the destination must be fresh, so a snapshot
// can never silently merge into or overwrite an existing directory.
func TestSnapshotRejectsExistingDestination(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	occupied := filepath.Join(dir, "taken")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := s.Snapshot(occupied); err == nil {
		t.Fatal("Snapshot into an existing directory succeeded, want an error")
	}
}
