package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSnapshot returns a snapshot function that creates dir and writes marker files,
// standing in for state.Store.Snapshot (which likewise creates the directory itself).
func fakeSnapshot(content string) func(string) error {
	return func(dir string) error {
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "000001.sst"), []byte(content), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "sub", "CURRENT"), []byte("current"), 0o644)
	}
}

func testManifest(applied uint64) *Manifest {
	return &Manifest{
		Partition:       1,
		AppliedPosition: applied,
		HighestPosition: applied,
		KeyCounter:      applied * 2,
		CreatedUnixNano: 1_755_000_000_000_000_000,
		AtlasVersion:    "0.2.0-test",
		Deployments:     []DeploymentRef{{Key: 7, Version: 1}},
	}
}

// TestPublishThenListLoadVerify: a published checkpoint is discoverable, its manifest
// round-trips the fields the caller set, and its recorded state checksum matches the
// snapshot content.
func TestPublishThenListLoadVerify(t *testing.T) {
	root := t.TempDir()
	m := testManifest(42)
	path, err := Publish(root, m, fakeSnapshot("state-v1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if want := filepath.Join(root, DirName(42)); path != want {
		t.Fatalf("published path = %q, want %q", path, want)
	}
	if m.StateChecksum == 0 {
		t.Error("Publish did not record a state checksum in the manifest")
	}

	positions, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 1 || positions[0] != 42 {
		t.Fatalf("List = %v, want [42]", positions)
	}

	got, err := Verify(root, 42)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Partition != 1 || got.AppliedPosition != 42 || got.HighestPosition != 42 ||
		got.KeyCounter != 84 || got.AtlasVersion != "0.2.0-test" || got.StateChecksum != m.StateChecksum {
		t.Fatalf("loaded manifest = %+v, want the published one %+v", got, m)
	}
	if len(got.Deployments) != 1 || got.Deployments[0] != (DeploymentRef{Key: 7, Version: 1}) {
		t.Fatalf("deployments = %+v, want [{7 1}]", got.Deployments)
	}
}

// TestPublishAtomicOnSnapshotFailure: a crash *during* the snapshot (modelled by a
// failing snapshot func) publishes nothing and leaves no temp directory behind.
func TestPublishAtomicOnSnapshotFailure(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("disk on fire")
	_, err := Publish(root, testManifest(7), func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil { // partial work, then fail
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Publish err = %v, want the snapshot error", err)
	}
	positions, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("a failed snapshot published %v, want nothing", positions)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed publish left %d entries behind, want none", len(entries))
	}
}

// TestCrashBeforeRenameIsInvisible: a crash after the snapshot and manifest were
// written but *before* the publishing rename leaves a complete temp directory. It must
// not count as a checkpoint, and the next attempt must clear it and publish cleanly —
// the rename is the publication point (ADR-0131).
func TestCrashBeforeRenameIsInvisible(t *testing.T) {
	root := t.TempDir()
	// Model the interrupted attempt: a fully-populated tmp- directory.
	tmp := filepath.Join(root, tempPrefix+DirName(9))
	if err := fakeSnapshot("half-published")(tmp); err != nil {
		t.Fatalf("seed temp dir: %v", err)
	}
	blob, err := testManifest(9).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ManifestName), blob, 0o644); err != nil {
		t.Fatalf("seed temp manifest: %v", err)
	}

	positions, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("an un-renamed temp directory was listed as published: %v", positions)
	}

	// The retry clears the leftover and publishes for real.
	if _, err := Publish(root, testManifest(9), fakeSnapshot("state-v2")); err != nil {
		t.Fatalf("Publish after interrupted attempt: %v", err)
	}
	if positions, err = List(root); err != nil || len(positions) != 1 || positions[0] != 9 {
		t.Fatalf("List after retry = %v (err %v), want [9]", positions, err)
	}
	if _, err := Verify(root, 9); err != nil {
		t.Fatalf("Verify after retry: %v", err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp directory still present after a successful retry (err %v)", err)
	}
}

