package wal_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// TestOpenRequiresDir covers the empty-Dir guard.
func TestOpenRequiresDir(t *testing.T) {
	if _, err := wal.Open(wal.Options{}); err == nil {
		t.Fatal("Open with empty Dir: got nil error, want an error")
	}
}

// TestOpenMkdirAllError covers the directory-creation failure path: a path whose
// parent component is a regular file cannot be created.
func TestOpenMkdirAllError(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := wal.Open(wal.Options{Dir: filepath.Join(file, "sub")}); err == nil {
		t.Fatal("Open under a file: got nil error, want a mkdir error")
	}
}

// TestOpenBadSegmentName covers the parseSeq failure path: a .wal file whose name
// is not a segment sequence number.
func TestOpenBadSegmentName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notanumber.wal"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := wal.Open(wal.Options{Dir: dir}); err == nil {
		t.Fatal("Open with a non-numeric segment name: got nil error, want a parse error")
	}
}

// TestOpenNewSegmentError covers Open's openNewSegment failure (and thus
// openNewSegment's OpenFile failure): a directory pre-occupies the first
// segment's path, so creating it as a file fails. segmentFiles skips the
// directory, so the log takes the "no segments" branch.
func TestOpenNewSegmentError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "0000000000000000.wal"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := wal.Open(wal.Options{Dir: dir}); err == nil {
		t.Fatal("Open with a directory at the segment path: got nil error, want a create error")
	}
}

// TestOpenLastSegmentOpenError covers Open's OpenFile failure for an existing
// last segment: a symlink with a valid segment name pointing at a directory makes
// the read-write open fail with EISDIR.
func TestOpenLastSegmentOpenError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(dir, "0000000000000000.wal")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := wal.Open(wal.Options{Dir: dir}); err == nil {
		t.Fatal("Open of a segment symlinked to a directory: got nil error, want an open error")
	}
}

// TestOpenTruncateError covers Open's Truncate failure: a segment symlinked to
// /dev/null opens and reads (empty) fine, but truncating it fails with EINVAL.
func TestOpenTruncateError(t *testing.T) {
	if _, err := os.Stat(os.DevNull); err != nil {
		t.Skipf("no %s", os.DevNull)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "0000000000000000.wal")
	if err := os.Symlink(os.DevNull, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := wal.Open(wal.Options{Dir: dir}); err == nil {
		t.Fatal("Open of a segment symlinked to /dev/null: got nil error, want a truncate error")
	}
}

// TestAppendRejectsOversizeRecord covers the record-too-large guard.
func TestAppendRejectsOversizeRecord(t *testing.T) {
	l, err := wal.Open(wal.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	oversize := make([]byte, (64<<20)+1)
	if err := l.Append(oversize); err == nil {
		t.Fatal("Append of an oversize record: got nil error, want an error")
	}
}

// TestCloseIsIdempotent covers Close's nil-active fast path: a second Close is a
// clean no-op.
func TestCloseIsIdempotent(t *testing.T) {
	l, err := wal.Open(wal.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v, want nil", err)
	}
}

// TestReplaySegmentFilesError covers Replay's segmentFiles failure: removing the
// log directory makes the directory listing fail.
func TestReplaySegmentFilesError(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("x")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := l.Replay(func([]byte) error { return nil }); err == nil {
		t.Fatal("Replay after directory removal: got nil error, want a listing error")
	}
	l.Close()
}

// TestReplayStopsOnCallbackError covers the path where the replay callback fails:
// replaySegment surfaces it and Replay returns it.
func TestReplayStopsOnCallbackError(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	for _, s := range []string{"a", "b", "c"} {
		if err := l.Append([]byte(s)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	sentinel := errors.New("stop replay")
	calls := 0
	err = l.Replay(func([]byte) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Replay error = %v, want it to wrap sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("callback called %d times, want 1 (stops on first error)", calls)
	}
}

// TestRecoverStopsAtZeroLengthFrame covers readFrames' zero-length (zeroed tail)
// branch: a crash can leave a run of zero bytes, which the reader treats as end
// of log.
func TestRecoverStopsAtZeroLengthFrame(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("good")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A zero-length frame header (8 zero bytes) mimics the zeroed tail a crash can
	// leave in a preallocated segment.
	paths := segmentPaths(t, dir)
	last := paths[len(paths)-1]
	f, err := os.OpenFile(last, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := f.Write(make([]byte, 8)); err != nil {
		t.Fatalf("write zero frame: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}

	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	wantEntries(t, replayAll(t, l2), "good")
}

// TestRecoverStopsAtCrcMismatch covers readFrames' CRC-mismatch branch: a fully
// written frame whose payload does not match its checksum is treated as a torn
// tail and dropped.
func TestRecoverStopsAtCrcMismatch(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("good")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A complete frame (length + payload) but with a deliberately wrong CRC.
	paths := segmentPaths(t, dir)
	last := paths[len(paths)-1]
	f, err := os.OpenFile(last, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], 3)          // payload length 3
	binary.LittleEndian.PutUint32(hdr[4:], 0xFFFFFFFF) // wrong CRC
	if _, err := f.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := f.Write([]byte("bad")); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}

	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	wantEntries(t, replayAll(t, l2), "good")
}
