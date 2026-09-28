package state

import (
	"reflect"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// departed drains one departure index for one element into the instance keys it named.
func departed(t *testing.T, s *Store, how Departure, defKey uint64, elementId int32) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.InstancesDepartedElementDesc(how, defKey, elementId, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("InstancesDepartedElementDesc: %v", err)
	}
	return got
}

// wipeDepartures drops both departure indexes and the marker that says they were
// seeded, leaving exactly the state a store written before they existed would hold.
func wipeDepartures(t *testing.T, s *Store) {
	t.Helper()
	for _, cf := range []columnFamily{cfInstanceCompletedAtEl, cfInstanceCancelledAtEl} {
		lo := []byte{byte(cf)}
		if err := s.db.DeleteRange(lo, prefixEnd(lo), pebble.Sync); err != nil {
			t.Fatalf("wipe index: %v", err)
		}
	}
	if err := s.db.Delete(keyMeta(metaElementDepartureIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
}

// TestBackfillDepartureIndex proves the one-time migration names the instances behind
// the diagram's gray and amber counts, from the history counters a store already holds.
// Without it an operator upgrading into this version and clicking a task two million
// tokens went through would be told none did.
//
// The gray count is visits less cancelled less the tokens still there, so each of those
// three is exercised: an instance that went through, one cancelled there, one still
// sitting there, and one that went round a loop and is sitting there again.
func TestBackfillDepartureIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	const def, el = uint64(7), int32(3)
	tx := s.NewTransaction()
	for _, pi := range []uint64{100, 101, 102, 103} {
		must(t, tx.PutProcessInstance(pi, &model.ProcessInstanceValue{ProcessDefKey: def, CreatedAt: int64(pi)}))
	}
	// 100 went through: one visit, nothing left behind.
	must(t, tx.RecordElementVisit(def, 100, el))
	// 101 was cancelled there: one visit, one termination.
	must(t, tx.RecordElementVisit(def, 101, el))
	must(t, tx.RecordElementTermination(def, 101, el))
	// 102 is sitting there: one visit, one live token.
	must(t, tx.RecordElementVisit(def, 102, el))
	must(t, tx.PutElementInstance(202, &model.ElementInstanceValue{ProcessInstanceKey: 102, ProcessDefKey: def, ElementId: el}))
	// 103 went round a loop: two visits, one of them sitting there now.
	must(t, tx.RecordElementVisit(def, 103, el))
	must(t, tx.RecordElementVisit(def, 103, el))
	must(t, tx.PutElementInstance(203, &model.ElementInstanceValue{ProcessInstanceKey: 103, ProcessDefKey: def, ElementId: el}))
	commit(t, tx)

	wipeDepartures(t, s)
	if got := departed(t, s, DepartedCompleted, def, el); got != nil {
		t.Fatalf("index survived the wipe: %v", got)
	}
	if err := s.backfillDepartureIndexIfNeeded(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if got, want := departed(t, s, DepartedCompleted, def, el), []uint64{103, 100}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed after backfill = %v, want %v", got, want)
	}
	if got, want := departed(t, s, DepartedCancelled, def, el), []uint64{101}; !reflect.DeepEqual(got, want) {
		t.Errorf("cancelled after backfill = %v, want %v", got, want)
	}

	// The marker makes it a one-time migration: a second run does not pass over the
	// history again, so an entry written since stays and nothing is re-derived.
	wipeIndexOnly := func() {
		lo := []byte{byte(cfInstanceCompletedAtEl)}
		if err := s.db.DeleteRange(lo, prefixEnd(lo), pebble.Sync); err != nil {
			t.Fatalf("wipe: %v", err)
		}
	}
	wipeIndexOnly()
	if err := s.backfillDepartureIndexIfNeeded(); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if got := departed(t, s, DepartedCompleted, def, el); got != nil {
		t.Errorf("a repeat run re-derived %v; the marker should have stopped it", got)
	}
}

// TestBackfillDepartureIndexAcrossChunks covers the history-sized seeding: past one
// batch's worth of entries it commits in chunks, and every entry lands however the
// chunk boundaries fall.
func TestBackfillDepartureIndexAcrossChunks(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	const def, el = uint64(7), int32(3)
	n := departureBackfillChunk + 7
	tx := s.NewTransaction()
	for i := 0; i < n; i++ {
		pi := uint64(1000 + i)
		must(t, tx.PutProcessInstance(pi, &model.ProcessInstanceValue{ProcessDefKey: def}))
		must(t, tx.RecordElementVisit(def, pi, el))
	}
	commit(t, tx)

	wipeDepartures(t, s)
	if err := s.backfillDepartureIndexIfNeeded(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := len(departed(t, s, DepartedCompleted, def, el)); got != n {
		t.Errorf("completed after a chunked backfill = %d instances, want %d", got, n)
	}
	if _, ok, err := getCopy(s.db, keyMeta(metaElementDepartureIndexV1)); err != nil || !ok {
		t.Errorf("marker after backfill: present=%v err=%v", ok, err)
	}
}

// TestPurgeDropsDepartures covers retention: a purged instance leaves both departure
// indexes with it. Left behind, it would be offered as having completed a task on a
// server that no longer holds it — and the entries, keyed by element, are reached by no
// prefix over the instance, so the purge has to name them.
//
// It includes the migrated-token case the purge reads the lifecycle trail for: a token
// that completed after a migration recorded its departure under the element it ended on,
// while its visit was counted under the element it started on.
func TestPurgeDropsDepartures(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	const def = uint64(9)
	pi, other := model.NewKey(1, 1), model.NewKey(1, 2)
	tx := s.NewTransaction()
	for _, k := range []uint64{pi, other} {
		must(t, tx.PutProcessInstanceHistory(k, &model.ProcessInstanceValue{ProcessDefKey: def, State: model.PICompleted}))
		must(t, tx.RecordElementVisit(def, k, 3))
		must(t, tx.RecordDeparture(DepartedCompleted, def, 3, k))
		must(t, tx.RecordElementVisit(def, k, 4))
		must(t, tx.RecordElementTermination(def, k, 4))
		must(t, tx.RecordDeparture(DepartedCancelled, def, 4, k))
	}
	// Element 5 was reached only by a migrated token: no visit under this definition,
	// a trail entry, and a departure.
	must(t, tx.RecordElementReplay(pi, 10, 1, 5, 500, 1, 0, 0, ReplayCompleted))
	must(t, tx.RecordDeparture(DepartedCompleted, def, 5, pi))
	commit(t, tx)

	tx = s.NewTransaction()
	must(t, tx.PurgeInstanceHistory(pi, def, 0))
	commit(t, tx)

	if got, want := departed(t, s, DepartedCompleted, def, 3), []uint64{other}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed at 3 after purge = %v, want only the other instance %v", got, want)
	}
	if got, want := departed(t, s, DepartedCancelled, def, 4), []uint64{other}; !reflect.DeepEqual(got, want) {
		t.Errorf("cancelled at 4 after purge = %v, want only the other instance %v", got, want)
	}
	// Not merely skipped on read because the record is gone: the entries themselves are.
	for _, k := range [][]byte{
		keyDeparture(cfInstanceCompletedAtEl, def, 3, pi),
		keyDeparture(cfInstanceCancelledAtEl, def, 4, pi),
		keyDeparture(cfInstanceCompletedAtEl, def, 5, pi),
	} {
		if _, ok, err := getCopy(s.db, k); err != nil || ok {
			t.Errorf("entry %x after purge: present=%v err=%v", k, ok, err)
		}
	}
}

// TestInstancesDepartedElementSkipsAndReports covers what the walk can meet that is not
// an instance: an entry whose record is gone is skipped, and a record that cannot be
// decoded is reported, because a corrupt store must not read as an empty one.
func TestInstancesDepartedElementSkipsAndReports(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	must(t, tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7}))
	must(t, tx.RecordDeparture(DepartedCompleted, 7, 3, 100))
	must(t, tx.RecordDeparture(DepartedCompleted, 7, 3, 101)) // an instance that is not there
	commit(t, tx)
	if got, want := departed(t, s, DepartedCompleted, 7, 3), []uint64{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("with an orphaned entry the walk yielded %v, want %v", got, want)
	}

	if err := s.InjectCorruptProcessInstance(102); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if err := s.db.Set(keyDeparture(cfInstanceCompletedAtEl, 7, 3, 102), nil, pebble.Sync); err != nil {
		t.Fatalf("corrupt entry: %v", err)
	}
	if err := s.InstancesDepartedElementDesc(DepartedCompleted, 7, 3, 0, func(uint64, *model.ProcessInstanceValue) error { return nil }); err == nil {
		t.Error("a corrupt instance record read as an empty answer")
	}
}
