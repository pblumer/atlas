package state

import (
	"reflect"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// scopeTriggers drains ScopeTriggers through a fresh transaction.
func scopeTriggers(t *testing.T, s *Store, scope uint64) []uint64 {
	t.Helper()
	tx := s.NewTransaction()
	defer func() { _ = tx.Close() }()
	var keys []uint64
	if err := tx.ScopeTriggers(scope, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
		t.Fatalf("ScopeTriggers: %v", err)
	}
	return keys
}

func trigger(pi, scope uint64, handler int32) *model.ElementInstanceValue {
	return &model.ElementInstanceValue{ProcessInstanceKey: pi, ProcessDefKey: 7, ElementId: handler,
		FlowScopeKey: scope, BpmnElementType: EventSubTriggerType}
}

// A scope's armed triggers are the index's prefix of that scope: an element that
// is not a trigger, and a trigger of another scope, are not in it; a re-put
// (migration) keeps the one entry; a deleted trigger leaves it; and a transaction
// sees the triggers it armed itself before it commits.
func TestScopeTriggersAreOneScopesArmedTriggers(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx := s.NewTransaction()
	for _, put := range []struct {
		key uint64
		v   *model.ElementInstanceValue
	}{
		{300, trigger(100, 100, 5)}, // the root scope's trigger
		{301, trigger(100, 200, 6)}, // a subprocess iteration's trigger
		{302, trigger(100, 200, 7)}, // a second one in the same scope
		{303, trigger(100, 201, 6)}, // the next iteration's
		{304, &model.ElementInstanceValue{ProcessInstanceKey: 100, ElementId: 2, FlowScopeKey: 200}}, // a task in the iteration
	} {
		if err := tx.PutElementInstance(put.key, put.v); err != nil {
			t.Fatal(err)
		}
	}
	var inFlight []uint64
	if err := tx.ScopeTriggers(200, func(k uint64) error { inFlight = append(inFlight, k); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inFlight, []uint64{301, 302}) {
		t.Fatalf("uncommitted triggers of scope 200: %v", inFlight)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()
	if got := scopeTriggers(t, s, 200); !reflect.DeepEqual(got, []uint64{301, 302}) {
		t.Fatalf("scope 200: %v", got)
	}
	if got := scopeTriggers(t, s, 100); !reflect.DeepEqual(got, []uint64{300}) {
		t.Fatalf("root scope: %v", got)
	}

	tx = s.NewTransaction()
	migrated := trigger(100, 200, 9) // re-put under another definition's handler
	migrated.ProcessDefKey = 8
	if err := tx.PutElementInstance(301, migrated); err != nil {
		t.Fatal(err)
	}
	if err := tx.DeleteElementInstance(302, trigger(100, 200, 7)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()
	if got := scopeTriggers(t, s, 200); !reflect.DeepEqual(got, []uint64{301}) {
		t.Fatalf("scope 200 after a re-put and a delete: %v", got)
	}

	// A key that is not scope:trigger is corruption, reported rather than misread.
	if err := s.db.Set(append(keyScopeTrigger(200, 0)[:9:9], 1), nil, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	tx = s.NewTransaction()
	defer func() { _ = tx.Close() }()
	if err := tx.ScopeTriggers(200, func(uint64) error { return nil }); err == nil {
		t.Fatal("a corrupt index key was read as a trigger")
	}
}

// The one-time migration seeds the index from the triggers a store armed before
// it existed, so an upgraded store still disarms them when their scope completes.
func TestBackfillScopeTriggerIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx := s.NewTransaction()
	_ = tx.PutElementInstance(301, trigger(100, 200, 6))
	_ = tx.PutElementInstance(303, trigger(100, 201, 6))
	_ = tx.PutElementInstance(304, &model.ElementInstanceValue{ProcessInstanceKey: 100, ElementId: 2, FlowScopeKey: 200})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	lo := []byte{byte(cfScopeTrigger)}
	if err := s.db.DeleteRange(lo, prefixEnd(lo), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Delete(keyMeta(metaScopeTriggerIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if got := scopeTriggers(t, s, 200); len(got) != 0 {
		t.Fatalf("index survived the wipe: %v", got)
	}
	if err := s.backfillScopeTriggerIndexIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if got := scopeTriggers(t, s, 200); !reflect.DeepEqual(got, []uint64{301}) {
		t.Fatalf("scope 200 after backfill: %v", got)
	}
	if got := scopeTriggers(t, s, 201); !reflect.DeepEqual(got, []uint64{303}) {
		t.Fatalf("scope 201 after backfill: %v", got)
	}
	// Once done, it is not done again.
	if err := s.db.Delete(keyScopeTrigger(201, 303), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillScopeTriggerIndexIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if got := scopeTriggers(t, s, 201); len(got) != 0 {
		t.Fatalf("the backfill ran twice: %v", got)
	}

	if err := s.InjectCorruptElementInstance(305); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Delete(keyMeta(metaScopeTriggerIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillScopeTriggerIndexIfNeeded(); err == nil {
		t.Fatal("backfill over a corrupt element instance reported success")
	}
}

// A store whose backfill cannot read a record does not open: an index seeded from
// less than the store holds would leave a trigger armed when its scope completes.
func TestAStoreWithAnUnreadableElementDoesNotOpenWithoutItsTriggerIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InjectCorruptElementInstance(305); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Delete(keyMeta(metaScopeTriggerIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(dir); err == nil {
		_ = s.Close()
		t.Fatal("a store opened with a trigger index seeded from an unreadable element")
	}
}