// TestPublishIdempotent: publishing again at an already-published position is a no-op,
// so retrying after a crash between the rename and the caller's bookkeeping is safe
// and does not disturb the existing checkpoint.
func TestPublishIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(5), fakeSnapshot("original")); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	first, err := Load(root, 5)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Publish(root, testManifest(5), func(string) error {
		t.Error("snapshot must not run for an already-published position")
		return nil
	}); err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	again, err := Load(root, 5)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if again.StateChecksum != first.StateChecksum {
		t.Error("re-publishing rewrote the existing checkpoint")
	}
	if positions, _ := List(root); len(positions) != 1 {
		t.Fatalf("List = %v, want a single checkpoint", positions)
	}
}

// TestVerifyDetectsCorruptStateFile: editing a state file after publication breaks the
// recorded checksum, so Verify rejects the checkpoint instead of restoring it.
func TestVerifyDetectsCorruptStateFile(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(11), fakeSnapshot("good")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	victim := filepath.Join(root, DirName(11), "000001.sst")
	if err := os.WriteFile(victim, []byte("corrupted"), 0o644); err != nil {
		t.Fatalf("corrupt state file: %v", err)
	}
	if _, err := Verify(root, 11); !errors.Is(err, ErrStateChecksum) {
		t.Fatalf("Verify err = %v, want ErrStateChecksum", err)
	}
}

// TestVerifyDetectsTruncatedManifest: a torn manifest fails the codec's own checksum,
// surfacing as a skip-and-fall-back signal rather than a hard failure.
func TestVerifyDetectsTruncatedManifest(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(13), fakeSnapshot("good")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	path := filepath.Join(root, DirName(13), ManifestName)
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(path, blob[:len(blob)-3], 0o644); err != nil {
		t.Fatalf("truncate manifest: %v", err)
	}
	if _, err := Verify(root, 13); err == nil {
		t.Fatal("Verify accepted a truncated manifest")
	}
}

// TestLoadMissingCheckpoint: loading a position that was never published is an
// ordinary not-exist error.
func TestLoadMissingCheckpoint(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root, 99); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load missing err = %v, want not-exist", err)
	}
	if _, err := Verify(root, 99); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Verify missing err = %v, want not-exist", err)
	}
}

// TestPublishRejectsInvalidManifest: a manifest that fails its own invariants is
// refused before anything is written.
func TestPublishRejectsInvalidManifest(t *testing.T) {
	root := t.TempDir()
	bad := testManifest(10)
	bad.HighestPosition = 9 // below the applied position
	if _, err := Publish(root, bad, fakeSnapshot("x")); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("Publish err = %v, want ErrInconsistent", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("rejected publish wrote %d entries", len(entries))
	}
}

// TestListIgnoresUnrelatedEntries: only well-formed published directories count, so
// stray files, short names, non-numeric names, and temp directories are skipped.
func TestListIgnoresUnrelatedEntries(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(3), fakeSnapshot("s")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for _, name := range []string{"README", "notacheckpoint", tempPrefix + DirName(4), "0000000000000000000x"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, DirName(8)+".txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	positions, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 1 || positions[0] != 3 {
		t.Fatalf("List = %v, want only [3]", positions)
	}
}

// TestListMissingRoot: a data directory with no checkpoints yet is not an error.
func TestListMissingRoot(t *testing.T) {
	positions, err := List(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("List = %v, want empty", positions)
	}
}

