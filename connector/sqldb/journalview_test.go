package sqldb

import (
	"context"
	"strings"
	"testing"
)

// A snapshot is what a mockup run leaves behind, for a screen somewhere else. Unlike
// the AD mock there is no state to show — this mock answers statements and executes
// nothing — so the journal is not a companion to the view, it *is* the view: the only
// way to see what a run did.
func TestSnapshotCarriesWhatTheProcessAsked(t *testing.T) {
	c, m := mockClient(t,
		MockAnswer{Statement: "SELECT mail FROM personen WHERE id = @p1", Columns: []string{"mail"}, Rows: [][]any{{"arno@example.com"}}},
		MockAnswer{Statement: "UPDATE personen SET aktiv = @aktiv WHERE id = @id", Affected: 1},
	)
	if _, err := c.Query(context.Background(), "SELECT mail FROM personen WHERE id = @p1", []any{int64(42)}, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}

	snap := m.Snapshot(0)
	if snap.Held != 1 || len(snap.Statements) != 1 {
		t.Fatalf("snapshot = %+v, want one statement", snap)
	}
	got := snap.Statements[0]
	if got.Statement != "SELECT mail FROM personen WHERE id = @p1" {
		t.Errorf("Statement = %q", got.Statement)
	}
	// The bound values are the point: "why did that query find nobody" is almost always
	// a question about what was bound, and the statement alone cannot answer it.
	if len(got.Params) != 1 || got.Params[0] != int64(42) {
		t.Errorf("Params = %#v, want [42]", got.Params)
	}
	if got.Failed {
		t.Errorf("a seeded statement is reported as failed: %+v", got)
	}
}

// A statement nobody seeded is the entry that matters most: it is how an operator
// learns what to put in the seed, and the error text carries the statement and the
// values ready to paste.
func TestSnapshotMarksWhatTheMockRefused(t *testing.T) {
	c, m := mockClient(t)
	if _, err := c.Query(context.Background(), "SELECT 1 FROM personen", nil, 0); err == nil {
		t.Fatal("an unseeded statement succeeded")
	}
	snap := m.Snapshot(0)
	if len(snap.Statements) != 1 {
		t.Fatalf("snapshot = %+v, want the refusal recorded", snap)
	}
	if !snap.Statements[0].Failed {
		t.Error("the refusal is not marked as failed, so the view would read as a run that worked")
	}
	if !strings.Contains(snap.Statements[0].Detail, "nothing is seeded") {
		t.Errorf("Detail = %q, want the reason", snap.Statements[0].Detail)
	}
}

// The snapshot crosses a network into another process's memory, so it is bounded — and
// says what it left out rather than quietly showing a short list. Held is what the
// journal actually has, which is the point of the flag rather than a decoration on it.
func TestSnapshotIsBoundedAndSaysSo(t *testing.T) {
	c, m := mockClient(t, MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}})
	for i := 0; i < 10; i++ {
		if _, err := c.Query(context.Background(), "SELECT 1", nil, 0); err != nil {
			t.Fatalf("Query %d: %v", i, err)
		}
	}

	snap := m.Snapshot(4)
	if len(snap.Statements) != 4 {
		t.Fatalf("got %d statements, want the 4 asked for", len(snap.Statements))
	}
	if !snap.Truncated {
		t.Error("a bounded snapshot did not say it was truncated")
	}
	if snap.Held != 10 {
		t.Errorf("Held = %d, want the 10 the journal actually has", snap.Held)
	}
	// The newest are kept: "what did it just do" is the question being asked.
	if snap.Statements[len(snap.Statements)-1].Seq != 10 {
		t.Errorf("the last statement is #%d, want the newest", snap.Statements[len(snap.Statements)-1].Seq)
	}

	// Unbounded is what a test and a small mockup want.
	if all := m.Snapshot(0); len(all.Statements) != 10 || all.Truncated {
		t.Errorf("unbounded snapshot = %d statements, truncated=%v", len(all.Statements), all.Truncated)
	}
}

