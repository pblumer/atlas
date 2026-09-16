package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestStageTakesTheSnapshotAndCommitDoesTheReading is the property the split exists
// for: Stage does the part that needs the writer stopped (the snapshot), and every
// byte-reading step — the checksum over the whole state — happens in Commit, which
// runs with the writer free.
//
// It is asserted through the checksum seam rather than by timing, so it states the
// structure rather than observing a race.
func TestStageTakesTheSnapshotAndCommitDoesTheReading(t *testing.T) {
	root := t.TempDir()
	checksums := 0
	restore := checksumDir
	checksumDir = func(dir string) (uint64, error) {
		checksums++
		return restore(dir)
	}
	t.Cleanup(func() { checksumDir = restore })

	snapshotted := false
	staged, err := Stage(root, testManifest(42), func(dir string) error {
		snapshotted = true
		return fakeSnapshot("state-v1")(dir)
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if !snapshotted {
		t.Error("Stage did not take the snapshot")
	}
	if checksums != 0 {
		t.Errorf("Stage read the state %d times; the whole point is that it reads none", checksums)
	}
	if staged.AppliedPosition() != 42 {
		t.Errorf("AppliedPosition = %d, want 42", staged.AppliedPosition())
	}

	path, err := staged.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if checksums != 1 {
		t.Errorf("Commit read the state %d times, want 1", checksums)
	}
	if want := filepath.Join(root, DirName(42)); path != want {
		t.Fatalf("published path = %q, want %q", path, want)
	}
	if _, err := Verify(root, 42); err != nil {
		t.Fatalf("Verify after a staged commit: %v", err)
	}
}

// TestStagedCommitIgnoresStateWrittenAfterStage: the staged directory is the snapshot
// as of Stage, so writes that land while Commit is running — which is the entire point
// of letting the writer run during it — cannot change what is published.
func TestStagedCommitIgnoresStateWrittenAfterStage(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "000001.sst"), []byte("as-of-stage"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, err := Stage(root, testManifest(9), func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(source, "000001.sst"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "000001.sst"), b, 0o644)
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// The writer runs on: the live state moves past the snapshot.
	if err := os.WriteFile(filepath.Join(source, "000001.sst"), []byte("after-stage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, DirName(9), "000001.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "as-of-stage" {
		t.Errorf("published content = %q, want the state as of Stage", got)
	}
	if _, err := Verify(root, 9); err != nil {
		t.Errorf("Verify: %v — the checksum must describe what was staged", err)
	}
}

// TestStagedAbandonLeavesNothingBehind: a caller that stages and then decides not to
// publish (a failing pass, a shutdown) must not leave the temp directory for the next
// attempt to trip over.
func TestStagedAbandonLeavesNothingBehind(t *testing.T) {
	root := t.TempDir()
	staged, err := Stage(root, testManifest(11), fakeSnapshot("state"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := staged.Abandon(); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("root still holds %d entries after Abandon, want none", len(entries))
	}
	positions, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 0 {
		t.Errorf("List = %v after Abandon, want none published", positions)
	}
}

// TestStageAtAPublishedPositionCommitsWithoutReading: staging where a checkpoint
// already exists publishes nothing and reads nothing — the retry-after-crash path
// must not pay for the whole state a second time.
func TestStageAtAPublishedPositionCommitsWithoutReading(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(5), fakeSnapshot("first")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	checksums := 0
	restore := checksumDir
	checksumDir = func(dir string) (uint64, error) {
		checksums++
		return restore(dir)
	}
	t.Cleanup(func() { checksumDir = restore })

	staged, err := Stage(root, testManifest(5), func(string) error {
		t.Error("Stage took a snapshot at an already-published position")
		return nil
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	path, err := staged.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if want := filepath.Join(root, DirName(5)); path != want {
		t.Fatalf("path = %q, want the already-published %q", path, want)
	}
	if checksums != 0 {
		t.Errorf("a no-op commit read the state %d times, want 0", checksums)
	}
}

// TestStagedCommitAbandonsOnFailure: a Commit that cannot finish must clear its temp
// directory, exactly as the single-call Publish does — a half-built checkpoint left
// behind is what the tmp- prefix exists to prevent.
func TestStagedCommitAbandonsOnFailure(t *testing.T) {
	root := t.TempDir()
	staged, err := Stage(root, testManifest(13), fakeSnapshot("state"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	boom := errors.New("rename refused")
	restore := renameDir
	renameDir = func(string, string) error { return boom }
	t.Cleanup(func() { renameDir = restore })

	if _, err := staged.Commit(); !errors.Is(err, boom) {
		t.Fatalf("Commit error = %v, want %v", err, boom)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("root holds %d entries after a failed commit, want none", len(entries))
	}
}
