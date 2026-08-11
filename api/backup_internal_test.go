package api

import (
	"archive/tar"
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// These internal tests drive the defensive error branches of backup/restore that
// a normal HTTP round-trip cannot reach as root (an unreadable file, a broken
// writer). They inject faults at the seams — an fs.FS, an io.Writer, an
// io.Reader, an http.ResponseWriter — rather than relying on filesystem
// permissions, which root ignores.

// errFS wraps a filesystem and fails Open for one specific path, so a walk can
// reach a file whose contents then fail to read.
type errFS struct {
	fs.FS
	failOpen string
}

func (e errFS) Open(name string) (fs.File, error) {
	if name == e.failOpen {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("injected open failure")}
	}
	return e.FS.Open(name)
}

// truncWriter accepts up to limit bytes, then fails every write — used to make
// tar header vs. body writes fail independently.
type truncWriter struct {
	limit, n int
}

func (t *truncWriter) Write(p []byte) (int, error) {
	room := t.limit - t.n
	if room <= 0 {
		return 0, errors.New("injected write failure")
	}
	if len(p) <= room {
		t.n += len(p)
		return len(p), nil
	}
	t.n = t.limit
	return room, errors.New("injected write failure")
}

// failReader always fails, to exercise the read side of writeRestoredFile.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("injected read failure") }

// failRW is an http.ResponseWriter whose body writes always fail, so a streaming
// backup handler hits its logged error path.
type failRW struct{ h http.Header }

func (f *failRW) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}
func (f *failRW) Write([]byte) (int, error) { return 0, errors.New("injected write failure") }
func (f *failRW) WriteHeader(int)           {}

func TestWriteBackupWalkError(t *testing.T) {
	// Opening the first allowlisted directory fails with a non-not-exist error,
	// which the walk must surface rather than swallow.
	fsys := errFS{FS: fstest.MapFS{}, failOpen: backupDirs[0]}
	if err := writeBackup(tar.NewWriter(&bytes.Buffer{}), fsys); err == nil {
		t.Fatal("writeBackup: want error from a failed directory walk, got nil")
	}
}

func TestWriteBackupReadFileError(t *testing.T) {
	fsys := errFS{
		FS:       fstest.MapFS{"drafts/a.json": {Data: []byte("x")}},
		failOpen: "drafts/a.json",
	}
	if err := writeBackup(tar.NewWriter(&bytes.Buffer{}), fsys); err == nil {
		t.Fatal("writeBackup: want error from an unreadable file, got nil")
	}
}

func TestWriteBackupHeaderAndBodyWriteErrors(t *testing.T) {
	fsys := fstest.MapFS{"drafts/a.json": {Data: []byte("hello")}}
	// limit 0: the very first (header) write fails.
	if err := writeBackup(tar.NewWriter(&truncWriter{limit: 0}), fsys); err == nil {
		t.Fatal("writeBackup: want error from a failed header write, got nil")
	}
	// limit 512: the 512-byte header succeeds, the body write then fails.
	if err := writeBackup(tar.NewWriter(&truncWriter{limit: 512}), fsys); err == nil {
		t.Fatal("writeBackup: want error from a failed body write, got nil")
	}
}

func TestStreamBackupSurfacesWriteBackupError(t *testing.T) {
	fsys := errFS{FS: fstest.MapFS{}, failOpen: backupDirs[0]}
	if err := streamBackup(&bytes.Buffer{}, fsys); err == nil {
		t.Fatal("streamBackup: want the underlying walk error, got nil")
	}
}

func TestHandleBackupLogsStreamFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "a.json"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{dataDir: dir}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil)
	// A failing ResponseWriter drives the handler's "streaming failed" branch; the
	// call must return without panicking.
	s.handleBackup(&failRW{}, req)
}

func TestBackupRestoreRequireAdmin(t *testing.T) {
	s := &Server{authEnabled: true}
	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		meth string
	}{
		{"backup", s.handleBackup, http.MethodGet},
		{"restore", s.handleRestore, http.MethodPost},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.meth, "/", nil)
		tc.fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without an admin principal: status=%d, want 403", tc.name, rec.Code)
		}
	}
}

func TestWriteRestoredFileErrors(t *testing.T) {
	dir := t.TempDir()

	// ReadAll fails: the source reader errors mid-stream.
	if err := writeRestoredFile(filepath.Join(dir, "drafts", "r.json"), failReader{}); err == nil {
		t.Error("writeRestoredFile: want error from a failing reader, got nil")
	}

	// MkdirAll fails: a regular file occupies a parent path component.
	if err := os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644); err != nil {
		t.Fatalf("plant blocker file: %v", err)
	}
	if err := writeRestoredFile(filepath.Join(dir, "blocker", "child.json"), bytes.NewReader([]byte("x"))); err == nil {
		t.Error("writeRestoredFile: want error when a parent path is a file, got nil")
	}

	// WriteFile(tmp) fails: a directory occupies the temp path.
	if err := os.MkdirAll(filepath.Join(dir, "drafts", "w.json.tmp"), 0o755); err != nil {
		t.Fatalf("plant tmp dir: %v", err)
	}
	if err := writeRestoredFile(filepath.Join(dir, "drafts", "w.json"), bytes.NewReader([]byte("x"))); err == nil {
		t.Error("writeRestoredFile: want error when the temp path is a directory, got nil")
	}

	// Rename fails: a directory occupies the final path.
	if err := os.MkdirAll(filepath.Join(dir, "drafts", "final.json"), 0o755); err != nil {
		t.Fatalf("plant final dir: %v", err)
	}
	if err := writeRestoredFile(filepath.Join(dir, "drafts", "final.json"), bytes.NewReader([]byte("x"))); err == nil {
		t.Error("writeRestoredFile: want error when the final path is a directory, got nil")
	}
}
