package wal_test

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// v1Frame renders one record the way the pre-batch writer did: a length, a CRC32C
// over the payload, and the payload. A segment built from these carries no format
// header, which is exactly how such a file is recognised.
func v1Frame(payload string) []byte {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum([]byte(payload), crc32.MakeTable(crc32.Castagnoli)))
	return append(hdr[:], payload...)
}

// TestLegacySegmentsStayReadable: a log written before batch framing keeps
// replaying after the upgrade, and new writes go to a fresh segment in the new
// format. An installation that upgrades does not have to discard its log.
//
// What those old records cannot be given is the guarantee the new format adds:
// they were framed one per record, so a write torn inside one of them was already
// on disk that way before this build ever saw the file. The point here is only
// that they remain readable and that nothing is silently misparsed.
func TestLegacySegmentsStayReadable(t *testing.T) {
	dir := t.TempDir()
	var seg []byte
	for _, rec := range []string{"old-1", "old-2", "old-3"} {
		seg = append(seg, v1Frame(rec)...)
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000000000.wal"), seg, 0o644); err != nil {
		t.Fatalf("write legacy segment: %v", err)
	}

	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open over a legacy log: %v", err)
	}
	defer l.Close()
	wantEntries(t, replayAll(t, l), "old-1", "old-2", "old-3")

	// New writes land in a new segment; the legacy one is left byte-for-byte alone.
	if err := l.Append([]byte("new-1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Append([]byte("new-2")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	wantEntries(t, replayAll(t, l), "old-1", "old-2", "old-3", "new-1", "new-2")

	after, err := os.ReadFile(filepath.Join(dir, "0000000000000000.wal"))
	if err != nil {
		t.Fatalf("re-read legacy segment: %v", err)
	}
	if string(after) != string(seg) {
		t.Fatal("the legacy segment was rewritten; it must be read and left alone")
	}
	segs, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2 (the legacy one plus a fresh batch-framed one)", len(segs))
	}
}

// TestLegacyAndBatchedRecordsReplayInOrder: the two formats coexist in one
// directory and replay as one log, in append order across the seam.
func TestLegacyAndBatchedRecordsReplayInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0000000000000000.wal"), v1Frame("first"), 0o644); err != nil {
		t.Fatalf("write legacy segment: %v", err)
	}
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, batch := range [][]string{{"a1", "a2"}, {"b1"}} {
		for _, rec := range batch {
			if err := l.Append([]byte(rec)); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	wantEntries(t, replayAll(t, l), "first", "a1", "a2", "b1")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening finds the batch-framed segment as the active one and keeps going.
	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	if err := l2.Append([]byte("c1")); err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if err := l2.Sync(); err != nil {
		t.Fatalf("Sync after reopen: %v", err)
	}
	wantEntries(t, replayAll(t, l2), "first", "a1", "a2", "b1", "c1")
	segs, _ := filepath.Glob(filepath.Join(dir, "*.wal"))
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2: reopening a batch-framed segment must not roll again", len(segs))
	}
}
