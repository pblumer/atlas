package wal_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/chrampfer/wal"
)

// replayAll collects every durable record in order.
func replayAll(t *testing.T, l *wal.Log) [][]byte {
	t.Helper()
	var got [][]byte
	if err := l.Replay(func(data []byte) error {
		got = append(got, append([]byte(nil), data...))
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return got
}

func wantEntries(t *testing.T, got [][]byte, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func segmentPaths(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wal") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

func TestAppendSyncReplay(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, s := range []string{"alpha", "beta", "gamma"} {
		if err := l.Append([]byte(s)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	wantEntries(t, replayAll(t, l), "alpha", "beta", "gamma")

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUnsyncedAppendsAreNotDurable(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("durable")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Stage another record but do not Sync it.
	if err := l.Append([]byte("lost")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: only the synced record is durable.
	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	wantEntries(t, replayAll(t, l2), "durable")
}

func TestGroupCommitManyAppendsOneSync(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	const n = 1000
	for i := range n {
		if err := l.Append(fmt.Appendf(nil, "rec-%04d", i)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := l.Sync(); err != nil { // a single fsync for the whole batch
		t.Fatalf("Sync: %v", err)
	}

	got := replayAll(t, l)
	if len(got) != n {
		t.Fatalf("got %d records, want %d", len(got), n)
	}
	for i := range n {
		if want := fmt.Sprintf("rec-%04d", i); string(got[i]) != want {
			t.Fatalf("record %d = %q, want %q", i, got[i], want)
		}
	}
}

func TestSegmentRolling(t *testing.T) {
	dir := t.TempDir()
	// Tiny segments so a handful of records spill across several files.
	l, err := wal.Open(wal.Options{Dir: dir, MaxSegmentSize: 32})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var want []string
	for i := range 20 {
		s := fmt.Sprintf("entry-%02d", i)
		want = append(want, s)
		if err := l.Append([]byte(s)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.Sync(); err != nil { // Sync per record to exercise rolling
			t.Fatalf("Sync: %v", err)
		}
	}

	if paths := segmentPaths(t, dir); len(paths) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(paths))
	}

	got := replayAll(t, l)
	wantEntries(t, got, want...)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecoverFromTornTail(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, s := range []string{"one", "two"} {
		if err := l.Append([]byte(s)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a crash mid-batch: a frame header promising 100 bytes followed
	// by only a partial payload, appended to the last segment.
	paths := segmentPaths(t, dir)
	last := paths[len(paths)-1]
	f, err := os.OpenFile(last, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], 100)
	binary.LittleEndian.PutUint32(hdr[4:], 0xDEADBEEF)
	if _, err := f.Write(hdr[:]); err != nil {
		t.Fatalf("write torn header: %v", err)
	}
	if _, err := f.Write([]byte("partial")); err != nil {
		t.Fatalf("write torn payload: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}

	// Reopen: torn tail is dropped, the two durable records survive.
	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	wantEntries(t, replayAll(t, l2), "one", "two")

	// Appends after recovery must be readable, which proves the torn tail was
	// truncated rather than left in front of new records.
	if err := l2.Append([]byte("three")); err != nil {
		t.Fatalf("Append after recovery: %v", err)
	}
	if err := l2.Sync(); err != nil {
		t.Fatalf("Sync after recovery: %v", err)
	}
	if err := l2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l3, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen 2: %v", err)
	}
	defer l3.Close()
	wantEntries(t, replayAll(t, l3), "one", "two", "three")
}

func TestAppendRejectsEmptyRecord(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if err := l.Append(nil); err == nil {
		t.Error("Append(nil) = nil, want error")
	}
	if err := l.Append([]byte{}); err == nil {
		t.Error("Append(empty) = nil, want error")
	}
}

func TestReplayEmpty(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if got := replayAll(t, l); len(got) != 0 {
		t.Fatalf("empty log replayed %d entries", len(got))
	}
	// Sync with nothing staged is a no-op.
	if err := l.Sync(); err != nil {
		t.Fatalf("empty Sync: %v", err)
	}
}

func TestBinaryPayloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	// Payloads with embedded NULs and high bytes must survive intact.
	payloads := [][]byte{
		{0x00, 0x01, 0x02, 0xFF},
		bytes.Repeat([]byte{0xAB}, 300),
		{0x7F},
	}
	for _, p := range payloads {
		if err := l.Append(p); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := replayAll(t, l)
	if len(got) != len(payloads) {
		t.Fatalf("got %d entries, want %d", len(got), len(payloads))
	}
	for i, p := range payloads {
		if !bytes.Equal(got[i], p) {
			t.Errorf("entry %d = % x, want % x", i, got[i], p)
		}
	}
}
