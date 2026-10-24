package wal

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// faultyFile is a segment whose disk runs out part-way: it accepts writeBudget more
// bytes and then fails the way a full file system does, with the bytes it took
// already in the file. That short write is what a real ENOSPC leaves behind and what
// a closed descriptor never does, which is why it needs a seam of its own.
type faultyFile struct {
	*os.File
	writeBudget  int // bytes still accepted; negative means no limit
	failSync     bool
	failTruncate bool
}

func (f *faultyFile) Write(p []byte) (int, error) {
	if f.writeBudget < 0 || len(p) <= f.writeBudget {
		if f.writeBudget >= 0 {
			f.writeBudget -= len(p)
		}
		return f.File.Write(p)
	}
	n, err := f.File.Write(p[:f.writeBudget])
	f.writeBudget = 0
	if err != nil {
		return n, err
	}
	return n, syscall.ENOSPC
}

func (f *faultyFile) Sync() error {
	if f.failSync {
		return syscall.EIO
	}
	return f.File.Sync()
}

func (f *faultyFile) Truncate(size int64) error {
	if f.failTruncate {
		return syscall.EIO
	}
	return f.File.Truncate(size)
}

// injectFault wraps the log's active segment so its next writes go through f.
func injectFault(t *testing.T, l *Log, f *faultyFile) {
	t.Helper()
	file, ok := l.active.(*os.File)
	if !ok {
		t.Fatalf("active segment is %T, want *os.File", l.active)
	}
	f.File = file
	l.active = f
}

func appendSync(t *testing.T, l *Log, data string) {
	t.Helper()
	if err := l.Append([]byte(data)); err != nil {
		t.Fatalf("Append(%q): %v", data, err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync(%q): %v", data, err)
	}
}

func replayAll(t *testing.T, dir string) []string {
	t.Helper()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopening the log after the failure: %v", err)
	}
	defer l.Close()
	var got []string
	if err := l.Replay(func(data []byte) error {
		got = append(got, string(data))
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return got
}

// TestAWriteCutShortByAFullDiskLeavesTheLogWritable is the disk-full case end to end:
// a batch the disk could only partly take fails, space comes back, the log carries on,
// and a restart reads exactly the batches that succeeded.
//
// Before the failed write was rolled back, its torn bytes stayed in the segment and the
// next batch was written behind them. Recovery then found damage with data after it —
// which no crash can leave, so it rightly refused to start — and a server that had
// lived through a full disk could not be restarted at all.
func TestAWriteCutShortByAFullDiskLeavesTheLogWritable(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendSync(t, l, "before")

	fault := &faultyFile{writeBudget: 5}
	injectFault(t, l, fault)
	if err := l.Append([]byte("refused by the full disk")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Sync on a full disk = %v, want ENOSPC", err)
	}

	fault.writeBudget = -1 // the operator freed space
	appendSync(t, l, "after")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := replayAll(t, dir)
	want := []string{"before", "after"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("replayed %q, want %q: the refused batch must leave no trace and the next one must survive", got, want)
	}
}

// TestAFailedWriteDropsTheBatchItStaged: the batch that failed is not written again
// with the next one. A caller that was told its batch failed must be able to rely on
// that, and a log that kept the staged bytes would hand them to the next Sync.
func TestAFailedWriteDropsTheBatchItStaged(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	fault := &faultyFile{writeBudget: 0}
	injectFault(t, l, fault)
	if err := l.Append([]byte("lost")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync on a full disk succeeded")
	}
	fault.writeBudget = -1
	appendSync(t, l, "kept")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := replayAll(t, dir); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("replayed %q, want only %q", got, "kept")
	}
}

// TestAFailedFsyncStopsTheLog: after a failed fsync nothing can be known about what
// reached the disk — the kernel may already have dropped the dirty pages it could not
// write, so a later fsync that succeeds proves nothing. The log refuses every further
// batch rather than write behind bytes whose fate is unknown.
func TestAFailedFsyncStopsTheLog(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Closed before the temp dir is removed: Windows will not delete an open file.
	t.Cleanup(func() { _ = l.Close() })
	appendSync(t, l, "before")
	fault := &faultyFile{writeBudget: -1, failSync: true}
	injectFault(t, l, fault)
	if err := l.Append([]byte("unknown")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); !errors.Is(err, syscall.EIO) {
		t.Fatalf("Sync with a failing fsync = %v, want EIO", err)
	}
	size := fileSize(t, l)

	fault.failSync = false
	if err := l.Append([]byte("refused")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync after a failed fsync succeeded; the log must refuse further batches")
	}
	if got := fileSize(t, l); got != size {
		t.Fatalf("segment grew from %d to %d bytes after the log stopped", size, got)
	}
	if err := l.Err(); !errors.Is(err, syscall.EIO) {
		t.Fatalf("Err() = %v, want the fsync failure", err)
	}
}

// TestAFailedRollbackStopsTheLog: when the torn bytes of a failed write cannot be cut
// off again, the log must not write behind them — that is the damage recovery refuses.
func TestAFailedRollbackStopsTheLog(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendSync(t, l, "before")
	fault := &faultyFile{writeBudget: 3, failTruncate: true}
	injectFault(t, l, fault)
	if err := l.Append([]byte("torn")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Sync = %v, want ENOSPC", err)
	}
	fault.writeBudget = -1
	if err := l.Append([]byte("refused")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync behind torn bytes that could not be cut off succeeded")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Nothing was written behind the torn bytes, so they are a torn tail recovery may
	// drop, and the log opens.
	if got := replayAll(t, dir); len(got) != 1 || got[0] != "before" {
		t.Fatalf("replayed %q, want only %q", got, "before")
	}
}

// TestDiscardDropsStagedRecords: a caller that gives up on a batch before syncing it
// can drop what it staged, so the next batch does not carry it.
func TestDiscardDropsStagedRecords(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("abandoned")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Discard()
	appendSync(t, l, "kept")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := replayAll(t, dir); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("replayed %q, want only %q", got, "kept")
	}
}

func fileSize(t *testing.T, l *Log) int64 {
	t.Helper()
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".wal" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		total += info.Size()
	}
	return total
}

// TestAFailedRollStopsTheLog: when the next segment cannot be created, the old one is
// already closed and the new one may hold a torn header. Nothing may be written from
// there, and closing the log afterwards has nothing left to close twice.
func TestAFailedRollStopsTheLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	l, err := Open(Options{Dir: dir, MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendSync(t, l, "first")
	// A directory where the next segment belongs, so creating that segment fails on
	// every platform without deleting anything under an open file, which Windows
	// refuses.
	if err := os.Mkdir(filepath.Join(dir, segmentName(1)), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := l.Append([]byte("second")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync rolled into a segment that could not be created")
	}
	if l.Err() == nil {
		t.Fatal("Err() = nil after a failed roll; the log must stop")
	}
	if err := l.Append([]byte("third")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("Sync after a failed roll succeeded")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close after a failed roll: %v", err)
	}
}
