package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These tests cover ADR-0131 slice 4: deleting the WAL segments a durable checkpoint and
// every consumer watermark make redundant. Deletion is irreversible, so the assertions
// are mostly about what is *refused* — and the one that matters most is that after a
// compaction a restore still reproduces the live state exactly.

// compactWith reopens the log and store under dir and runs a compaction against root
// with the given consumer limits, returning how many segments went.
func compactWith(t *testing.T, dir, storeDir, root string, cp *compiler.CompiledProcess, limits []uint64) int {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(storeDir)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	removed, err := p.CompactLog(root, limits)
	if err != nil {
		t.Fatalf("CompactLog: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Errorf("log.Close: %v", err)
	}
	return removed
}

func walSegments(t *testing.T, dir string) int {
	t.Helper()
	segs, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return len(segs)
}

// TestCompactLogThenRestoreEqualsLive is the end-to-end proof: after compaction has
// deleted the covered segments, restoring the checkpoint and replaying what remains
// still reproduces exactly the state the live run produced.
func TestCompactLogThenRestoreEqualsLive(t *testing.T) {
	dir, root, cp, at, live := checkpointedRun(t)
	before := walSegments(t, dir)

	removed := compactWith(t, dir, filepath.Join(dir, "state"), root, cp, nil)
	if removed == 0 {
		t.Fatal("compaction removed nothing; the fixture proves nothing")
	}
	if got := walSegments(t, dir); got != before-removed {
		t.Fatalf("segments left = %d, want %d", got, before-removed)
	}

	restored := filepath.Join(t.TempDir(), "state-from-checkpoint")
	copyTree(t, filepath.Join(root, checkpoint.DirName(at)), restored)
	assertSnapshotEqual(t, recoverInto(t, dir, restored, root, cp), live, "restore after compaction")
}

// TestCompactLogWithoutCheckpointDeletesNothing: with no checkpoint to license it, the
// log is the only recovery source and must stay whole.
func TestCompactLogWithoutCheckpointDeletesNothing(t *testing.T) {
	dir, _, cp, _, _ := checkpointedRun(t)
	before := walSegments(t, dir)

	if removed := compactWith(t, dir, filepath.Join(dir, "state"), "", cp, nil); removed != 0 {
		t.Fatalf("compaction removed %d segments with no checkpoint root, want 0", removed)
	}
	if got := walSegments(t, dir); got != before {
		t.Fatalf("segments = %d, want the log untouched at %d", got, before)
	}
}

// TestCompactLogRefusesCorruptCheckpoint: once the prefix is deleted the checkpoint's
// state files are the only way to rebuild it, so a snapshot that fails its checksum
// licenses no deletion at all. This is the check RecoverFrom deliberately skips and
// compaction deliberately makes.
func TestCompactLogRefusesCorruptCheckpoint(t *testing.T) {
	dir, root, cp, at, _ := checkpointedRun(t)
	before := walSegments(t, dir)

	// Corrupt a state file inside the published checkpoint — the manifest still decodes.
	entries, err := os.ReadDir(filepath.Join(root, checkpoint.DirName(at)))
	if err != nil {
		t.Fatalf("ReadDir checkpoint: %v", err)
	}
	corrupted := false
	for _, e := range entries {
		if e.IsDir() || e.Name() == checkpoint.ManifestName {
			continue
		}
		path := filepath.Join(root, checkpoint.DirName(at), e.Name())
		if err := os.WriteFile(path, []byte("corrupted"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", e.Name(), err)
		}
		corrupted = true
		break
	}
	if !corrupted {
		t.Fatal("checkpoint held no state file to corrupt")
	}

	if removed := compactWith(t, dir, filepath.Join(dir, "state"), root, cp, nil); removed != 0 {
		t.Fatalf("compaction removed %d segments on a corrupt checkpoint, want 0", removed)
	}
	if got := walSegments(t, dir); got != before {
		t.Fatalf("segments = %d, want the log untouched at %d", got, before)
	}
}

// TestCompactLogRefusesCheckpointAheadOfStore: recovery refuses a checkpoint ahead of
// the store (it skips reading a prefix, it does not install state files), so deleting
// below such a checkpoint would strand recovery with a gap it can no longer replay.
//
// A running engine has always recovered, which pulls its store up to the log, so this
// is the defensive case — a store lost or rolled back behind its checkpoint. The test
// constructs it directly by compacting *without* recovering first; recovering would
// itself close the gap and prove nothing.
func TestCompactLogRefusesCheckpointAheadOfStore(t *testing.T) {
	dir, root, cp, _, _ := checkpointedRun(t)
	before := walSegments(t, dir)

	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(t.TempDir(), "state-empty")) // applied position 0
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)

	removed, err := p.CompactLog(root, nil)
	if err != nil {
		t.Fatalf("CompactLog: %v", err)
	}
	if removed != 0 {
		t.Fatalf("compaction removed %d segments while the store trailed its checkpoint, want 0", removed)
	}
	if got := walSegments(t, dir); got != before {
		t.Fatalf("segments = %d, want the log untouched at %d", got, before)
	}
}

// TestCompactLogHonorsConsumerWatermarks: a consumer that has only read up to an early
// position holds the cut down to its watermark, so records it has not seen survive.
func TestCompactLogHonorsConsumerWatermarks(t *testing.T) {
	dir, root, cp, _, live := checkpointedRun(t)
	before := walSegments(t, dir)

	// A consumer stuck at the very beginning permits nothing beyond position 1.
	if removed := compactWith(t, dir, filepath.Join(dir, "state"), root, cp, []uint64{1}); removed != 0 {
		t.Fatalf("compaction removed %d segments despite a watermark at 1, want 0", removed)
	}
	if got := walSegments(t, dir); got != before {
		t.Fatalf("segments = %d, want the log untouched at %d", got, before)
	}

	// A watermark at or past the checkpoint no longer constrains it.
	if removed := compactWith(t, dir, filepath.Join(dir, "state"), root, cp, []uint64{1 << 30}); removed == 0 {
		t.Fatal("compaction removed nothing with a watermark well past the checkpoint")
	}
	// And the surviving log still recovers to the live state.
	assertSnapshotEqual(t, recoverInto(t, dir, filepath.Join(dir, "state"), root, cp), live,
		"recovery after watermark-limited compaction")
}

// TestCompactLogUnusableRootIsReported: a checkpoint root that is not a directory (an
// operator pointing at a file) is a real error, and nothing is deleted on it.
func TestCompactLogUnusableRootIsReported(t *testing.T) {
	dir, _, cp, _, _ := checkpointedRun(t)
	before := walSegments(t, dir)

	notADir := filepath.Join(t.TempDir(), "checkpoints")
	if err := os.WriteFile(notADir, []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	removed, err := p.CompactLog(notADir, nil)
	if err == nil {
		t.Fatal("CompactLog accepted a checkpoint root that is not a directory")
	}
	if removed != 0 || walSegments(t, dir) != before {
		t.Fatalf("a failed compaction deleted segments (removed=%d)", removed)
	}
}
