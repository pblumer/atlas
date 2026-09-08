package wal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// sealedLog writes three single-record batches into three segments, so the first
// two are sealed and the third is active, and returns the segment paths.
func sealedLog(t *testing.T, dir string) []string {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: dir, MaxSegmentSize: 1}) // roll every batch
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, rec := range []string{"A", "B", "C"} {
		if err := l.Append([]byte(rec)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("fixture produced %d segments, want 3", len(paths))
	}
	return paths
}

// flipByteAt corrupts one byte of a file.
func flipByteAt(t *testing.T, path string, off int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.ReadAt(b[:], off); err != nil {
		t.Fatalf("read at %d: %v", off, err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b[:], off); err != nil {
		t.Fatalf("write at %d: %v", off, err)
	}
}

// TestCorruptionInASealedSegmentIsAnError is F03. A segment that has rolled is
// finished: every batch in it was written whole and fsynced before the next
// segment existed, so nothing in it can legitimately be torn. Damage there is
// damage, and treating it as a tail turns a detectable integrity failure into a
// successful recovery with a hole in it.
//
// The hole is the dangerous part. Replay does not stop at the damaged segment —
// it moves on to the next one — so the log comes back as A, C: an event that
// happened is silently absent, and the state derived from it silently disagrees
// with the history that produced it.
func TestCorruptionInASealedSegmentIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		seg  int
	}{
		{"first segment", 0},
		{"middle segment", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := sealedLog(t, dir)
			// Byte 25 is inside the batch payload: past the 16-byte segment header
			// and the 8-byte batch header.
			flipByteAt(t, paths[tc.seg], 25)

			l, err := wal.Open(wal.Options{Dir: dir})
			if err != nil {
				if !strings.Contains(err.Error(), filepath.Base(paths[tc.seg])) {
					t.Errorf("Open error %q does not name the damaged segment", err)
				}
				return
			}
			defer l.Close()
			var got []string
			rerr := l.Replay(func(data []byte) error {
				got = append(got, string(data))
				return nil
			})
			if rerr == nil {
				t.Fatalf("replay of a damaged sealed segment returned %q and no error", got)
			}
			if !strings.Contains(rerr.Error(), filepath.Base(paths[tc.seg])) {
				t.Errorf("error %q does not name the damaged segment", rerr)
			}
		})
	}
}

// TestTornTailOfTheActiveSegmentIsStillFine: the one place a partial write can
// legitimately be is the very end of the segment being written. That must stay
// tolerated, or every crash becomes an unrecoverable log.
func TestTornTailOfTheActiveSegmentIsStillFine(t *testing.T) {
	dir := t.TempDir()
	paths := sealedLog(t, dir)
	active := paths[len(paths)-1]
	st, err := os.Stat(active)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := os.Truncate(active, st.Size()-2); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open over a torn active tail: %v", err)
	}
	defer l.Close()
	wantEntries(t, replayAll(t, l), "A", "B")
}

// TestCorruptionFollowedByDataIsNotATail: even in the active segment, a damaged
// batch with more batches after it cannot be the tail a crash left — a crash
// stops writing, it does not write past the damage. Accepting it would drop a
// batch out of the middle of the log and call the result a clean recovery.
func TestCorruptionFollowedByDataIsNotATail(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, rec := range []string{"first", "second", "third"} {
		if err := l.Append([]byte(rec)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	l.Close()

	// Damage the middle batch's payload; the third batch still follows it.
	seg := filepath.Join(dir, "0000000000000000.wal")
	flipByteAt(t, seg, 16+8+5+8+2) // header, batch 1, into batch 2's payload

	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		return // refusing at Open is the loud answer too
	}
	defer l2.Close()
	var got []string
	if rerr := l2.Replay(func(data []byte) error {
		got = append(got, string(data))
		return nil
	}); rerr == nil {
		t.Fatalf("replay returned %q and no error; damage with data after it is not a torn tail", got)
	}
}

// TestAMissingSegmentInTheMiddleIsAnError: compaction removes a prefix, so the
// segments that remain are contiguous. A hole in the middle is a segment that
// went missing some other way, and replaying across it would skip whatever it
// held without a word.
func TestAMissingSegmentInTheMiddleIsAnError(t *testing.T) {
	dir := t.TempDir()
	paths := sealedLog(t, dir)
	if err := os.Remove(paths[1]); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		return
	}
	defer l.Close()
	var got []string
	if rerr := l.Replay(func(data []byte) error {
		got = append(got, string(data))
		return nil
	}); rerr == nil {
		t.Fatalf("replay across a missing segment returned %q and no error", got)
	}
}

