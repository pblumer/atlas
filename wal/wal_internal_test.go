package wal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// errReader always fails with a non-EOF error, driving readFrames' read-error
// return (as opposed to the clean torn-tail EOF handling).
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

// TestReadFramesReadError covers the branch where reading a frame header fails
// with a genuine I/O error rather than EOF: readFrames surfaces it.
func TestReadFramesReadError(t *testing.T) {
	n, err := readFrames(errReader{}, nil)
	if err == nil {
		t.Fatal("readFrames over an erroring reader: got nil error, want it surfaced")
	}
	if n != 0 {
		t.Fatalf("consumed = %d, want 0", n)
	}
}

// TestReplaySegmentOpenError covers replaySegment's os.Open failure path.
func TestReplaySegmentOpenError(t *testing.T) {
	if err := replaySegment(filepath.Join(t.TempDir(), "absent.wal"), nil); err == nil {
		t.Fatal("replaySegment of a missing file: got nil error, want an open error")
	}
}

// TestSyncWriteError covers Sync's write-failure return: the active segment's
// descriptor is closed out from under the log, so the batch write fails.
func TestSyncWriteError(t *testing.T) {
	l, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("data")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.active.Close() // no roll will happen (activeSize < max), so Write fails first
	if err := l.Sync(); err == nil {
		t.Fatal("Sync after underlying close: got nil error, want a write error")
	}
}

// TestSyncFsyncError covers Sync's fsync-failure return. Writes to /dev/null
// succeed but fsync on it fails with EINVAL, isolating the fsync branch.
func TestSyncFsyncError(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		t.Skipf("no %s: %v", os.DevNull, err)
	}
	defer devnull.Close()
	l := &Log{dir: t.TempDir(), active: devnull, maxSegmentSize: 1 << 30}
	if err := l.Append([]byte("data")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync writing to /dev/null: got nil error, want an fsync error")
	}
}

// TestRollError covers roll's failure (and Sync's handling of it): the active
// segment is already closed, so the roll's Close fails when a size-triggered roll
// fires.
func TestRollError(t *testing.T) {
	l, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l.active.Close()     // roll's Close of the active segment will fail
	l.activeSize = 100   // force the size-triggered roll
	l.maxSegmentSize = 1 // ...on the next Sync
	if err := l.Append([]byte("data")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync that must roll a broken segment: got nil error, want a roll error")
	}
}

// TestSyncDirError covers syncDir's os.Open failure path via a directory that
// does not exist.
func TestSyncDirError(t *testing.T) {
	l := &Log{dir: filepath.Join(t.TempDir(), "does-not-exist")}
	if err := l.syncDir(); err == nil {
		t.Fatal("syncDir of a missing directory: got nil error, want an open error")
	}
}
