package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// These tests cover the engine half of ADR-0131 slice 2: producing a recovery
// checkpoint on the single-writer goroutine at a batch boundary. Restoring from one,
// and deleting the WAL segments it makes redundant, are the later slices — so every
// assertion here is about what was *written*, and about the engine being undisturbed
// by having written it.

// secondProcess builds a second, distinct definition (its own key and version) so the
// manifest's deployment set has more than one entry to order.
func secondProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey+1, "second", 2)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build second process: %v", err)
	}
	return cp
}

// TestCheckpointRecordsAppliedState: a checkpoint taken after a workload records the
// store's applied position, the processor's highest position and key counter, the
// partition, and the deployed definitions — and its state snapshot verifies.
func TestCheckpointRecordsAppliedState(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	h := openHarness(t, dir)
	defer h.close(t)

	cp, _ := linearProcess(t)
	second := secondProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	p.Deploy(second)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	applied, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	wantApplied, err := h.store.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if applied != wantApplied || applied == 0 {
		t.Fatalf("checkpoint applied position = %d, want the store's %d (non-zero)", applied, wantApplied)
	}

	positions, err := checkpoint.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 1 || positions[0] != applied {
		t.Fatalf("List = %v, want [%d]", positions, applied)
	}

	m, err := checkpoint.Verify(root, applied)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m.Partition != 1 {
		t.Errorf("manifest partition = %d, want 1", m.Partition)
	}
	if m.AppliedPosition != applied {
		t.Errorf("manifest applied = %d, want %d", m.AppliedPosition, applied)
	}
	if m.HighestPosition < m.AppliedPosition {
		t.Errorf("manifest highest %d < applied %d", m.HighestPosition, m.AppliedPosition)
	}
	// The key counter must be past the keys this workload minted, so a restored engine
	// cannot re-issue them.
	if m.KeyCounter == 0 {
		t.Error("manifest key counter is zero after a workload that minted keys")
	}
	// Both definitions are recorded, ordered by key so identical state yields an
	// identical manifest.
	if len(m.Deployments) != 2 {
		t.Fatalf("manifest deployments = %+v, want both deployed definitions", m.Deployments)
	}
	if m.Deployments[0].Key >= m.Deployments[1].Key {
		t.Errorf("deployments not ordered by key: %+v", m.Deployments)
	}
	byKey := map[uint64]int32{m.Deployments[0].Key: m.Deployments[0].Version, m.Deployments[1].Key: m.Deployments[1].Version}
	if byKey[cp.Key] != cp.Version || byKey[second.Key] != second.Version {
		t.Errorf("deployments = %+v, want %d v%d and %d v%d", m.Deployments, cp.Key, cp.Version, second.Key, second.Version)
	}

	// The snapshot is a real state store, not an empty directory.
	entries, err := os.ReadDir(filepath.Join(root, checkpoint.DirName(applied)))
	if err != nil {
		t.Fatalf("ReadDir checkpoint: %v", err)
	}
	if len(entries) < 2 { // at least the manifest plus Pebble's own files
		t.Fatalf("checkpoint directory holds %d entries, want a state snapshot plus the manifest", len(entries))
	}
}

// TestCheckpointLeavesEngineRunnable: taking a checkpoint is an optimization, not a
// state transition — the engine keeps processing afterwards, and a later checkpoint
// captures the newer position.
func TestCheckpointLeavesEngineRunnable(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	h := openHarness(t, dir)
	defer h.close(t)

	cp, _ := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	first, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint 1: %v", err)
	}

	// Keep working after the checkpoint: a second instance must run normally.
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2 (after checkpoint): %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 2 {
		t.Fatalf("active instances after the post-checkpoint run = %d, want 2", pi)
	}

	second, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint 2: %v", err)
	}
	if second <= first {
		t.Fatalf("second checkpoint position %d, want > first %d", second, first)
	}
	positions, err := checkpoint.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 2 || positions[0] != first || positions[1] != second {
		t.Fatalf("List = %v, want [%d %d] oldest-first", positions, first, second)
	}
	if _, err := checkpoint.Verify(root, second); err != nil {
		t.Fatalf("Verify newest: %v", err)
	}
}

// TestCheckpointReplayStillEqualsLive: a checkpointed engine's WAL still replays into
// exactly the live state. This slice only *writes* checkpoints — recovery must be
// completely unaffected by their existence (restore arrives in the next slice).
func TestCheckpointReplayStillEqualsLive(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	h := openHarness(t, dir)

	cp, _ := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if _, err := p.Checkpoint(root); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	// More work lands after the checkpoint, so the log carries a suffix past it.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "2"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	live := snapshot(t, h.store)
	h.close(t)

	sc := scenario{dir: dir, cp: cp}
	assertSnapshotEqual(t, recoverFrom(t, sc), live, "replay with a checkpoint present")
}

// TestCheckpointOnEmptyEngine: checkpointing an engine that has applied nothing is
// valid and records position zero — there is simply no suffix to skip yet.
func TestCheckpointOnEmptyEngine(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	h := openHarness(t, dir)
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	applied, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied position = %d, want 0 on an empty engine", applied)
	}
	m, err := checkpoint.Verify(root, 0)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(m.Deployments) != 0 {
		t.Errorf("deployments = %+v, want none on an engine with nothing deployed", m.Deployments)
	}
}

// TestCheckpointRecordsBuildVersion: the diagnostics-only build stamp round-trips into
// the manifest when the server sets it.
func TestCheckpointRecordsBuildVersion(t *testing.T) {
	prev := engine.BuildVersion
	engine.BuildVersion = "0.2.0-test-build"
	defer func() { engine.BuildVersion = prev }()

	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	h := openHarness(t, dir)
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	applied, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	m, err := checkpoint.Load(root, applied)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.AtlasVersion != "0.2.0-test-build" {
		t.Fatalf("manifest atlas version = %q, want the build stamp", m.AtlasVersion)
	}
}

// TestCheckpointErrorsAreNotFatal: a checkpoint that cannot be written (its root path
// is a file) reports the error without disturbing the engine, which keeps running —
// a failed checkpoint costs a slower recovery, never correctness (invariant I2).
func TestCheckpointErrorsAreNotFatal(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	if err := os.WriteFile(root, []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	h := openHarness(t, dir)
	defer h.close(t)

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
	before := snapshot(t, h.store)

	if _, err := p.Checkpoint(root); err == nil {
		t.Fatal("Checkpoint succeeded despite an unusable root")
	}
	// The engine is untouched and still runs.
	assertSnapshotEqual(t, snapshot(t, h.store), before, "state after a failed checkpoint")
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after a failed checkpoint: %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 2 {
		t.Fatalf("active instances = %d, want 2 — the engine kept running", pi)
	}
}
