package wal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// recoverLog replays a log the way recovery does and returns the records it saw
// and the continuation it ended with.
func recoverLog(t *testing.T, l *wal.Log) ([]string, string) {
	t.Helper()
	var recs []string
	cont, err := l.ReplayForRecovery(0, nil, func(data []byte) error {
		recs = append(recs, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayForRecovery: %v", err)
	}
	return recs, string(cont)
}

// TestContinuationRidesWithItsBatch: the outstanding work a batch records is
// durable exactly when that batch's events are, and comes back separately from
// them. Both halves matter — the engine folds records into state and must never
// fold a continuation, which is an intention rather than a fact.
func TestContinuationRidesWithItsBatch(t *testing.T) {
	l, err := wal.Open(wal.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	for _, rec := range []string{"e1", "e2"} {
		if err := l.Append([]byte(rec)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.AppendContinuation([]byte("owed-after-batch-1")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	recs, cont := recoverLog(t, l)
	wantEntries(t, [][]byte{[]byte(recs[0]), []byte(recs[1])}, "e1", "e2")
	if len(recs) != 2 {
		t.Fatalf("records = %q, want just the two events", recs)
	}
	if cont != "owed-after-batch-1" {
		t.Fatalf("continuation = %q, want the one the batch carried", cont)
	}

	// A plain Replay is the record path, and must not see it at all.
	if got := replayAll(t, l); len(got) != 2 {
		t.Fatalf("Replay delivered %q; a continuation is not a record", got)
	}
}

// TestNewestContinuationWins: each continuation states the whole outstanding
// queue rather than a change to it, so an older one is a strictly older answer to
// the same question and recovery must take the last.
func TestNewestContinuationWins(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir, MaxSegmentSize: 1}) // roll every batch
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i, owed := range []string{"owed-1", "owed-2", "owed-3"} {
		if err := l.Append([]byte{byte('a' + i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.AppendContinuation([]byte(owed)); err != nil {
			t.Fatalf("AppendContinuation: %v", err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	recs, cont := recoverLog(t, l)
	if len(recs) != 3 {
		t.Fatalf("records = %q, want three", recs)
	}
	if cont != "owed-3" {
		t.Fatalf("continuation = %q, want the newest (owed-3) across segment rolls", cont)
	}
	l.Close()

	// Reopening finds the same answer: it is on disk, not in the writer.
	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	if _, cont := recoverLog(t, l2); cont != "owed-3" {
		t.Fatalf("after reopen continuation = %q, want owed-3", cont)
	}
}

// TestEmptyContinuationSupersedes: a batch that owes nothing still says so, and
// that statement has to win over the older one. Otherwise a restart resumes work
// that was already finished.
func TestEmptyContinuationSupersedes(t *testing.T) {
	l, err := wal.Open(wal.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	if err := l.Append([]byte("e1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.AppendContinuation([]byte("still-owed")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Append([]byte("e2")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// The engine encodes "nothing owed" as a payload, never as an absent entry.
	if err := l.AppendContinuation([]byte("none")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, cont := recoverLog(t, l); cont != "none" {
		t.Fatalf("continuation = %q, want the later batch's (none)", cont)
	}
}

// TestNoContinuationWhenNoneWasWritten: a log of plain records owes nothing, and
// says so by returning none rather than by inventing one.
func TestNoContinuationWhenNoneWasWritten(t *testing.T) {
	l, err := wal.Open(wal.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if err := l.Append([]byte("only")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	recs, cont := recoverLog(t, l)
	if len(recs) != 1 || cont != "" {
		t.Fatalf("records=%q continuation=%q, want one record and no continuation", recs, cont)
	}
}

// TestTornBatchLosesItsContinuationToo: the whole point of putting the
// continuation in the batch's frame is that it cannot survive its own cause. A
// batch cut anywhere takes both with it.
func TestTornBatchLosesItsContinuationToo(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append([]byte("kept")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.AppendContinuation([]byte("kept-owed")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Append([]byte("lost")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.AppendContinuation([]byte("lost-owed")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	l.Close()

	path := filepath.Join(dir, "0000000000000000.wal")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Cut four bytes off the second batch: enough to tear it, nowhere near the first.
	if err := os.Truncate(path, st.Size()-4); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	l2, err := wal.Open(wal.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	recs, cont := recoverLog(t, l2)
	if len(recs) != 1 || recs[0] != "kept" {
		t.Fatalf("records = %q, want just the whole batch", recs)
	}
	if cont != "kept-owed" {
		t.Fatalf("continuation = %q, want the surviving batch's — a torn batch takes its continuation with it", cont)
	}
}

// TestReplayForRecoverySkipsACoveredPrefix: recovery may start past a prefix a
// checkpoint already covers, and must still come back with the newest
// continuation rather than one from the part it skipped.
func TestReplayForRecoverySkipsACoveredPrefix(t *testing.T) {
	dir := t.TempDir()
	l := openRolling(t, dir)
	writePositions(t, l, 1, 2, 3, 4)
	if err := l.AppendContinuation([]byte("owed-at-the-end")); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var seen int
	cont, err := l.ReplayForRecovery(2, payloadPos, func([]byte) error {
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayForRecovery: %v", err)
	}
	if seen == 0 {
		t.Fatal("skipping the prefix skipped everything")
	}
	if string(cont) != "owed-at-the-end" {
		t.Fatalf("continuation = %q, want the newest one", cont)
	}
}
