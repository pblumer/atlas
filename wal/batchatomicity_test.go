package wal_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// writeBatches appends each batch's records and Syncs once per batch, then
// returns the segment file's path and size.
func writeBatches(t *testing.T, dir string, batches [][]string) (string, int64) {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, batch := range batches {
		for _, rec := range batch {
			if err := l.Append([]byte(rec)); err != nil {
				t.Fatalf("Append(%q): %v", rec, err)
			}
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	path := filepath.Join(dir, "0000000000000000.wal")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	return path, st.Size()
}

// TestTornWriteNeverExposesHalfABatch is F02 stated as a property, over every
// byte at which a crash could have cut the write.
//
// One command produces several events. The processor appends them all and Syncs
// once, so they reach the platter in a single write — but a single write is not
// an atomic one. Framing each record on its own made every prefix of that write
// look like a shorter, valid log, so recovery could apply a command's first
// event and not its second: a process instance materialized without the
// variables its creation carried, which is not a state the engine can otherwise
// reach.
//
// The rule this pins is all-or-nothing per batch. At every truncation point the
// log must read back as a whole number of batches — never a partial one.
func TestTornWriteNeverExposesHalfABatch(t *testing.T) {
	batches := [][]string{
		{"b1r1", "b1r2", "b1r3"},
		{"b2r1", "b2r2"},
	}
	path, size := writeBatches(t, t.TempDir(), batches)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// The prefixes that are legal to observe: nothing, batch 1, both batches.
	legal := map[string]bool{
		"":                         true,
		"b1r1,b1r2,b1r3":           true,
		"b1r1,b1r2,b1r3,b2r1,b2r2": true,
	}

	for cut := int64(0); cut <= size; cut++ {
		t.Run(fmt.Sprintf("cut=%d", cut), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "0000000000000000.wal"), full[:cut], 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			l, err := wal.Open(wal.Options{Dir: dir})
			if err != nil {
				t.Fatalf("Open at cut %d: %v", cut, err)
			}
			defer l.Close()
			got := replayAll(t, l)
			joined := ""
			for i, rec := range got {
				if i > 0 {
					joined += ","
				}
				joined += string(rec)
			}
			if !legal[joined] {
				t.Fatalf("truncating to %d of %d bytes exposed %q — a batch must be all or nothing", cut, size, joined)
			}
		})
	}
}

// TestTornBatchStillAcceptsAppends: after a torn tail is dropped, the log must
// stay writable — Open truncates to the last whole batch and the next Sync
// extends a clean log rather than appending after garbage.
func TestTornBatchStillAcceptsAppends(t *testing.T) {
	dir := t.TempDir()
	path, size := writeBatches(t, dir, [][]string{{"first", "second"}, {"third"}})
	// Cut three bytes into the final batch's payload.
	if err := os.Truncate(path, size-3); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if got := replayAll(t, l); len(got) != 2 {
		t.Fatalf("after a torn tail: %q, want the first whole batch only", got)
	}
	if err := l.Append([]byte("fourth")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	wantEntries(t, replayAll(t, l), "first", "second", "fourth")
}
