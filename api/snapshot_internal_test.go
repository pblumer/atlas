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
	if err := writeFullBackup(tar.NewWriter(&bytes.Buffer{}), fsys); err == nil {
		t.Fatal("writeFullBackup: want error from a failed WAL walk, got nil")
	}
}

func TestWriteFullBackupVaultKeyReadError(t *testing.T) {
	// The dirs are absent (skipped); reading the vault key then fails.
	fsys := errFS{FS: fstest.MapFS{"vault.key": {Data: []byte("secret")}}, failOpen: "vault.key"}
	if err := writeFullBackup(tar.NewWriter(&bytes.Buffer{}), fsys); err == nil {
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
	if err := writeFullBackup(tar.NewWriter(&truncWriter{limit: 0}), fsys); err == nil {
		t.Fatal("writeFullBackup: want error from a failed header write, got nil")
	}
	if err := writeFullBackup(tar.NewWriter(&truncWriter{limit: 512}), fsys); err == nil {
		t.Fatal("writeFullBackup: want error from a failed body write, got nil")
	}
}

func TestStreamFullBackupSurfacesError(t *testing.T) {
	fsys := errFS{FS: fstest.MapFS{}, failOpen: "wal"}
	if err := streamFullBackup(&bytes.Buffer{}, fsys); err == nil {
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

// TestApplyPendingRestoreDropsCheckpoints: a whole-instance restore replaces the WAL
// with a different log (ADR-0108), so any checkpoint left over from the old one
// describes positions in a log that no longer exists. Recovery only refuses such a
// checkpoint while the rebuilt store still trails it; once the restored log advances
// past that position the stale checkpoint would pass every guard and seed recovery from
// another log's state. Dropping them alongside the state store is what keeps that
// impossible — at the cost of one full replay, which is exactly what a restore does
// anyway (ADR-0131).
func TestApplyPendingRestoreDropsCheckpoints(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, restorePendingDir)
	if err := os.MkdirAll(filepath.Join(staging, "wal"), 0o755); err != nil {
		t.Fatalf("mkdir staged wal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "wal", "0000000001.wal"), []byte("restored"), 0o644); err != nil {
		t.Fatalf("write staged wal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, restoreReady), []byte("1"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// A checkpoint published against the log this restore is about to replace.
	stale := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(42))
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("mkdir stale checkpoint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, checkpoint.ManifestName), []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale manifest: %v", err)
	}

	applied, err := ApplyPendingRestore(dir)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v, want true/nil", applied, err)
	}
	if _, err := os.Stat(checkpoint.Dir(dir)); !os.IsNotExist(err) {
		t.Fatalf("checkpoints of the replaced log survived the restore (err=%v)", err)
	}
}
