package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// openStore opens a store under a temp dir and closes it with the test.
func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// putInstance writes one live process instance, and returns its key.
func putInstance(t *testing.T, s *Store, key uint64, parentEl uint64) uint64 {
	t.Helper()
	tx := s.NewTransaction()
	if err := tx.PutProcessInstance(key, &model.ProcessInstanceValue{
		ProcessDefKey:            1,
		CreatedAt:                int64(key),
		ParentElementInstanceKey: parentEl,
	}); err != nil {
		t.Fatalf("PutProcessInstance(%d): %v", key, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return key
}

// TestChildByParentIndexRoundTrip covers the reverse call-activity link at the store
// level: two children of the same call activity are both listed, a third under a
// different one is not, and deleting a link removes exactly that child.
func TestChildByParentIndexRoundTrip(t *testing.T) {
	s := openStore(t)
	const callA, callB = uint64(10), uint64(20)

	tx := s.NewTransaction()
	for _, l := range []struct{ parent, child uint64 }{{callA, 100}, {callA, 101}, {callB, 200}} {
		if err := tx.PutChildByParent(l.parent, l.child); err != nil {
			t.Fatalf("PutChildByParent: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	children := func(parent uint64) []uint64 {
		t.Helper()
		var out []uint64
		if err := s.ChildInstancesOfParent(parent, func(k uint64) error {
			out = append(out, k)
			return nil
		}); err != nil {
			t.Fatalf("ChildInstancesOfParent(%d): %v", parent, err)
		}
		return out
	}
	if got := children(callA); len(got) != 2 || got[0] != 100 || got[1] != 101 {
		t.Errorf("children of %d = %v, want [100 101]", callA, got)
	}
	if got := children(callB); len(got) != 1 || got[0] != 200 {
		t.Errorf("children of %d = %v, want [200]", callB, got)
	}

	tx = s.NewTransaction()
	if err := tx.DeleteChildByParent(callA, 100); err != nil {
		t.Fatalf("DeleteChildByParent: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := children(callA); len(got) != 1 || got[0] != 101 {
		t.Errorf("children of %d after deleting one = %v, want [101]", callA, got)
	}
	// A call activity that started nothing lists nothing rather than failing.
	if got := children(999); len(got) != 0 {
		t.Errorf("children of an unknown element = %v, want none", got)
	}
}

// TestActiveProcessInstancePointRead covers the narrow lookup the cancel path uses:
// it answers from the active family alone, so an instance that already finished
// reads as absent — the distinction that keeps a cancel from claiming to have
// cancelled something that was already over.
func TestActiveProcessInstancePointRead(t *testing.T) {
	s := openStore(t)
	putInstance(t, s, 7, 0)

	pi, ok, err := s.ActiveProcessInstance(7)
	if err != nil || !ok {
		t.Fatalf("ActiveProcessInstance(7) = %v, %v, %v; want a value", pi, ok, err)
	}
	if pi.ProcessDefKey != 1 {
		t.Errorf("ProcessDefKey = %d, want 1", pi.ProcessDefKey)
	}
	if _, ok, err := s.ActiveProcessInstance(8); err != nil || ok {
		t.Errorf("ActiveProcessInstance(8) = %v, %v; want absent", ok, err)
	}

	// A finished instance lives in the history family only: ProcessInstance finds it,
	// the active-only read does not.
	tx := s.NewTransaction()
	if err := tx.PutProcessInstanceHistory(9, &model.ProcessInstanceValue{ProcessDefKey: 1}); err != nil {
		t.Fatalf("PutProcessInstanceHistory: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, ok, _ := s.ProcessInstance(9); !ok {
		t.Error("ProcessInstance(9) did not find the finished instance")
	}
	if _, ok, _ := s.ActiveProcessInstance(9); ok {
		t.Error("ActiveProcessInstance(9) found a finished instance; it must read the active family only")
	}
}

// TestInstancesDescIsNewestFirstAndBounded covers the bounded descending scans the
// cross-instance views page with: newest first (keys are allocated in creation
// order), stopping at the limit, and reporting that more remained — which is what
// keeps a bounded page from costing the whole population.
func TestInstancesDescIsNewestFirstAndBounded(t *testing.T) {
	s := openStore(t)
	for k := uint64(1); k <= 5; k++ {
		putInstance(t, s, k, 0)
	}
	tx := s.NewTransaction()
	for k := uint64(10); k <= 12; k++ {
		if err := tx.PutProcessInstanceHistory(k, &model.ProcessInstanceValue{ProcessDefKey: 1}); err != nil {
			t.Fatalf("PutProcessInstanceHistory: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	collect := func(more bool, err error, got []uint64) ([]uint64, bool) {
		t.Helper()
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		return got, more
	}

	var active []uint64
	more, err := s.ActiveProcessInstancesDesc(3, func(k uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, k)
		return nil
	})
	got, hitCap := collect(more, err, active)
	if len(got) != 3 || got[0] != 5 || got[1] != 4 || got[2] != 3 {
		t.Errorf("bounded active scan = %v, want the newest three [5 4 3]", got)
	}
	if !hitCap {
		t.Error("more = false after the scan filled its limit, want true")
	}

	// A limit past the end reports no remainder.
	active = nil
	more, err = s.ActiveProcessInstancesDesc(99, func(k uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, k)
		return nil
	})
	if got, hitCap = collect(more, err, active); len(got) != 5 || hitCap {
		t.Errorf("unbounded-enough active scan = %v (more=%v), want all five and more=false", got, hitCap)
	}

	var done []uint64
	more, err = s.CompletedProcessInstancesDesc(2, func(k uint64, _ *model.ProcessInstanceValue) error {
		done = append(done, k)
		return nil
	})
	if got, hitCap = collect(more, err, done); len(got) != 2 || got[0] != 12 || got[1] != 11 || !hitCap {
		t.Errorf("bounded finished scan = %v (more=%v), want [12 11] and more=true", got, hitCap)
	}

	// A limit of zero or less is a scan of nothing, not a scan of everything — the
	// caller asked for no rows.
	called := false
	if more, err := s.ActiveProcessInstancesDesc(0, func(uint64, *model.ProcessInstanceValue) error {
		called = true
		return nil
	}); err != nil || more || called {
		t.Errorf("limit 0 scanned anyway (called=%v, more=%v, err=%v)", called, more, err)
	}
}

// TestPointReadsRefuseACorruptRecord covers the decode-failure paths of the two
// reads this change added. A record that cannot be decoded must surface as an
// error, not as "no such instance": the second answer would let a cancel report
// success for an instance it never looked at.
func TestPointReadsRefuseACorruptRecord(t *testing.T) {
	s := openStore(t)
	if err := s.InjectCorruptProcessInstance(42); err != nil {
		t.Fatalf("InjectCorruptProcessInstance: %v", err)
	}

	if _, ok, err := s.ActiveProcessInstance(42); err == nil {
		t.Errorf("ActiveProcessInstance on a corrupt record = ok:%v, err:nil; want an error", ok)
	}

	// The bounded descending scan decodes the same records, so it fails the same way
	// rather than skipping the row and reporting a short page as a complete one.
	if _, err := s.ActiveProcessInstancesDesc(10, func(uint64, *model.ProcessInstanceValue) error {
		return nil
	}); err == nil {
		t.Error("ActiveProcessInstancesDesc over a corrupt record returned no error")
	}
}

// TestChildIndexBackfillRefusesACorruptStore covers the migration's failure path: a
// store whose active family holds an undecodable record must fail to open rather
// than open with a half-built index. A partially seeded index is the worst outcome
// available — it looks complete, and the caller it silently omits is a child that
// outlives its parent.
func TestChildIndexBackfillRefusesACorruptStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.InjectCorruptProcessInstance(5); err != nil {
		t.Fatalf("InjectCorruptProcessInstance: %v", err)
	}
	// Re-arm this migration alone: the earlier backfills keep their markers, so the
	// scan under test is the one that meets the corrupt record.
	if err := s.db.Delete(keyMeta(metaChildIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir)
	if err == nil {
		_ = s2.Close()
		t.Fatal("Open succeeded over a corrupt record; want the backfill to refuse")
	}
}

// TestCorruptDataObjectReadsFail covers the decode-failure paths of the two
// data-object reads the instance Data view depends on. A trail missing its middle
// reads as a value that never passed through a state it did, so an unreadable
// record has to surface as an error rather than be skipped.
func TestCorruptDataObjectReadsFail(t *testing.T) {
	s := openStore(t)
	const scope = uint64(3)

	if err := s.InjectCorruptDataObject(scope, "order"); err != nil {
		t.Fatalf("InjectCorruptDataObject: %v", err)
	}
	if err := s.DataObjectsOfScope(scope, func(*model.DataObjectValue) error { return nil }); err == nil {
		t.Error("DataObjectsOfScope over a corrupt object returned no error")
	}

	if err := s.InjectCorruptDataObjectSnapshot(scope, 1, 1); err != nil {
		t.Fatalf("InjectCorruptDataObjectSnapshot: %v", err)
	}
	if err := s.DataObjectSnapshotHistory(scope, func(int64, uint64, *model.DataObjectValue) error { return nil }); err == nil {
		t.Error("DataObjectSnapshotHistory over a corrupt snapshot returned no error")
	}
}
