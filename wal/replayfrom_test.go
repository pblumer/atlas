package wal_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// The WAL is record-format agnostic, so these tests use a minimal stand-in payload:
// an 8-byte little-endian log position followed by a marker byte.

func posPayload(pos uint64) []byte {
	b := make([]byte, 9)
	binary.LittleEndian.PutUint64(b, pos)
	b[8] = 0x2A
	return b
}

func payloadPos(data []byte) (uint64, error) {
	if len(data) < 8 {
		return 0, errors.New("short payload")
	}
	return binary.LittleEndian.Uint64(data), nil
}

// openRolling opens a log that rolls to a new segment on every Sync, so each appended
// record lands in a segment of its own and segment skipping is observable exactly.
func openRolling(t *testing.T, dir string) *wal.Log {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: dir, MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// writePositions appends each position as its own fsynced batch.
func writePositions(t *testing.T, l *wal.Log, positions ...uint64) {
	t.Helper()
	for _, pos := range positions {
		if err := l.Append(posPayload(pos)); err != nil {
			t.Fatalf("Append %d: %v", pos, err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync %d: %v", pos, err)
		}
	}
}

func replayedPositions(t *testing.T, l *wal.Log, after uint64) []uint64 {
	t.Helper()
	var got []uint64
	if err := l.ReplayFrom(after, payloadPos, func(data []byte) error {
		pos, err := payloadPos(data)
		if err != nil {
			return err
		}
		got = append(got, pos)
		return nil
	}); err != nil {
		t.Fatalf("ReplayFrom(%d): %v", after, err)
	}
	return got
}

func equalPositions(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReplayFromSkipsWholeSegments: with one record per segment, replaying after a
// position delivers exactly the records past it — the earlier segments are skipped
// rather than read.
func TestReplayFromSkipsWholeSegments(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3, 4, 5, 6)

	if got := replayedPositions(t, l, 3); !equalPositions(got, []uint64{4, 5, 6}) {
		t.Fatalf("ReplayFrom(3) = %v, want [4 5 6]", got)
	}
	// after = 0 means the whole log, i.e. plain Replay.
	if got := replayedPositions(t, l, 0); !equalPositions(got, []uint64{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("ReplayFrom(0) = %v, want every record", got)
	}
	// A cut past the end still replays the final segment: it has no successor to bound
	// its extent, so it can never be proven skippable. Filtering is the caller's job.
	if got := replayedPositions(t, l, 99); !equalPositions(got, []uint64{6}) {
		t.Fatalf("ReplayFrom(99) = %v, want just the unbounded final segment [6]", got)
	}
}

// TestReplayFromNeverReadsSkippedSegments: the segments below the cut are deleted, and
// the replay still succeeds and returns the suffix — proof the skipping is real and
// not merely a filter, which is what makes WAL compaction possible (ADR-0131).
func TestReplayFromNeverReadsSkippedSegments(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3, 4, 5, 6)

	segs, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(segs) < 4 {
		t.Fatalf("expected a segment per record, got %d", len(segs))
	}
	// Delete the segments holding positions 1..3 (the first three, in name order).
	for _, seg := range segs[:3] {
		if err := os.Remove(seg); err != nil {
			t.Fatalf("remove %s: %v", seg, err)
		}
	}
	if got := replayedPositions(t, l, 3); !equalPositions(got, []uint64{4, 5, 6}) {
		t.Fatalf("ReplayFrom(3) after deleting the prefix = %v, want [4 5 6]", got)
	}
}

// TestReplayFromBoundarySegmentIsReplayedWhole: when a segment straddles the cut, it is
// replayed in full — ReplayFrom skips whole segments only, and filtering the few
// records at or below the cut is the caller's job (as documented).
func TestReplayFromBoundarySegmentIsReplayedWhole(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir}) // default size: everything in one segment
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer l.Close()
	writePositions(t, l, 1, 2, 3)

	if got := replayedPositions(t, l, 2); !equalPositions(got, []uint64{1, 2, 3}) {
		t.Fatalf("ReplayFrom(2) over a single segment = %v, want the whole segment", got)
	}
}

// TestReplayFromEmptyLog: a log with no records replays nothing, at any cut.
func TestReplayFromEmptyLog(t *testing.T) {
	l := openRolling(t, t.TempDir())
	if got := replayedPositions(t, l, 5); len(got) != 0 {
		t.Fatalf("ReplayFrom on an empty log = %v, want nothing", got)
	}
}

// TestReplayFromPropagatesCallbackError: an error from fn stops the replay and is
// returned, matching Replay.
func TestReplayFromPropagatesCallbackError(t *testing.T) {
	l := openRolling(t, t.TempDir())
	writePositions(t, l, 1, 2, 3, 4)
	boom := errors.New("stop here")
	err := l.ReplayFrom(2, payloadPos, func([]byte) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("ReplayFrom err = %v, want the callback error", err)
	}
}

// TestReplayFromPropagatesPositionError: a payload the caller cannot decode is a hard
// error, not a silently skipped segment — a corrupt head must not look like an empty
// one and quietly widen the replay.
func TestReplayFromPropagatesPositionError(t *testing.T) {
	l := openRolling(t, t.TempDir())
	writePositions(t, l, 1, 2, 3)
	bad := errors.New("undecodable")
	err := l.ReplayFrom(2, func([]byte) (uint64, error) { return 0, bad }, func([]byte) error { return nil })
	if !errors.Is(err, bad) {
		t.Fatalf("ReplayFrom err = %v, want the position-decode error", err)
	}
}