// TestCompactedPrefixIsNotAHole: deleting the oldest segments is what compaction
// does, and the remaining suffix is a valid log. The continuity check must not
// mistake it for damage.
func TestCompactedPrefixIsNotAHole(t *testing.T) {
	dir := t.TempDir()
	paths := sealedLog(t, dir)
	if err := os.Remove(paths[0]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open over a compacted prefix: %v", err)
	}
	defer l.Close()
	wantEntries(t, replayAll(t, l), "B", "C")
}

// TestEveryKindOfDamageInASealedSegmentIsReported: the acceptance criterion asks
// for checksum, length and truncation faults, not just the checksum one. A sealed
// segment cannot have been left in any of those shapes by a crash, so all three
// are damage and all three must say so.
//
// The shapes differ in how the reader meets them — a bad length is rejected before
// any payload is read, a short payload runs out mid-read, a bad checksum needs the
// whole batch first — and each took its own path to the "is this a tail?" decision.
// Testing one of the three would have left the other two free to be wrong.
func TestEveryKindOfDamageInASealedSegmentIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, path string)
	}{
		{
			name: "checksum does not match",
			damage: func(t *testing.T, path string) {
				flipByteAt(t, path, 25) // inside the batch payload
			},
		},
		{
			name: "length prefix is nonsense",
			damage: func(t *testing.T, path string) {
				f, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				defer f.Close()
				// Zero the batch length, the shape a partially-zeroed write leaves.
				if _, err := f.WriteAt([]byte{0, 0, 0, 0}, segmentHeaderBytes); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
		},
		{
			name: "payload cut short",
			damage: func(t *testing.T, path string) {
				st, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat: %v", err)
				}
				if err := os.Truncate(path, st.Size()-3); err != nil {
					t.Fatalf("truncate: %v", err)
				}
			},
		},
		{
			name: "segment header cut short",
			damage: func(t *testing.T, path string) {
				if err := os.Truncate(path, 5); err != nil {
					t.Fatalf("truncate: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := sealedLog(t, dir)
			tc.damage(t, paths[1]) // the middle segment: sealed, with one after it

			l, err := wal.Open(wal.Options{Dir: dir})
			if err != nil {
				return // refusing to open is the loud answer too
			}
			defer l.Close()
			var got []string
			if rerr := l.Replay(func(data []byte) error {
				got = append(got, string(data))
				return nil
			}); rerr == nil {
				t.Fatalf("replay of a sealed segment with %s returned %q and no error", tc.name, got)
			}
		})
	}
}

// segmentHeaderBytes is the size of the version-2 segment preamble, spelled out
// here so a test can reach past it without importing the package's internals.
const segmentHeaderBytes = 16

// TestEveryEntryPointRefusesAnUntrustworthyListing: the segment list is what every
// reader starts from, and when it cannot be trusted none of them may quietly read
// a subset of it.
//
// A .wal file whose name is not a sequence number cannot be placed in segment
// order — and order is the log. A reader that skipped it, or guessed, would
// replay a different log than the one on disk and have no way to say so. The
// listing therefore fails, and the failure has to reach every door into the log
// rather than only the one that happens to be tested.
func TestEveryEntryPointRefusesAnUntrustworthyListing(t *testing.T) {
	newLog := func(t *testing.T) *wal.Log {
		t.Helper()
		dir := t.TempDir()
		l, err := wal.Open(wal.Options{Dir: dir})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = l.Close() })
		if err := l.Append([]byte("real")); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		// Sorts after the real segment, and is not a sequence number.
		if err := os.WriteFile(filepath.Join(dir, "zzz-stray.wal"), []byte("junk"), 0o644); err != nil {
			t.Fatalf("write stray: %v", err)
		}
		return l
	}

	pos := func(data []byte) (uint64, error) { return uint64(len(data)), nil }

	for _, tc := range []struct {
		name string
		call func(l *wal.Log) error
	}{
		{"Replay", func(l *wal.Log) error {
			return l.Replay(func([]byte) error { return nil })
		}},
		{"ReplayFrom", func(l *wal.Log) error {
			return l.ReplayFrom(1, pos, func([]byte) error { return nil })
		}},
		{"ReplayForRecovery", func(l *wal.Log) error {
			_, err := l.ReplayForRecovery(0, pos, func([]byte) error { return nil })
			return err
		}},
		{"ReplayForRecovery past a prefix", func(l *wal.Log) error {
			_, err := l.ReplayForRecovery(1, pos, func([]byte) error { return nil })
			return err
		}},
		{"EarliestPosition", func(l *wal.Log) error {
			_, _, err := l.EarliestPosition(pos)
			return err
		}},
		{"Compact", func(l *wal.Log) error {
			_, err := l.Compact(1, pos)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(newLog(t)); err == nil {
				t.Fatalf("%s read a segment listing it could not order, without saying so", tc.name)
			}
		})
	}
}
