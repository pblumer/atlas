package ad_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// A mock forest is only as useful as it is visible. The worker's log says what the
// mock was *asked* to do, one line per operation, which answers "did that job reach a
// directory" and not "what is in the directory now" — and the second question is the
// one somebody trying a joiner/leaver out actually has. These tests cover the
// reporting half: the snapshot a worker sends, and the view a server keeps of it
// (ADR-draft-ad-mock-directory-in-the-console).

// The snapshot holds every forest the mock has been asked to reach, each with its own
// live entries — flattened would be the wrong shape for exactly the run a mockup is
// for, a process provisioning two directories.
func TestMockSnapshotHoldsEveryForestSeparately(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	run(t, d, ad.Job{URL: "ldaps://dc.other.example:636", Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})

	snap := d.Snapshot(0)
	if len(snap.Forests) != 2 {
		t.Fatalf("forests = %d, want 2 (one per URL dialled)", len(snap.Forests))
	}
	// Sorted by URL, so a view built from two snapshots does not reorder itself.
	if snap.Forests[0].URL != "ldaps://dc.example.com:636" || snap.Forests[1].URL != "ldaps://dc.other.example:636" {
		t.Fatalf("forest URLs = %q, %q; want them sorted", snap.Forests[0].URL, snap.Forests[1].URL)
	}
	for _, f := range snap.Forests {
		if len(f.Entries) != 1 || !strings.EqualFold(f.Entries[0].DN, arnoDN) {
			t.Fatalf("forest %s holds %v, want just %s", f.URL, f.Entries, arnoDN)
		}
		if got := f.Entries[0].Attributes["sAMAccountName"]; len(got) != 1 || got[0] != "arno" {
			t.Errorf("entry attributes = %v, want the account name the job wrote", f.Entries[0].Attributes)
		}
	}
}

// A deleted entry is gone from the snapshot. It lives on as a tombstone so a delta can
// report it, but a view of what the directory *holds* that still showed a deleted
// account would be worse than no view: it is the one thing a leaver run is checking.
func TestMockSnapshotLeavesTombstonesOut(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: arnoDN})

	snap := d.Snapshot(0)
	for _, f := range snap.Forests {
		for _, e := range f.Entries {
			if strings.EqualFold(e.DN, arnoDN) {
				t.Fatalf("deleted %s is still in the snapshot", arnoDN)
			}
		}
	}
}

// The snapshot is bounded, because it crosses a network into a server's memory. Past
// the limit it says so rather than silently showing a directory smaller than the one
// the worker holds — a view that quietly truncates is how somebody concludes their
// import did not run.
func TestMockSnapshotIsBoundedAndSaysSo(t *testing.T) {
	d := ad.NewMockDirectory()
	for _, cn := range []string{"a", "b", "c", "d"} {
		run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: "cn=" + cn + "," + usersDN,
			Attributes: map[string][]string{"sAMAccountName": {cn}}})
	}

	snap := d.Snapshot(2)
	if len(snap.Forests) != 1 {
		t.Fatalf("forests = %d, want 1", len(snap.Forests))
	}
	f := snap.Forests[0]
	if len(f.Entries) != 2 || !f.Truncated {
		t.Fatalf("entries = %d truncated = %v; want 2 and truncated", len(f.Entries), f.Truncated)
	}
	if f.Held != 4 {
		t.Errorf("held = %d, want the 4 the forest actually holds", f.Held)
	}
	// The bound spans the whole snapshot, not each forest: two forests of two entries
	// each must not send four when two were asked for.
	run(t, d, ad.Job{URL: "ldaps://dc.other.example:636", Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	if total := entryCount(d.Snapshot(2)); total != 2 {
		t.Errorf("entries across forests = %d, want the limit of 2", total)
	}
}

// An unbounded snapshot is what a test and a small mockup want, and 0 says so.
func TestMockSnapshotZeroLimitHoldsEverything(t *testing.T) {
	d := ad.NewMockDirectory()
	for _, cn := range []string{"a", "b", "c"} {
		run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: "cn=" + cn + "," + usersDN,
			Attributes: map[string][]string{"sAMAccountName": {cn}}})
	}
	snap := d.Snapshot(0)
	if total := entryCount(snap); total != 3 {
		t.Errorf("entries = %d, want all 3", total)
	}
	if snap.Forests[0].Truncated {
		t.Error("an unbounded snapshot reports itself truncated")
	}
}

// The seed count travels even before a forest exists. Forests are created on first
// contact, so a worker that has leased no AD job yet holds no directory at all — and
// the operator watching the view still needs to be told that mock mode is on and what
// it will start from, which is exactly what "no forests, 2 seeded" says.
func TestMockSnapshotCarriesTheSeedCountBeforeAnyDial(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}},
		ad.Entry{DN: usersDN},
	)
	snap := d.Snapshot(0)
	if len(snap.Forests) != 0 {
		t.Errorf("forests = %d before any job; a forest exists only once dialled", len(snap.Forests))
	}
	if snap.Seeded != 2 {
		t.Errorf("seeded = %d, want 2", snap.Seeded)
	}
}

// The operation journal rides along, so the view can say what happened and not only
// what is there. It is the same journal the worker logs, newest last.
func TestMockSnapshotCarriesTheOperationJournal(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})

	snap := d.Snapshot(0)
	var ops []string
	for _, op := range snap.Operations {
		ops = append(ops, op.Op)
	}
	if len(ops) == 0 || ops[len(ops)-1] != "add" {
		t.Fatalf("operations = %v, want the add last", ops)
	}
}

// A password never reaches the view, because it never reached the directory: the mock
// checks the encoding and drops the value (ADR-0181). This is the test that keeps it
// that way now that the entries leave the worker's memory.
func TestMockSnapshotNeverCarriesAPassword(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "set-password", DN: arnoDN, NewPassword: "Sommer-2026!"})

	snap := d.Snapshot(0)
	for _, f := range snap.Forests {
		for _, e := range f.Entries {
			for name, vals := range e.Attributes {
				if strings.EqualFold(name, "unicodePwd") {
					t.Errorf("entry %s carries %s in the snapshot", e.DN, name)
				}
				for _, v := range vals {
					if strings.Contains(v, "Sommer-2026!") {
						t.Errorf("entry %s attribute %s carries the password", e.DN, name)
					}
				}
			}
		}
	}
	for _, op := range snap.Operations {
		if strings.Contains(op.Detail, "Sommer-2026!") {
			t.Errorf("operation %d carries the password: %s", op.Seq, op.Detail)
		}
	}
}

// Version counts what the directory has done, so a reporter can skip a send that would
// carry nothing new. A read-only job leaves it alone; a write moves it.
func TestMockVersionMovesOnlyWhenSomethingHappened(t *testing.T) {
	d := ad.NewMockDirectory()
	if v := d.Version(); v != 0 {
		t.Fatalf("version = %d on a fresh directory, want 0", v)
	}
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	after := d.Version()
	if after == 0 {
		t.Fatal("version did not move after a create")
	}
	if got := d.Version(); got != after {
		t.Errorf("version moved without an operation: %d then %d", after, got)
	}
}

// entryCount totals the entries a snapshot carries.
func entryCount(s ad.MockSnapshot) int {
	n := 0
	for _, f := range s.Forests {
		n += len(f.Entries)
	}
	return n
}
