package wal

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzOpenAndReplayASegment hands the log a segment file with arbitrary contents and
// asks the two things a start asks of it: open, and replay. Either may refuse — a
// file nobody wrote is not a log — but neither may panic, because a panic here is a
// server that cannot start, on a file an operator cannot be expected to repair by
// hand.
//
// The seeds are a segment this package wrote and the ways it is most often damaged:
// cut short at every interesting place, and with single bytes of the header and of a
// batch's length flipped. Run as an ordinary test it replays the seeds; run with
// -fuzz it searches from them.
func FuzzOpenAndReplayASegment(f *testing.F) {
	dir := f.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		f.Fatalf("open: %v", err)
	}
	for _, rec := range [][]byte{[]byte("first record"), []byte("second"), make([]byte, 300)} {
		if err := l.Append(rec); err != nil {
			f.Fatalf("append: %v", err)
		}
		if err := l.Sync(); err != nil {
			f.Fatalf("sync: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		f.Fatalf("close: %v", err)
	}
	names, err := segmentFilesIn(dir)
	if err != nil || len(names) == 0 {
		f.Fatalf("segments: %v %v", names, err)
	}
	good, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		f.Fatalf("read the segment: %v", err)
	}
	f.Add(good)
	f.Add([]byte{})
	for _, cut := range []int{1, 4, 8, 12, 16, 20, len(good) / 2, len(good) - 1} {
		if cut > 0 && cut < len(good) {
			f.Add(append([]byte(nil), good[:cut]...))
		}
	}
	for _, at := range []int{0, 3, 8, 9, 10, 11, 12, 16, len(good) - 5} {
		if at >= 0 && at < len(good) {
			flipped := append([]byte(nil), good...)
			flipped[at] ^= 0xff
			f.Add(flipped)
		}
	}

	f.Fuzz(func(t *testing.T, segment []byte) {
		// The reader refuses a batch longer than maxBatchBytes before it allocates
		// one; a fuzz input is far smaller than that, so what is exercised is the
		// framing, not the memory of the machine running the test.
		if len(segment) > 1<<20 {
			t.Skip()
		}
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, segmentName(1)), segment, 0o644); err != nil {
			t.Fatalf("write the segment: %v", err)
		}
		l, err := Open(Options{Dir: d})
		if err != nil {
			return // refused: a valid answer
		}
		defer l.Close()
		_ = l.Replay(func(data []byte) error { return nil })
	})
}