// TestPruneKeepsNewest: pruning bounds disk by deleting the oldest checkpoints, never
// drops below one, and clears temp directories left by crashed attempts.
func TestPruneKeepsNewest(t *testing.T) {
	root := t.TempDir()
	for _, pos := range []uint64{10, 20, 30, 40} {
		if _, err := Publish(root, testManifest(pos), fakeSnapshot("s")); err != nil {
			t.Fatalf("Publish %d: %v", pos, err)
		}
	}
	leftover := filepath.Join(root, tempPrefix+DirName(50))
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatalf("seed leftover: %v", err)
	}

	if err := Prune(root, 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	positions, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(positions) != 2 || positions[0] != 30 || positions[1] != 40 {
		t.Fatalf("after prune List = %v, want [30 40]", positions)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Prune left a temp directory behind (err %v)", err)
	}
	// The newest survivor is still intact and verifiable.
	if _, err := Verify(root, 40); err != nil {
		t.Fatalf("Verify newest after prune: %v", err)
	}

	// keep < 1 is clamped, so the only recovery source is never removed.
	if err := Prune(root, 0); err != nil {
		t.Fatalf("Prune(0): %v", err)
	}
	if positions, _ = List(root); len(positions) != 1 || positions[0] != 40 {
		t.Fatalf("after Prune(0) List = %v, want [40]", positions)
	}
}

// TestPruneNoCheckpoints: pruning an empty or missing root is a no-op, not an error.
func TestPruneNoCheckpoints(t *testing.T) {
	if err := Prune(t.TempDir(), 3); err != nil {
		t.Fatalf("Prune empty root: %v", err)
	}
	if err := Prune(filepath.Join(t.TempDir(), "absent"), 3); err != nil {
		t.Fatalf("Prune missing root: %v", err)
	}
}

// TestChecksumDirIgnoresManifest: the checksum covers state files only, so writing the
// manifest cannot change the value recorded inside it — that is what lets Verify
// recompute the same number.
func TestChecksumDirIgnoresManifest(t *testing.T) {
	dir := t.TempDir()
	if err := fakeSnapshot("payload")(dir); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	before, err := ChecksumDir(dir)
	if err != nil {
		t.Fatalf("ChecksumDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte("anything"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	after, err := ChecksumDir(dir)
	if err != nil {
		t.Fatalf("ChecksumDir after: %v", err)
	}
	if before != after {
		t.Fatalf("checksum changed when the manifest appeared: %d -> %d", before, after)
	}
	// Renaming a state file changes the checksum even though the bytes are the same.
	if err := os.Rename(filepath.Join(dir, "000001.sst"), filepath.Join(dir, "000002.sst")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed, err := ChecksumDir(dir)
	if err != nil {
		t.Fatalf("ChecksumDir renamed: %v", err)
	}
	if renamed == after {
		t.Error("checksum ignored a state-file rename")
	}
}

// TestChecksumDirMissing: hashing a directory that does not exist is an error, not a
// silent zero that would masquerade as a valid checksum.
func TestChecksumDirMissing(t *testing.T) {
	if _, err := ChecksumDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("ChecksumDir on a missing directory returned no error")
	}
}

// TestPublishRootIsAFile: if the checkpoint root path is occupied by a file, publish
// fails cleanly instead of proceeding with a half-built directory.
func TestPublishRootIsAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkpoints")
	if err := os.WriteFile(root, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := Publish(root, testManifest(1), fakeSnapshot("s")); err == nil {
		t.Fatal("Publish succeeded with a file where the root directory belongs")
	}
}

// TestPublishUnreadableStateFile: a snapshot whose content cannot be read back (here a
// dangling symlink) fails the checksum step, so nothing is published and the temp
// directory is cleaned up rather than left to be mistaken for work in progress.
func TestPublishUnreadableStateFile(t *testing.T) {
	root := t.TempDir()
	_, err := Publish(root, testManifest(2), func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.Symlink(filepath.Join(dir, "nonexistent-target"), filepath.Join(dir, "000001.sst"))
	})
	if err == nil {
		t.Fatal("Publish succeeded despite an unreadable state file")
	}
	if positions, _ := List(root); len(positions) != 0 {
		t.Fatalf("List = %v, want nothing published", positions)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("failed publish left %d entries behind", len(entries))
	}
}

// TestPublishManifestPathBlocked: if the manifest cannot be written (its path is taken
// by a directory), the checkpoint is abandoned and nothing is published.
func TestPublishManifestPathBlocked(t *testing.T) {
	root := t.TempDir()
	_, err := Publish(root, testManifest(3), func(dir string) error {
		if err := fakeSnapshot("s")(dir); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(dir, ManifestName), 0o755) // blocks the manifest file
	})
	if err == nil {
		t.Fatal("Publish succeeded despite an unwritable manifest path")
	}
	if positions, _ := List(root); len(positions) != 0 {
		t.Fatalf("List = %v, want nothing published", positions)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("failed publish left %d entries behind", len(entries))
	}
}

