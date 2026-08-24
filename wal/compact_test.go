package wal_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

func segmentCount(t *testing.T, dir string) int {
	t.Helper()
	segs, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return len(segs)
}

// TestCompactDeletesOnlyFullyCoveredSegments: with one record per segment, compacting
// after position 3 removes exactly the three segments below it and the surviving log
// replays the rest.
func TestCompactDeletesOnlyFullyCoveredSegments(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3, 4, 5, 6)
	before := segmentCount(t, dir)

	removed, err := l.Compact(3, payloadPos)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed %d segments, want 3", removed)
	}
	if got := segmentCount(t, dir); got != before-3 {
		t.Fatalf("segments left = %d, want %d", got, before-3)
	}
	// Everything past the cut survives, and a plain full replay now yields exactly it.
	if got := replayedPositions(t, l, 0); !equalPositions(got, []uint64{4, 5, 6}) {
		t.Fatalf("Replay after compaction = %v, want [4 5 6]", got)
	}
}

// TestCompactMatchesReplayFromSkip is the safety property that makes compaction sound:
// deleting a segment must never remove anything a recovery would have read. For every
// cut, the records ReplayFrom(cut) yields are identical before and after compacting at
// that same cut — the deleted set is exactly the skipped set.
func TestCompactMatchesReplayFromSkip(t *testing.T) {
	for _, cut := range []uint64{1, 2, 3, 4, 5, 6, 7} {
		dir := t.TempDir()
		l := openRolling(t, dir)
		writePositions(t, l, 1, 2, 3, 4, 5, 6)

		want := replayedPositions(t, l, cut)
		if _, err := l.Compact(cut, payloadPos); err != nil {
			t.Fatalf("Compact(%d): %v", cut, err)
		}
		if got := replayedPositions(t, l, cut); !equalPositions(got, want) {
			t.Fatalf("cut %d: ReplayFrom after compaction = %v, want the pre-compaction %v", cut, got, want)
		}
	}
}

// TestCompactNeverDeletesActiveSegment: even a cut far past every record leaves the
// segment being written, so the log stays a valid, appendable log.
func TestCompactNeverDeletesActiveSegment(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3)

	if _, err := l.Compact(99999, payloadPos); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := segmentCount(t, dir); got < 1 {
		t.Fatal("compaction removed every segment, leaving no log")
	}
	// The log still accepts writes and replays them.
	writePositions(t, l, 7)
	got := replayedPositions(t, l, 0)
	if len(got) == 0 || got[len(got)-1] != 7 {
		t.Fatalf("after compacting and appending, replay = %v, want it to end at 7", got)
	}
}

// TestCompactZeroIsNoop: a zero cut means "nothing is provably redundant", so nothing
// is deleted — the state a missing or unusable checkpoint puts the caller in.
func TestCompactZeroIsNoop(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3)
	before := segmentCount(t, dir)

	removed, err := l.Compact(0, payloadPos)
	if err != nil {
		t.Fatalf("Compact(0): %v", err)
	}
	if removed != 0 || segmentCount(t, dir) != before {
		t.Fatalf("Compact(0) removed %d segments, want none", removed)
	}
}

// TestCompactSingleSegmentKeepsIt: with everything in one segment there is nothing that
// can be proven redundant, so the log is untouched.
func TestCompactSingleSegmentKeepsIt(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer l.Close()
	writePositions(t, l, 1, 2, 3)

	removed, err := l.Compact(3, payloadPos)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d segments from a single-segment log, want 0", removed)
	}
	if got := replayedPositions(t, l, 0); !equalPositions(got, []uint64{1, 2, 3}) {
		t.Fatalf("replay = %v, want the whole log intact", got)
	}
}

// TestCompactPropagatesPositionError: a segment head that cannot be decoded aborts the
// compaction instead of guessing — nothing is deleted on a bad read.
func TestCompactPropagatesPositionError(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3, 4)
	before := segmentCount(t, dir)

	bad := errStub{}
	if _, err := l.Compact(3, func([]byte) (uint64, error) { return 0, bad }); err == nil {
		t.Fatal("Compact accepted an undecodable segment head")
	}
	if got := segmentCount(t, dir); got != before {
		t.Fatalf("a failed compaction deleted %d segments, want none", before-got)
	}
}

type errStub struct{}

func (errStub) Error() string { return "undecodable" }
