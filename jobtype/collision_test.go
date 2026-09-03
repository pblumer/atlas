package jobtype_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
)

// The reserved job-type range grows whenever a built-in worker is added, and
// dynamic indices are issued from one past it. A store written before such an
// addition can therefore hold a model-authored type on an index that is now a
// built-in — and the jobs already on disk under that index do not move.
//
// These tests are the measurement that has to come before any repair: they say
// whether a given store is affected at all, so a migration is built for a real
// case rather than a suspected one.

// seedStored writes a dynamic assignment straight into the table's directory, the
// way an older build would have left it.
func seedStored(t *testing.T, dir string, entries ...jobtype.Entry) {
	t.Helper()
	st, err := sidecar.NewStore(dir, "job types", func(e jobtype.Entry) string { return e.Name })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, e := range entries {
		if err := st.Save(e); err != nil {
			t.Fatalf("Save(%+v): %v", e, err)
		}
	}
}

// A stored name sitting on an index a built-in has since taken is reported, and the
// report names what that index means now — which is what tells an operator what
// their parked jobs would be handed to.
func TestCollisionsFindsAStoredNameTheReservedRangeGrewOver(t *testing.T) {
	dir := t.TempDir()
	// Index 0 is always reserved, so this stands for any name issued before the
	// range reached it.
	seedStored(t, dir, jobtype.Entry{Name: "send-email", Index: 0})

	got, err := jobtype.Collisions(dir)
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("collisions = %+v, want the one stored name", got)
	}
	if got[0].Name != "send-email" || got[0].Index != 0 {
		t.Errorf("collision = %+v, want send-email at 0", got[0])
	}
	if got[0].NowMeans != compiler.ReservedJobTypes()[0] {
		t.Errorf("nowMeans = %q, want the built-in that holds index 0", got[0].NowMeans)
	}
}

// A store whose assignments are all above the reserved range is reported clean. This
// is the answer most stores should give, and saying so plainly is the point of
// measuring first.
func TestCollisionsReportsAHealthyStoreAsClean(t *testing.T) {
	dir := t.TempDir()
	seedStored(t, dir,
		jobtype.Entry{Name: "send-email", Index: compiler.FirstDynamicJobTypeIndex()},
		jobtype.Entry{Name: "charge-card", Index: compiler.FirstDynamicJobTypeIndex() + 1},
	)

	got, err := jobtype.Collisions(dir)
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("collisions = %+v, want none", got)
	}
}

// A directory that was never written is clean rather than an error: a fresh server
// has no table yet, and the check runs at every startup.
func TestCollisionsOnAnUnwrittenStoreIsClean(t *testing.T) {
	got, err := jobtype.Collisions(t.TempDir())
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("collisions = %+v, want none", got)
	}
}

// Several collisions come back in index order, so a report reads the same way twice
// and an operator can compare two runs.
func TestCollisionsAreReportedInIndexOrder(t *testing.T) {
	dir := t.TempDir()
	seedStored(t, dir,
		jobtype.Entry{Name: "later", Index: 2},
		jobtype.Entry{Name: "earlier", Index: 1},
	)

	got, err := jobtype.Collisions(dir)
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(got) != 2 || got[0].Name != "earlier" || got[1].Name != "later" {
		t.Errorf("collisions = %+v, want earlier then later", got)
	}
}

// Opening a registry over an affected store does not hide the problem. Dropping the
// record silently is what made this invisible in the first place; the load now
// carries the finding out so a caller can log or refuse.
func TestOpeningARegistryReportsWhatItHadToDrop(t *testing.T) {
	dir := t.TempDir()
	seedStored(t, dir, jobtype.Entry{Name: "send-email", Index: 0})

	r, err := jobtype.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	dropped := r.Dropped()
	if len(dropped) != 1 || dropped[0].Name != "send-email" {
		t.Fatalf("dropped = %+v, want the collided record", dropped)
	}
	// And the record really is not in the table, which is why it must be reported.
	if _, ok := r.Index("send-email"); ok {
		t.Error("the collided name is in the table after all; the report would be misleading")
	}
	if !strings.Contains(dropped[0].NowMeans, "io.atlas") {
		t.Errorf("nowMeans = %q, want the built-in job type that took the index", dropped[0].NowMeans)
	}
}