// Version is what keeps a worker that answered nothing new from posting the same
// journal over and over.
func TestVersionTracksTheJournal(t *testing.T) {
	c, m := mockClient(t, MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}})
	if v := m.Version(); v != 0 {
		t.Errorf("a fresh mock reports version %d, want 0", v)
	}
	if _, err := c.Query(context.Background(), "SELECT 1", nil, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
	first := m.Version()
	if first == 0 {
		t.Fatal("running a statement did not move the version")
	}
	if m.Version() != first {
		t.Error("the version moved without a statement running")
	}
}

// The view is the server's half: the newest report per worker, replacing rather than
// merging, because a worker sends its whole journal every time.
func TestTheViewKeepsTheNewestReportPerWorker(t *testing.T) {
	v := NewMockJournalViewClock(8, stubClock())
	v.Put(MockJournalSnapshot{Worker: "sql-1", Statements: []MockStatement{{Seq: 1, Statement: "SELECT 1"}}, Held: 1})
	v.Put(MockJournalSnapshot{Worker: "sql-2", Statements: []MockStatement{{Seq: 1, Statement: "SELECT 2"}}, Held: 1})
	v.Put(MockJournalSnapshot{Worker: "sql-1", Statements: []MockStatement{
		{Seq: 1, Statement: "SELECT 1"}, {Seq: 2, Statement: "SELECT 3"},
	}, Held: 2})

	got := v.Snapshots()
	if len(got) != 2 {
		t.Fatalf("got %d workers, want 2", len(got))
	}
	// Sorted by worker, so a Console polling this does not reshuffle under the reader.
	if got[0].Worker != "sql-1" || got[1].Worker != "sql-2" {
		t.Errorf("workers = %q, %q, want them sorted", got[0].Worker, got[1].Worker)
	}
	if len(got[0].Statements) != 2 {
		t.Errorf("sql-1 has %d statements, want the newer report to have replaced the older", len(got[0].Statements))
	}
	if got[0].At == 0 {
		t.Error("the view did not stamp the report's arrival")
	}
}

// A worker that did not name itself still gets filed: an external worker is configured
// by hand and may have no id to send, and dropping its report would answer "why is my
// journal not showing" with silence.
func TestAnUnnamedWorkerIsStillFiled(t *testing.T) {
	v := NewMockJournalViewClock(8, stubClock())
	v.Put(MockJournalSnapshot{Statements: []MockStatement{{Seq: 1, Statement: "SELECT 1"}}, Held: 1})
	got := v.Snapshots()
	if len(got) != 1 || got[0].Worker == "" {
		t.Fatalf("snapshots = %+v, want the unnamed worker filed under a name", got)
	}
}

// The view is bounded too: an operator running many external mock workers must not be
// able to grow a server's memory one dead journal at a time. The one heard from
// longest ago goes.
func TestTheViewDropsTheStalestWorker(t *testing.T) {
	v := NewMockJournalViewClock(2, stubClock())
	for _, name := range []string{"sql-1", "sql-2", "sql-3"} {
		v.Put(MockJournalSnapshot{Worker: name, Held: 1})
	}
	got := v.Snapshots()
	if len(got) != 2 {
		t.Fatalf("got %d workers, want the cap of 2", len(got))
	}
	for _, s := range got {
		if s.Worker == "sql-1" {
			t.Error("the stalest worker survived; the cap drops the wrong end")
		}
	}
}

// stubClock is a monotonic clock, so freshness is decidable without wall time.
func stubClock() func() int64 {
	var n int64
	return func() int64 { n++; return n }
}

// The default constructor is what the server actually calls, so it gets a test rather
// than being reached only through the clock-injecting one the other tests use. A
// capacity below one falls back to the default instead of producing a view that drops
// every report it is given.
func TestTheDefaultJournalViewHoldsReportsAndStampsThem(t *testing.T) {
	v := NewMockJournalView(0) // 0 is not "keep nothing"
	v.Put(MockJournalSnapshot{Worker: "sql-1", Held: 1})

	got := v.Snapshots()
	if len(got) != 1 {
		t.Fatalf("got %d reports from a default view, want 1 — a zero capacity must not mean zero workers", len(got))
	}
	if got[0].At == 0 {
		t.Error("the default clock did not stamp the report, so freshness cannot decide who is dropped")
	}
}
