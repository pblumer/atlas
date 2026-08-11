package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestFullSnapshotRestoreRoundTrip is the core ADR-0108 property: a whole-instance
// snapshot taken while an instance is running, restored onto a fresh, empty data
// directory, brings that running instance back — its state rebuilt from the restored
// WAL, with the materialized state store never carried in the archive.
func TestFullSnapshotRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	first := boot(t, src)
	if code, body := doReq(t, first.ts, "POST", "/api/v1/deployments", sampleBPMN, "application/xml"); code != 200 {
		t.Fatalf("deploy: %d %s", code, body)
	}
	if code, body := doReq(t, first.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance: %d %s", code, body)
	}

	code, archive := doReq(t, first.ts, "GET", "/api/v1/backup/full", "", "")
	if code != 200 {
		t.Fatalf("backup/full status=%d", code)
	}
	names := tarGzEntries(t, archive)
	if !hasPrefix(names, "wal/") {
		t.Fatalf("snapshot missing the WAL (running instances): %v", names)
	}
	if !hasPrefix(names, "deployments/") {
		t.Fatalf("snapshot missing the design-time deployment: %v", names)
	}
	for n := range names {
		if strings.HasPrefix(n, "state/") {
			t.Fatalf("snapshot wrongly carried the derivable state store: %q", n)
		}
	}
	first.shutdown()

	// A fresh, empty instance: stage the snapshot, then apply it and boot.
	dst := t.TempDir()
	d2 := boot(t, dst)
	code, body := doReqBytes(t, d2.ts, "POST", "/api/v1/restore/full", archive, "application/gzip")
	if code != 200 || !strings.Contains(string(body), `"restartRequired":true`) {
		t.Fatalf("restore/full status=%d body=%s", code, body)
	}
	d2.shutdown()

	applied, err := api.ApplyPendingRestore(dst)
	if err != nil || !applied {
		t.Fatalf("ApplyPendingRestore: applied=%v err=%v", applied, err)
	}
	// The staging directory is consumed once applied.
	if _, err := os.Stat(filepath.Join(dst, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatalf("staging directory survived apply (err=%v)", err)
	}

	third := boot(t, dst)
	defer third.shutdown()

	// The deployment is back...
	if code, body := doReq(t, third.ts, "GET", "/api/v1/processes", "", ""); code != 200 || !strings.Contains(string(body), `"processId":"order"`) {
		t.Fatalf("processes after restore: %d %s", code, body)
	}
	// ...and so is the running instance, rebuilt from the restored WAL.
	if code, body := doReq(t, third.ts, "GET", "/api/v1/stats", "", ""); code != 200 || !strings.Contains(string(body), `"activeProcessInstances":1`) {
		t.Fatalf("stats after restore: %d %s", code, body)
	}
}

// TestBackupFullIncludesSecretsExcludesState checks the whole-instance snapshot
// carries the WAL, the vault key and user accounts (true DR), but never the
// derivable state store.
func TestBackupFullIncludesSecretsExcludesState(t *testing.T) {
	src, dir := newBackupServer(t)
	// Plant a user account and a state-store file; the vault key already exists
	// (the vault is on by default).
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "users", "admin.json"), []byte(`{"u":"admin"}`), 0o600); err != nil {
		t.Fatalf("write user: %v", err)
	}

	code, archive := doReq(t, src, "GET", "/api/v1/backup/full", "", "")
	if code != 200 {
		t.Fatalf("backup/full status=%d", code)
	}
	names := tarGzEntries(t, archive)
	for _, want := range []string{"wal/", "users/", "vault.key"} {
		if !hasPrefix(names, want) {
			t.Errorf("snapshot missing %q: %v", want, names)
		}
	}
	for n := range names {
		if strings.HasPrefix(n, "state/") {
			t.Errorf("snapshot wrongly carried state file %q", n)
		}
	}
}

// TestRestoreFullRejectsArchiveWithoutWAL guards against a design-time-only backup
// being applied as a full restore, which would wipe state while leaving the current
// WAL in place.
func TestRestoreFullRejectsArchiveWithoutWAL(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{"drafts/x.json": `{"processId":"x"}`})
	code, body := doReqBytes(t, dst, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "not a full snapshot") {
		t.Fatalf("restore/full without WAL status=%d body=%s, want 400 not-a-full-snapshot", code, body)
	}
	// The rejected upload leaves no staging behind.
	if _, err := os.Stat(filepath.Join(dir, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatalf("rejected restore left a staging dir (err=%v)", err)
	}
}