// TestVerifyMissingStateAfterLoad: a manifest that survives while its state files are
// gone is rejected by Verify rather than treated as a usable checkpoint.
func TestVerifyMissingStateAfterLoad(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(4), fakeSnapshot("s")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Replace a state file with a dangling symlink: the manifest still decodes, but the
	// content can no longer be hashed.
	victim := filepath.Join(root, DirName(4), "000001.sst")
	if err := os.Remove(victim); err != nil {
		t.Fatalf("remove state file: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"), victim); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Verify(root, 4); err == nil {
		t.Fatal("Verify accepted a checkpoint whose state cannot be read")
	}
}

// TestLoadRejectsSemanticallyInvalidManifest: a manifest whose bytes decode cleanly but
// whose fields are inconsistent (a highest position below the applied one) is refused.
// Marshal does not enforce the invariants, so a hand-written or hostile manifest can
// reach Load — it must be skipped like any other unusable checkpoint, not trusted.
func TestLoadRejectsSemanticallyInvalidManifest(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(root, testManifest(6), fakeSnapshot("s")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bad := testManifest(6)
	bad.HighestPosition = 1 // below AppliedPosition, but still encodable
	blob, err := bad.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, DirName(6), ManifestName), blob, 0o644); err != nil {
		t.Fatalf("overwrite manifest: %v", err)
	}
	if _, err := Load(root, 6); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("Load err = %v, want ErrInconsistent", err)
	}
	if _, err := Verify(root, 6); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("Verify err = %v, want ErrInconsistent", err)
	}
}

// withFailingFsync makes the directory fsync fail for dir (matched by suffix) and
// restores the real implementation when the test ends.
func withFailingFsync(t *testing.T, match string, boom error) {
	t.Helper()
	prev := fsyncDir
	fsyncDir = func(dir string) error {
		if strings.HasSuffix(dir, match) {
			return boom
		}
		return prev(dir)
	}
	t.Cleanup(func() { fsyncDir = prev })
}

// TestPublishAbandonsOnTempFsyncFailure: if the checkpoint directory cannot be made
// durable, nothing is published — an un-fsynced checkpoint must never become visible.
func TestPublishAbandonsOnTempFsyncFailure(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("fsync failed")
	withFailingFsync(t, tempPrefix+DirName(21), boom)

	if _, err := Publish(root, testManifest(21), fakeSnapshot("s")); !errors.Is(err, boom) {
		t.Fatalf("Publish err = %v, want the fsync error", err)
	}
	if positions, _ := List(root); len(positions) != 0 {
		t.Fatalf("List = %v, want nothing published", positions)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("abandoned publish left %d entries behind", len(entries))
	}
}

// TestPublishAbandonsOnRenameFailure: the rename is the publication point, so a rename
// that fails publishes nothing and cleans up after itself.
func TestPublishAbandonsOnRenameFailure(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("rename failed")
	prev := renameDir
	renameDir = func(string, string) error { return boom }
	t.Cleanup(func() { renameDir = prev })

	if _, err := Publish(root, testManifest(22), fakeSnapshot("s")); !errors.Is(err, boom) {
		t.Fatalf("Publish err = %v, want the rename error", err)
	}
	if positions, _ := List(root); len(positions) != 0 {
		t.Fatalf("List = %v, want nothing published", positions)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("abandoned publish left %d entries behind", len(entries))
	}
}

