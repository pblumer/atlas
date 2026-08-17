package engine_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These tests cover ADR-0131 slice 3: recovering from the newest usable checkpoint and
// replaying only the WAL suffix past it, with a fallback to an older checkpoint or to
// genesis whenever a checkpoint cannot be trusted. The property that matters is that
// the shortcut never changes the outcome — recovered state must equal the state the
// live run produced, exactly as a genesis replay would give.

// copyTree copies a directory recursively, so a test can open a published checkpoint's
// state snapshot as a store without mutating the checkpoint itself.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}); err != nil {
		t.Fatalf("copyTree %s -> %s: %v", src, dst, err)
	}
}

// checkpointedRun builds an engine whose WAL rolls often (so the prefix a checkpoint
// covers occupies whole segments), runs a workload, takes a checkpoint, then runs more
// work. It returns the data dir, the checkpoint root, the definition, the checkpoint's
// position, and a snapshot of the final live state.
func checkpointedRun(t *testing.T) (dir, root string, cp *compiler.CompiledProcess, at uint64, live stateSnapshot) {
	t.Helper()
	dir = t.TempDir()
	root = filepath.Join(dir, "checkpoints")
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1}) // roll every batch
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	cp, _ = linearProcess(t)
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Work before the checkpoint.
	for _, n := range []string{"1", "2"} {
		p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: n})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle pre-checkpoint: %v", err)
		}
	}
	if at, err = p.Checkpoint(root); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	// Work after it — this is the suffix a restore must replay.
	for _, n := range []string{"3", "4"} {
		p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: n})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle post-checkpoint: %v", err)
		}
	}
	live = snapshot(t, store)

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
	return dir, root, cp, at, live
}

// recoverInto opens the WAL under dir together with the store at storeDir and recovers
// it using the checkpoint root, returning a snapshot of the rebuilt state.
func recoverInto(t *testing.T, dir, storeDir, root string, cp *compiler.CompiledProcess) stateSnapshot {
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
	if err := p.RecoverFrom(root); err != nil {
		t.Fatalf("RecoverFrom(%q): %v", root, err)
	}
	snap := snapshot(t, store)
	if err := store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Errorf("log.Close: %v", err)
	}
	return snap
}

// TestRecoverFromCheckpointReplaysSuffixToLiveState is the headline: starting from the
// checkpoint's own state snapshot, recovery replays only the WAL past it and lands on
// exactly the state the live run produced.
func TestRecoverFromCheckpointReplaysSuffixToLiveState(t *testing.T) {
	dir, root, cp, at, live := checkpointedRun(t)

	restored := filepath.Join(t.TempDir(), "state-from-checkpoint")
	copyTree(t, filepath.Join(root, checkpoint.DirName(at)), restored)

	assertSnapshotEqual(t, recoverInto(t, dir, restored, root, cp), live, "checkpoint + suffix replay")
}

// TestRecoverFromCheckpointNeedsNoPrefixSegments: with the checkpoint restored, the WAL
// segments it covers are not read at all — deleting them changes nothing. This is the
// premise WAL compaction rests on (ADR-0131 slice 4).
func TestRecoverFromCheckpointNeedsNoPrefixSegments(t *testing.T) {
	dir, root, cp, at, live := checkpointedRun(t)

	restored := filepath.Join(t.TempDir(), "state-from-checkpoint")
	copyTree(t, filepath.Join(root, checkpoint.DirName(at)), restored)

	// Delete every segment that lies entirely at or below the checkpoint.
	segs, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatalf("glob segments: %v", err)
	}
	removed := 0
	for _, seg := range segs {
		last, err := lastPositionIn(seg)
		if err != nil {
			t.Fatalf("scan %s: %v", seg, err)
		}
		if last != 0 && last <= at {
			if err := os.Remove(seg); err != nil {
				t.Fatalf("remove %s: %v", seg, err)
			}
			removed++
		}
	}
	if removed == 0 {
		t.Fatal("no segment lay entirely below the checkpoint; the fixture proves nothing")
	}

	assertSnapshotEqual(t, recoverInto(t, dir, restored, root, cp), live, "suffix replay with the prefix deleted")
}

// lastPositionIn reports the highest log position in a segment file, or 0 if it holds
// no readable record.
func lastPositionIn(path string) (uint64, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	// Frames are [4-byte length | 4-byte CRC | payload]; walk them and decode each
	// record's position, keeping the highest.
	var last uint64
	for off := 0; off+8 <= len(blob); {
		n := int(uint32(blob[off]) | uint32(blob[off+1])<<8 | uint32(blob[off+2])<<16 | uint32(blob[off+3])<<24)
		if n <= 0 || off+8+n > len(blob) {
			break
		}
		rec, err := model.ReadRecord(blob[off+8 : off+8+n])
		if err != nil {
			return 0, err
		}
		if rec.Header.Position > last {
			last = rec.Header.Position
		}
		off += 8 + n
	}
	return last, nil
}

// TestRecoverFromWithoutCheckpointMatchesGenesis: an empty root is plain recovery, so
// the shortcut is opt-in and its absence costs only speed.
func TestRecoverFromWithoutCheckpointMatchesGenesis(t *testing.T) {
	dir, _, cp, _, live := checkpointedRun(t)
	fresh := filepath.Join(t.TempDir(), "state-fresh")
	assertSnapshotEqual(t, recoverInto(t, dir, fresh, "", cp), live, "genesis replay into a fresh store")
}

