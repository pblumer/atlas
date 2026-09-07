package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
)

// publishCheckpointWith writes a verifiable checkpoint holding one marker file, so
// a test can tell whether the state directory came from it.
func publishCheckpointWith(t *testing.T, dataDir, marker string) {
	t.Helper()
	root := checkpoint.Dir(dataDir)
	m := &checkpoint.Manifest{Partition: 1, AppliedPosition: 7, HighestPosition: 7, KeyCounter: 3, CreatedUnixNano: 1}
	if _, err := checkpoint.Publish(root, m, func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, marker), []byte("x"), 0o644)
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// TestSeedStateFromCheckpointOnlyWhenThereIsNoState: a data directory with no
// state store has to get its starting point from a checkpoint, because a compacted
// log no longer carries the prefix that would rebuild it. A directory that already
// has one must be left alone — its state is the newer answer, and replacing it with
// a checkpoint would throw away everything applied since.
func TestSeedStateFromCheckpointOnlyWhenThereIsNoState(t *testing.T) {
	t.Run("no state: seeded from the checkpoint", func(t *testing.T) {
		dir := t.TempDir()
		publishCheckpointWith(t, dir, "from-checkpoint")
		seeded, err := SeedStateFromCheckpoint(dir)
		if err != nil {
			t.Fatalf("SeedStateFromCheckpoint: %v", err)
		}
		if !seeded {
			t.Fatal("reported nothing seeded, but the directory had no state and a good checkpoint")
		}
		if _, err := os.Stat(filepath.Join(dir, "state", "from-checkpoint")); err != nil {
			t.Fatalf("state was not taken from the checkpoint: %v", err)
		}
	})

	t.Run("state present: left alone", func(t *testing.T) {
		dir := t.TempDir()
		publishCheckpointWith(t, dir, "from-checkpoint")
		if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state", "already-here"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		seeded, err := SeedStateFromCheckpoint(dir)
		if err != nil {
			t.Fatalf("SeedStateFromCheckpoint: %v", err)
		}
		if seeded {
			t.Fatal("an existing state store was replaced; everything applied since the checkpoint would be lost")
		}
		if _, err := os.Stat(filepath.Join(dir, "state", "already-here")); err != nil {
			t.Fatalf("the existing state was disturbed: %v", err)
		}
	})

	t.Run("no checkpoint: nothing to seed, and that is fine", func(t *testing.T) {
		dir := t.TempDir()
		seeded, err := SeedStateFromCheckpoint(dir)
		if err != nil {
			t.Fatalf("SeedStateFromCheckpoint on a fresh directory: %v", err)
		}
		if seeded {
			t.Fatal("reported seeding from a checkpoint that does not exist")
		}
	})

	t.Run("checkpoints present but none verifies: refuse", func(t *testing.T) {
		dir := t.TempDir()
		publishCheckpointWith(t, dir, "from-checkpoint")
		positions, err := checkpoint.List(checkpoint.Dir(dir))
		if err != nil || len(positions) == 0 {
			t.Fatalf("fixture has no checkpoint: %v", err)
		}
		bad := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(positions[0]), checkpoint.ManifestName)
		if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
			t.Fatalf("corrupt manifest: %v", err)
		}
		if _, err := SeedStateFromCheckpoint(dir); err == nil {
			t.Fatal("seeded from a checkpoint set where none verifies; a compacted log would boot with everything below the cut missing")
		}
	})
}
