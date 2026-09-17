package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// These tests cover the split ADR-0131 needed once a store grew: the snapshot stays on
// the single-writer goroutine, and the whole-state read that follows it does not.

// TestStageCheckpointPublishesOnlyOnCommit: staging captures the applied position and
// writes nothing a reader can find; committing publishes exactly that position.
func TestStageCheckpointPublishesOnlyOnCommit(t *testing.T) {
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
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	staged, err := p.StageCheckpoint(root)
	if err != nil {
		t.Fatalf("StageCheckpoint: %v", err)
	}
	wantApplied, err := h.store.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if staged.AppliedPosition() != wantApplied || wantApplied == 0 {
		t.Fatalf("staged position = %d, want the store's %d (non-zero)", staged.AppliedPosition(), wantApplied)
	}
	if positions, err := checkpoint.List(root); err != nil || len(positions) != 0 {
		t.Fatalf("List after Stage = %v (err %v), want nothing published yet", positions, err)
	}

	if _, err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	positions, err := checkpoint.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 1 || positions[0] != wantApplied {
		t.Fatalf("List = %v, want [%d]", positions, wantApplied)
	}
	m, err := checkpoint.Verify(root, wantApplied)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m.Partition != 1 || m.AppliedPosition != wantApplied {
		t.Fatalf("manifest = %+v, want partition 1 at %d", m, wantApplied)
	}
}

// TestStageCheckpointKeepsProcessingCorrectAcrossTheCommit: the engine runs on while a
// staged checkpoint is uncommitted — which is the point of staging — and the published
// checkpoint still describes the state as of the stage, not as of the commit.
func TestStageCheckpointKeepsProcessingCorrectAcrossTheCommit(t *testing.T) {
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
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	staged, err := p.StageCheckpoint(root)
	if err != nil {
		t.Fatalf("StageCheckpoint: %v", err)
	}
	atStage := staged.AppliedPosition()

	// The writer carries on between Stage and Commit, exactly as the server now lets it.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "2"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after staging: %v", err)
	}
	after, err := h.store.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if after <= atStage {
		t.Fatalf("the engine did not advance across the commit window: %d then %d", atStage, after)
	}

	if _, err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// The checkpoint is published at the staged position and its checksum still
	// describes the snapshot, not the state the writer has since produced.
	if _, err := checkpoint.Verify(root, atStage); err != nil {
		t.Fatalf("Verify at the staged position: %v", err)
	}
}

// TestStageCheckpointAbandonLeavesNoTempDirectory: a pass that stages and then gives up
// must not leave a temp directory for the next one.
func TestStageCheckpointAbandonLeavesNoTempDirectory(t *testing.T) {
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
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	staged, err := p.StageCheckpoint(root)
	if err != nil {
		t.Fatalf("StageCheckpoint: %v", err)
	}
	if err := staged.Abandon(); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("checkpoint root holds %d entries after Abandon, want none", len(entries))
	}
}

// TestCompactionCutSeparatesVerificationFromDeletion: resolving the cut is the half
// that reads every byte of a checkpoint (checkpoint.Verify), and it is computable
// without the writer; only the deletion needs it. Both halves together must agree with
// what the single-call CompactLog does.
func TestCompactionCutSeparatesVerificationFromDeletion(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	applied, err := p.Checkpoint(root)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	lastApplied, err := h.store.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	cut, err := p.CompactionCut(root, lastApplied, nil)
	if err != nil {
		t.Fatalf("CompactionCut: %v", err)
	}
	if cut != applied {
		t.Fatalf("cut = %d, want the verified checkpoint's applied position %d", cut, applied)
	}

	// A consumer watermark below the checkpoint holds the cut down to it.
	held, err := p.CompactionCut(root, lastApplied, []uint64{1})
	if err != nil {
		t.Fatalf("CompactionCut with a watermark: %v", err)
	}
	if held != 1 {
		t.Fatalf("cut with a watermark at 1 = %d, want 1", held)
	}

	// Deleting at a zero cut is the "nothing qualifies" case and must remove nothing.
	if n, err := p.CompactLogAt(0); err != nil || n != 0 {
		t.Fatalf("CompactLogAt(0) = %d, %v; want 0, nil", n, err)
	}
	if _, err := p.CompactLogAt(cut); err != nil {
		t.Fatalf("CompactLogAt: %v", err)
	}
}
