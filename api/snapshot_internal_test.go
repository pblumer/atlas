package api

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pblumer/atlas/checkpoint"
)

// These internal tests reach the defensive error branches of the full-snapshot
// backup/restore that a normal round-trip cannot hit as root, injecting faults at
// the fs.FS / io.Writer / http.ResponseWriter seams (the helper types errFS,
// truncWriter and failRW live in backup_internal_test.go).

func TestWriteFullBackupWalkError(t *testing.T) {
	// Opening the WAL directory (first in the snapshot) fails non-fatally.
	fsys := errFS{FS: fstest.MapFS{}, failOpen: "wal"}
	if err := writeFullBackup(tar.NewWriter(&bytes.Buffer{}), fsys, ""); err == nil {
		t.Fatal("writeFullBackup: want error from a failed WAL walk, got nil")
	}
}

func TestWriteFullBackupVaultKeyReadError(t *testing.T) {
	// The dirs are absent (skipped); reading the vault key then fails.
	fsys := errFS{FS: fstest.MapFS{"vault.key": {Data: []byte("secret")}}, failOpen: "vault.key"}
	if err := writeFullBackup(tar.NewWriter(&bytes.Buffer{}), fsys, ""); err == nil {
		t.Fatal("writeFullBackup: want error from an unreadable vault key, got nil")
	}
}

func TestWriteFileIntoSkipsMissing(t *testing.T) {
	// A missing optional file (vault.key with the vault disabled) is skipped, not an error.
	if err := writeFileInto(tar.NewWriter(&bytes.Buffer{}), fstest.MapFS{}, "vault.key"); err != nil {
		t.Fatalf("writeFileInto of a missing file: %v, want nil", err)
	}
}

func TestWriteFullBackupHeaderAndBodyWriteErrors(t *testing.T) {
	fsys := fstest.MapFS{"wal/seg-0": {Data: []byte("hello")}}
	if err := writeFullBackup(tar.NewWriter(&truncWriter{limit: 0}), fsys, ""); err == nil {
		t.Fatal("writeFullBackup: want error from a failed header write, got nil")
	}
	if err := writeFullBackup(tar.NewWriter(&truncWriter{limit: 512}), fsys, ""); err == nil {
		t.Fatal("writeFullBackup: want error from a failed body write, got nil")
	}
}

func TestStreamFullBackupSurfacesError(t *testing.T) {
	fsys := errFS{FS: fstest.MapFS{}, failOpen: "wal"}
	if err := streamFullBackup(&bytes.Buffer{}, fsys, ""); err == nil {
		t.Fatal("streamFullBackup: want the underlying walk error, got nil")
	}
}

func TestHandleBackupFullLogsStreamFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wal"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal", "seg-0"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{dataDir: dir}
	// A failing ResponseWriter drives the "streaming failed" branch without panicking.
	s.handleBackupFull(&failRW{}, httptest.NewRequest(http.MethodGet, "/api/v1/backup/full", nil))
}

func TestFullSnapshotEndpointsRequireAdmin(t *testing.T) {
	s := &Server{authEnabled: true}
	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		meth string
	}{
		{"backup/full", s.handleBackupFull, http.MethodGet},
		{"restore/full", s.handleRestoreFull, http.MethodPost},
	} {
		rec := httptest.NewRecorder()
		tc.fn(rec, httptest.NewRequest(tc.meth, "/", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without an admin principal: status=%d, want 403", tc.name, rec.Code)
		}
	}
}

func TestApplyPendingRestoreNoStaging(t *testing.T) {
	applied, err := ApplyPendingRestore(t.TempDir())
	if err != nil || applied {
		t.Fatalf("ApplyPendingRestore with nothing staged: applied=%v err=%v, want false/nil", applied, err)
	}
}

