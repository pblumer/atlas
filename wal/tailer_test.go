package wal_test

import (
	"fmt"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// appendSync stages each record and makes the batch durable with one Sync.
func appendSync(t *testing.T, l *wal.Log, records ...string) {
	t.Helper()
	for _, r := range records {
		if err := l.Append([]byte(r)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// drain reads every durable record from `from` and returns them plus the resume
// cursor. fn never stops, so the cursor lands at the durable end.
func drain(t *testing.T, tr *wal.Tailer, from wal.Cursor) ([]string, wal.Cursor) {
	t.Helper()
	var got []string
	cur, err := tr.Read(from, func(data []byte) (bool, error) {
		got = append(got, string(data))
		return false, nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got, cur
}

func wantStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d records %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTailerReadsAllFromGenesis: a Tailer over a populated log, read from the
// zero cursor, yields every durable record in append order.
func TestTailerReadsAllFromGenesis(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendSync(t, l, "alpha", "beta", "gamma")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tr := wal.NewTailer(dir)
	got, _ := drain(t, tr, wal.Cursor{})
	wantStrings(t, got, []string{"alpha", "beta", "gamma"})
}

// TestTailerIncrementalRead: reading to the durable end returns a cursor; records
// appended afterward are read from that cursor without re-reading the earlier ones.
func TestTailerIncrementalRead(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	appendSync(t, l, "one", "two")
	tr := wal.NewTailer(dir)
	got, cur := drain(t, tr, wal.Cursor{})
	wantStrings(t, got, []string{"one", "two"})

	// Nothing new yet: reading from the cursor yields nothing.
	got2, cur2 := drain(t, tr, cur)
	wantStrings(t, got2, nil)

	// Append more; the cursor picks up only the new records.
	appendSync(t, l, "three", "four")
	got3, _ := drain(t, tr, cur2)
	wantStrings(t, got3, []string{"three", "four"})
}

// TestTailerStopDoesNotConsume: when fn stops on a record, that record is not
// consumed — the returned cursor points at it, and a later Read re-delivers it.
func TestTailerStopDoesNotConsume(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	appendSync(t, l, "a", "b", "c", "d")

	tr := wal.NewTailer(dir)
	var seen []string
	cur, err := tr.Read(wal.Cursor{}, func(data []byte) (bool, error) {
		if string(data) == "c" {
			return true, nil // stop before consuming "c"
		}
		seen = append(seen, string(data))
		return false, nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantStrings(t, seen, []string{"a", "b"})

	// Resuming from the returned cursor re-delivers "c" (and "d").
	rest, _ := drain(t, tr, cur)
	wantStrings(t, rest, []string{"c", "d"})
}

// TestTailerAcrossSegmentRoll: with a tiny segment cap the log spans several
// segments; the Tailer reads across the rolls in order, both in one pass and
// incrementally.
func TestTailerAcrossSegmentRoll(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir, MaxSegmentSize: 16})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	// Each record + framing (8 bytes) exceeds the 16-byte soft cap, so every
	// Sync rolls a new segment.
	var want []string
	for i := 0; i < 6; i++ {
		rec := fmt.Sprintf("record-%02d", i)
		appendSync(t, l, rec)
		want = append(want, rec)
	}
	if got := segmentPaths(t, dir); len(got) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(got))
	}

	tr := wal.NewTailer(dir)
	got, cur := drain(t, tr, wal.Cursor{})
	wantStrings(t, got, want)

	// A record appended after the full read is picked up incrementally across the
	// segment boundary.
	appendSync(t, l, "record-06")
	got2, _ := drain(t, tr, cur)
	wantStrings(t, got2, []string{"record-06"})
}

// TestTailerIgnoresUnsynced: records staged with Append but not yet Synced are
// not on disk, so the Tailer does not see them until Sync makes them durable.
func TestTailerIgnoresUnsynced(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	appendSync(t, l, "durable")
	if err := l.Append([]byte("staged")); err != nil { // not Synced
		t.Fatalf("Append: %v", err)
	}

	tr := wal.NewTailer(dir)
	got, cur := drain(t, tr, wal.Cursor{})
	wantStrings(t, got, []string{"durable"})

	// Once Synced, the previously-staged record becomes visible from the cursor.
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got2, _ := drain(t, tr, cur)
	wantStrings(t, got2, []string{"staged"})
}