// TestRecoverFromIgnoresUnusableCheckpoints: a checkpoint that cannot be trusted is
// skipped and recovery falls back to a full replay, still reaching the live state. A
// corrupt manifest, another partition's checkpoint, and one ahead of the store all
// qualify — the last because this slice skips reading a prefix rather than restoring
// state, so the store must already hold what the prefix produced.
func TestRecoverFromIgnoresUnusableCheckpoints(t *testing.T) {
	t.Run("corrupt manifest", func(t *testing.T) {
		dir, root, cp, at, live := checkpointedRun(t)
		path := filepath.Join(root, checkpoint.DirName(at), checkpoint.ManifestName)
		blob, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if err := os.WriteFile(path, blob[:len(blob)-4], 0o644); err != nil {
			t.Fatalf("truncate manifest: %v", err)
		}
		fresh := filepath.Join(t.TempDir(), "state-fresh")
		assertSnapshotEqual(t, recoverInto(t, dir, fresh, root, cp), live, "fallback past a corrupt checkpoint")
	})

	t.Run("ahead of the store", func(t *testing.T) {
		// A fresh, empty store has applied nothing, so the existing checkpoint is ahead
		// of it: using it would leave everything below it unapplied.
		dir, root, cp, _, live := checkpointedRun(t)
		fresh := filepath.Join(t.TempDir(), "state-fresh")
		assertSnapshotEqual(t, recoverInto(t, dir, fresh, root, cp), live, "fallback past a too-new checkpoint")
	})

	t.Run("another partition", func(t *testing.T) {
		dir, root, cp, at, live := checkpointedRun(t)
		// Publish a foreign-partition checkpoint at a position this engine could
		// otherwise skip past.
		foreign := &checkpoint.Manifest{
			Partition: 2, AppliedPosition: at, HighestPosition: at, KeyCounter: 1,
		}
		if err := os.RemoveAll(filepath.Join(root, checkpoint.DirName(at))); err != nil {
			t.Fatalf("clear real checkpoint: %v", err)
		}
		if _, err := checkpoint.Publish(root, foreign, func(d string) error {
			return os.MkdirAll(d, 0o755)
		}); err != nil {
			t.Fatalf("publish foreign checkpoint: %v", err)
		}
		fresh := filepath.Join(t.TempDir(), "state-fresh")
		assertSnapshotEqual(t, recoverInto(t, dir, fresh, root, cp), live, "fallback past a foreign checkpoint")
	})
}

// TestRecoverFromPrefersNewestUsableCheckpoint: with several checkpoints published, the
// newest usable one is chosen — and the result is the live state either way.
func TestRecoverFromPrefersNewestUsableCheckpoint(t *testing.T) {
	dir, root, cp, at, live := checkpointedRun(t)
	positions, err := checkpoint.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 1 || positions[0] != at {
		t.Fatalf("List = %v, want the single checkpoint at %d", positions, at)
	}
	// Publish an older, equally valid checkpoint alongside it; the newest must win, and
	// restoring from the newest's state must still reproduce the live state.
	older := &checkpoint.Manifest{Partition: 1, AppliedPosition: 1, HighestPosition: 1, KeyCounter: 1}
	if _, err := checkpoint.Publish(root, older, func(d string) error { return os.MkdirAll(d, 0o755) }); err != nil {
		t.Fatalf("publish older checkpoint: %v", err)
	}
	restored := filepath.Join(t.TempDir(), "state-from-checkpoint")
	copyTree(t, filepath.Join(root, checkpoint.DirName(at)), restored)
	assertSnapshotEqual(t, recoverInto(t, dir, restored, root, cp), live, "newest usable checkpoint wins")
}

// TestRecoverFromCheckpointSeedsKeyCounter is the test that proves the checkpoint is
// actually *used*, not merely present. After a checkpoint with no work following it,
// the covered WAL segments are deleted, so a replay sees no records at all. The
// manifest is then the only source of the key counter and the highest log position: a
// recovery that ignored it would restart the counter at zero and mint a key that
// collides with a live instance, silently overwriting it.
func TestRecoverFromCheckpointSeedsKeyCounter(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "checkpoints")
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	cp, _ := linearProcess(t)
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, n := range []string{"1", "2"} {
		p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: n})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	at, err := p.Checkpoint(root) // nothing happens after this, so the suffix is empty
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	before, _ := counts(t, store)
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}

	// Drop every segment the checkpoint covers: replay now has nothing to read.
	segs, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, seg := range segs {
		last, err := lastPositionIn(seg)
		if err != nil {
			t.Fatalf("scan %s: %v", seg, err)
		}
		if last != 0 && last <= at {
			if err := os.Remove(seg); err != nil {
				t.Fatalf("remove %s: %v", seg, err)
			}
		}
	}

	restored := filepath.Join(t.TempDir(), "state-from-checkpoint")
	copyTree(t, filepath.Join(root, checkpoint.DirName(at)), restored)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	store2, err := state.Open(restored)
	if err != nil {
		t.Fatalf("open restored store: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.RecoverFrom(root); err != nil {
		t.Fatalf("RecoverFrom: %v", err)
	}
	if got, _ := counts(t, store2); got != before {
		t.Fatalf("restored active instances = %d, want the checkpoint's %d", got, before)
	}

	// Minting a key must not collide with a recovered instance: the new instance is
	// additional, not an overwrite.
	p2.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "3"})
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after restore: %v", err)
	}
	if got, _ := counts(t, store2); got != before+1 {
		t.Fatalf("active instances after creating one more = %d, want %d — a reset key counter overwrote a recovered instance", got, before+1)
	}
}