// TestRestoreFullStagesAllowlistedSkipsRest stages a valid full archive: the WAL and
// design-time entries land in staging, a crafted state/ entry is skipped, and a
// restart is requested.
func TestRestoreFullStagesAllowlistedSkipsRest(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"wal/seg-0000000000": "log-bytes",
		"drafts/d.json":      `{"processId":"d"}`,
		"state/CURRENT":      "should-be-skipped",
	})
	code, body := doReqBytes(t, dst, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != 200 || !strings.Contains(string(body), `"restartRequired":true`) {
		t.Fatalf("restore/full status=%d body=%s", code, body)
	}
	staging := filepath.Join(dir, ".restore-pending")
	if _, err := os.Stat(filepath.Join(staging, "wal", "seg-0000000000")); err != nil {
		t.Errorf("WAL not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "drafts", "d.json")); err != nil {
		t.Errorf("draft not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "state", "CURRENT")); !os.IsNotExist(err) {
		t.Errorf("non-allowlisted state/ entry was staged (err=%v)", err)
	}
}

// TestRestoreFullRejectsTraversal ensures a hostile archive cannot escape the staging
// dir, and the rejected upload leaves nothing behind.
func TestRestoreFullRejectsTraversal(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"../escape.txt":      "pwned",
		"wal/seg-0000000000": "x",
	})
	code, _ := doReqBytes(t, dst, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("traversal archive status=%d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("path traversal wrote outside the data dir (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatalf("rejected traversal left a staging dir (err=%v)", err)
	}
}

// TestRestoreFullRejectsGarbage rejects a non-gzip body.
func TestRestoreFullRejectsGarbage(t *testing.T) {
	dst, _ := newBackupServer(t)
	code, _ := doReqBytes(t, dst, "POST", "/api/v1/restore/full", []byte("not gzip"), "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("garbage body status=%d, want 400", code)
	}
}

// TestRestoreFullRejectsCorruptTar rejects a valid gzip wrapping invalid tar bytes.
func TestRestoreFullRejectsCorruptTar(t *testing.T) {
	dst, _ := newBackupServer(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("definitely not a tar archive")); err != nil {
		t.Fatalf("gz write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	code, _ := doReqBytes(t, dst, "POST", "/api/v1/restore/full", buf.Bytes(), "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("corrupt tar status=%d, want 400", code)
	}
}

// TestRestoreFullRejectsTooManyEntries trips the entry-count guard with entries
// outside the allowlist, so no files are staged before it fires.
func TestRestoreFullRejectsTooManyEntries(t *testing.T) {
	dst, _ := newBackupServer(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < 100_001; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: "skip/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}); err != nil {
			t.Fatalf("write header %d: %v", i, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	code, body := doReqBytes(t, dst, "POST", "/api/v1/restore/full", buf.Bytes(), "application/gzip")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "too many entries") {
		t.Fatalf("too-many-entries status=%d body=%s, want 400", code, body)
	}
}

// TestRestoreFullSkipsDirectoryEntries accepts an archive whose directory entries
// are skipped while its regular WAL file stages.
func TestRestoreFullSkipsDirectoryEntries(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"wal/":               "", // an explicit directory entry
		"wal/seg-0000000000": "log-bytes",
	})
	code, body := doReqBytes(t, dst, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != 200 || !strings.Contains(string(body), `"restartRequired":true`) {
		t.Fatalf("restore/full status=%d body=%s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, ".restore-pending", "wal", "seg-0000000000")); err != nil {
		t.Fatalf("WAL segment not staged: %v", err)
	}
}

// TestRestoreFullReportsWriteFailure surfaces a 500 when a staged file cannot be
// written (a directory already occupies its path).
func TestRestoreFullReportsWriteFailure(t *testing.T) {
	dst, _ := newBackupServer(t)
	// The archive stages "wal" as a file and "wal/seg-0/x" as a child under it;
	// whichever is written second collides (a file where a dir is needed, or a rename
	// onto a directory), surfacing a 500.
	arc := makeTarGz(t, map[string]string{
		"wal":         "i am a file, not a dir",
		"wal/seg-0/x": "child under a file",
	})
	code, _ := doReqBytes(t, dst, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != http.StatusInternalServerError {
		t.Fatalf("write-collision status=%d, want 500", code)
	}
}