// TestPublishReportsParentFsyncFailure: when the *parent* fsync fails the rename has
// already happened, so the checkpoint is visible but not provably durable. Publish
// reports the error, and a retry finds it published and succeeds — the idempotent path
// that makes reporting safe.
func TestPublishReportsParentFsyncFailure(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("parent fsync failed")
	prev := fsyncDir
	fsyncDir = func(dir string) error {
		if dir == root {
			return boom
		}
		return prev(dir)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			fsyncDir = prev
		}
	})

	if _, err := Publish(root, testManifest(23), fakeSnapshot("s")); !errors.Is(err, boom) {
		t.Fatalf("Publish err = %v, want the parent fsync error", err)
	}
	// The rename happened, so the checkpoint is on disk and verifiable.
	if positions, _ := List(root); len(positions) != 1 || positions[0] != 23 {
		t.Fatalf("List = %v, want [23] — the rename already published it", positions)
	}
	if _, err := Verify(root, 23); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// A retry with a healthy disk is a no-op and reports success.
	fsyncDir = prev
	restored = true
	if _, err := Publish(root, testManifest(23), func(string) error {
		t.Error("snapshot must not re-run for an already-published position")
		return nil
	}); err != nil {
		t.Fatalf("retry after a parent-fsync failure: %v", err)
	}
}

// TestPublishRejectsUnencodableManifest: a manifest that passes its semantic
// invariants but cannot be encoded (an over-long version string) is caught at Marshal,
// so the attempt is abandoned and nothing is published.
func TestPublishRejectsUnencodableManifest(t *testing.T) {
	root := t.TempDir()
	m := testManifest(24)
	m.AtlasVersion = strings.Repeat("v", maxAtlasVersion+1)
	if _, err := Publish(root, m, fakeSnapshot("s")); !errors.Is(err, ErrTooLong) {
		t.Fatalf("Publish err = %v, want ErrTooLong", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("rejected publish left %d entries behind", len(entries))
	}
}

// TestListAndPruneOnNonDirectory: a checkpoint root that is not a directory is a real
// error, distinct from "no checkpoints yet" — it must not be mistaken for an empty set.
func TestListAndPruneOnNonDirectory(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "checkpoints")
	if err := os.WriteFile(notADir, []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := List(notADir); err == nil {
		t.Fatal("List on a non-directory returned no error")
	}
	if err := Prune(notADir, 2); err == nil {
		t.Fatal("Prune on a non-directory returned no error")
	}
}

// TestSyncDirMissing: fsyncing a directory that is not there fails rather than silently
// reporting success — a durability step must never be a no-op.
func TestSyncDirMissing(t *testing.T) {
	if err := syncDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("syncDir on a missing directory returned no error")
	}
}

// TestDirIsADedicatedSubdirectoryOfTheDataDir pins the on-disk layout the server and
// startup recovery share. Both resolve the checkpoint root through Dir, so this is the
// one place the path is decided; the assertions are the properties that make sharing
// it safe — it sits inside the data dir, and it is a directory of its own rather than
// the data dir itself, which already holds the WAL and the state store.
func TestDirIsADedicatedSubdirectoryOfTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	root := Dir(dataDir)
	if root == dataDir {
		t.Fatalf("Dir(%q) returned the data dir itself; checkpoints would mix with the WAL and state store", dataDir)
	}
	if parent := filepath.Dir(root); parent != dataDir {
		t.Errorf("Dir(%q) = %q, which lives under %q rather than the data dir", dataDir, root, parent)
	}
	// And a checkpoint published at that path is found again at that same path — the
	// round trip a restart depends on.
	if _, err := Publish(root, testManifest(8), fakeSnapshot("s")); err != nil {
		t.Fatalf("Publish into Dir(dataDir): %v", err)
	}
	positions, err := List(Dir(dataDir))
	if err != nil {
		t.Fatalf("List(Dir(dataDir)): %v", err)
	}
	if len(positions) != 1 || positions[0] != 8 {
		t.Fatalf("List(Dir(dataDir)) = %v, want [8]", positions)
	}
}