func TestApplyPendingRestoreStatError(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the staging directory is expected makes the marker Stat
	// fail with a non-not-exist error (ENOTDIR), which must surface.
	if err := os.WriteFile(filepath.Join(dir, restorePendingDir), []byte("x"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	if _, err := ApplyPendingRestore(dir); err == nil {
		t.Fatal("ApplyPendingRestore: want an error when staging is a file, got nil")
	}
}

func TestApplyPendingRestoreDiscardsPartialStaging(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, restorePendingDir)
	if err := os.MkdirAll(filepath.Join(staging, "wal"), 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "wal", "seg-0"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	// No .restore-ready marker: an interrupted upload. Apply discards it.
	applied, err := ApplyPendingRestore(dir)
	if err != nil || applied {
		t.Fatalf("apply of marker-less staging: applied=%v err=%v, want false/nil", applied, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("partial staging not discarded (err=%v)", err)
	}
}

func TestApplyPendingRestoreMovesEntriesAndDropsState(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, restorePendingDir)
	// Stage a directory entry, a file entry, and the completion marker.
	if err := os.MkdirAll(filepath.Join(staging, "drafts"), 0o755); err != nil {
		t.Fatalf("mkdir staged drafts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "drafts", "d.json"), []byte(`{"processId":"d"}`), 0o644); err != nil {
		t.Fatalf("write staged draft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "vault.key"), []byte("k"), 0o600); err != nil {
		t.Fatalf("write staged vault: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, restoreReady), []byte("1"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// A live state store that must be dropped so recovery rebuilds it.
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "CURRENT"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	applied, err := ApplyPendingRestore(dir)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v, want true/nil", applied, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "drafts", "d.json")); err != nil {
		t.Errorf("staged draft not moved into place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.key")); err != nil {
		t.Errorf("staged vault key not moved into place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Errorf("state store not dropped (err=%v)", err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging not removed after apply (err=%v)", err)
	}
}

// stageStaleCheckpoint plants a checkpoint of the log a restore is about to replace,
// and stages a minimal complete restore over dir. carriesCheckpoints follows the
// staging contract: handleRestoreFull always creates the checkpoint entry, empty when
// the archive had none, so the apply's rename pass replaces the local root either way.
func stageStaleCheckpoint(t *testing.T, dir string, carriesCheckpoints bool) {
	t.Helper()
	staging := filepath.Join(dir, restorePendingDir)
	if err := os.MkdirAll(filepath.Join(staging, "wal"), 0o755); err != nil {
		t.Fatalf("mkdir staged wal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "wal", "0000000001.wal"), []byte("restored"), 0o644); err != nil {
		t.Fatalf("write staged wal: %v", err)
	}
	if carriesCheckpoints {
		if err := os.MkdirAll(filepath.Join(staging, checkpoint.DirBase), 0o755); err != nil {
			t.Fatalf("mkdir staged checkpoints: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, restoreReady), []byte("1"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	stale := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(42))
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("mkdir stale checkpoint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, checkpoint.ManifestName), []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale manifest: %v", err)
	}
}

// TestApplyPendingRestoreDropsCheckpoints: a whole-instance restore replaces the WAL
// with a different log (ADR-0109), so any checkpoint left over from the old one
// describes positions in a log that no longer exists. Recovery only refuses such a
// checkpoint while the rebuilt store still trails it; once the restored log advances
// past that position the stale checkpoint would pass every guard and seed recovery from
// another log's state.
//
// The archive's own (possibly empty) checkpoint entry replacing the local root is what
// keeps that impossible, so this stages one the way handleRestoreFull always does.
func TestApplyPendingRestoreDropsCheckpoints(t *testing.T) {
	dir := t.TempDir()
	stageStaleCheckpoint(t, dir, true)

	applied, err := ApplyPendingRestore(dir)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v, want true/nil", applied, err)
	}
	positions, err := checkpoint.List(checkpoint.Dir(dir))
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("checkpoints of the replaced log survived the restore: %v", positions)
	}
	// Nothing to install from, so state stays absent and recovery replays the log.
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatalf("state was seeded from nothing (err=%v)", err)
	}
}

// TestApplyPendingRestoreRefusesUnverifiableCheckpoint: a restore whose checkpoints
// cannot be verified must fail loudly. Falling back to a plain replay would be fine for
// a whole log and would silently drop every instance below the cut for a compacted one
// — and the restore cannot tell which it has (ADR-0131).
func TestApplyPendingRestoreRefusesUnverifiableCheckpoint(t *testing.T) {
	dir := t.TempDir()
	// carriesCheckpoints=false leaves the damaged local checkpoint in place, standing in
	// for an archive that carried one whose manifest does not decode.
	stageStaleCheckpoint(t, dir, false)

	applied, err := ApplyPendingRestore(dir)
	if err == nil {
		t.Fatal("apply accepted a restore whose only checkpoint does not verify")
	}
	if applied {
		t.Error("a refused restore reported itself applied")
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Errorf("a refused restore left a state store behind (err=%v)", err)
	}
}

// TestInstallCheckpointStateWithoutARoot: a staging that carries no checkpoint entry and
// finds no local root — an archive from before ADR-0131, or a data dir that never
// checkpointed — installs nothing and leaves recovery to replay the whole log.
func TestInstallCheckpointStateWithoutARoot(t *testing.T) {
	dir := t.TempDir()
	if err := installCheckpointState(dir); err != nil {
		t.Fatalf("installCheckpointState with no checkpoint root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatalf("a no-op install created a state store (err=%v)", err)
	}
}

// TestUnreadableCheckpointRootIsNotSilentlyIgnored: a *missing* checkpoint root simply
// means there is none, but one that cannot be read at all might be hiding the checkpoint
// a compacted log depends on. The restore refuses rather than replaying past it, and the
// backup falls back to a WAL-only archive that the restore will then refuse in turn.
func TestUnreadableCheckpointRootIsNotSilentlyIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(checkpoint.Dir(dir), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	if err := installCheckpointState(dir); err == nil {
		t.Error("installCheckpointState ignored an unreadable checkpoint root")
	}
	if got := newestVerifiedCheckpoint(dir); got != "" {
		t.Errorf("newestVerifiedCheckpoint = %q over an unreadable root, want none", got)
	}
}

// TestWriteFullBackupCheckpointWalkError: a read failure while copying the archive's
// checkpoint fails the backup instead of silently shipping it half-copied.
func TestWriteFullBackupCheckpointWalkError(t *testing.T) {
	fsys := errFS{FS: fstest.MapFS{"checkpoints/0001/manifest": {Data: []byte("m")}}, failOpen: "checkpoints/0001"}
	if err := writeFullBackup(tar.NewWriter(&bytes.Buffer{}), fsys, "checkpoints/0001"); err == nil {
		t.Fatal("writeFullBackup: want the checkpoint walk error, got nil")
	}
}

// TestInstallStateFromMissingSourceFails: the copy surfaces its error rather than
// leaving a half-built state store behind — a Pebble directory that exists but is
// incomplete is worse than none.
func TestInstallStateFromMissingSourceFails(t *testing.T) {
	dir := t.TempDir()
	if err := installStateFrom(filepath.Join(dir, "no-such-checkpoint"), dir); err == nil {
		t.Fatal("installStateFrom accepted a source that does not exist")
	}
	for _, name := range []string{"state", "state.restoring"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("a failed install left %s behind (err=%v)", name, err)
		}
	}
}

// TestCopyTreeSkipsNonRegularFiles: a checkpoint directory holds only regular files and
// directories, so anything else is skipped rather than followed — a symlink must not
// smuggle a file from outside the checkpoint into the restored state store.
func TestCopyTreeSkipsNonRegularFiles(t *testing.T) {
	src := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "000001.sst"), []byte("sst"), 0o644); err != nil {
		t.Fatalf("write sst: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "link")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "000001.sst")); err != nil {
		t.Errorf("regular file not copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link")); !os.IsNotExist(err) {
		t.Errorf("symlink was copied into the state store (err=%v)", err)
	}
}

// TestNewestVerifiedCheckpointFallsPastAllCorrupt: with no checkpoint that verifies, the
// snapshot carries none at all rather than one that cannot stand in for a WAL prefix.
// The archive is then exactly the pre-ADR-0131 one — correct for a whole log, and the
// restore refuses it rather than guessing if the log turns out to be compacted.
func TestNewestVerifiedCheckpointFallsPastAllCorrupt(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(7))
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatalf("mkdir checkpoint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, checkpoint.ManifestName), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if got := newestVerifiedCheckpoint(dir); got != "" {
		t.Fatalf("newestVerifiedCheckpoint = %q, want none when nothing verifies", got)
	}
}
