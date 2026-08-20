package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The migration fold is the one operation that rewrites a whole instance's live records
// at once (ADR-0162), and the engine tests reach it only through the fixtures a running
// process happens to produce — never with an incident and a compensable record in play.
// These drive it directly, against state built by hand, so every family it touches is
// asserted rather than assumed.

const (
	migPI    = uint64(9001)
	migFrom  = uint64(700)
	migTo    = uint64(800)
	migElA   = uint64(9101) // parked on element index 1 → 2
	migElB   = uint64(9102) // a second token, index 3 → 5
	migScope = uint64(9103) // a subprocess scope holding a compensable record
)

// migrationFixture writes one instance with two live element instances, an incident on
// the first, and a compensable record under a scope, all bound to migFrom.
func migrationFixture(t *testing.T, s *state.Store) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(migPI, &model.ProcessInstanceValue{
			ProcessDefKey: migFrom, State: model.PIActive, CorrelationKey: "order-7",
		}); err != nil {
			return err
		}
		if err := tx.IncDefInstanceCount(migFrom); err != nil {
			return err
		}
		if err := tx.IncrementActiveStartKey(migFrom, "order-7"); err != nil {
			return err
		}
		for _, el := range []struct {
			key uint64
			id  int32
		}{{migElA, 1}, {migElB, 3}, {migScope, 7}} {
			if err := tx.PutElementInstance(el.key, &model.ElementInstanceValue{
				ProcessInstanceKey: migPI, ProcessDefKey: migFrom, ElementId: el.id, TokenID: el.key,
			}); err != nil {
				return err
			}
			if err := tx.IncElementToken(migFrom, el.id); err != nil {
				return err
			}
		}
		if err := tx.PutIncident(&model.IncidentValue{
			ProcessInstanceKey: migPI, ElementInstanceKey: migElA, ElementId: 1,
			RaisedAt: 42, Message: "mail: connection refused",
		}); err != nil {
			return err
		}
		return tx.RecordCompensable(11, &model.CompensableValue{
			ProcessInstanceKey: migPI, ProcessDefKey: migFrom, ScopeKey: migScope,
			ElementInstanceKey: migElB, ElementId: 3, HandlerNode: 4,
		})
	})
}

// migrationMapping is the fixture's correspondence: every index shifts, so nothing
// passes by accident.
func migrationMapping() model.ProcessMigrationValue {
	return model.NewProcessMigration(migPI, migFrom, migTo, map[int32]int32{
		1: 2, 3: 5, 7: 9, 4: 6,
	})
}

func TestMigrateInstanceRewritesEveryBoundRecord(t *testing.T) {
	s := openStore(t)
	migrationFixture(t, s)
	v := migrationMapping()
	commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&v) })

	pi, ok, err := s.ProcessInstance(migPI)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance: %v (ok=%v)", err, ok)
	}
	if pi.ProcessDefKey != migTo {
		t.Errorf("instance def key = %d, want %d", pi.ProcessDefKey, migTo)
	}

	// Element instances carry the index execution dispatches on, and their keys — which
	// everything else hangs off — must not have moved.
	for _, want := range []struct {
		key uint64
		id  int32
	}{{migElA, 2}, {migElB, 5}, {migScope, 9}} {
		ei, ok, err := s.GetElementInstance(want.key)
		if err != nil || !ok {
			t.Fatalf("GetElementInstance(%d): %v (ok=%v)", want.key, err, ok)
		}
		if ei.ElementId != want.id || ei.ProcessDefKey != migTo {
			t.Errorf("element %d = def %d element %d, want %d/%d", want.key, ei.ProcessDefKey, ei.ElementId, migTo, want.id)
		}
		if ei.TokenID != want.key {
			t.Errorf("element %d lost its token identity: %+v", want.key, *ei)
		}
	}

	// The per-definition live-token counters moved with the tokens (ADR-0080).
	if got := tokensOf(t, s, migFrom); len(got) != 0 {
		t.Errorf("the source version still counts %v, want nothing", got)
	}
	want := map[int32]int64{2: 1, 5: 1, 9: 1}
	if got := tokensOf(t, s, migTo); !sameCounts(got, want) {
		t.Errorf("target live tokens = %v, want %v", got, want)
	}

	// The incident's element index is what every operator surface resolves to a diagram
	// element — and, since ADR-0160, to the connector behind it.
	inc, err := s.GetIncident(migElA)
	if err != nil || inc == nil {
		t.Fatalf("GetIncident: %v (nil=%v)", err, inc == nil)
	}
	if inc.ElementId != 2 {
		t.Errorf("incident element = %d, want 2", inc.ElementId)
	}
	if inc.Message != "mail: connection refused" || inc.RaisedAt != 42 {
		t.Errorf("incident lost its facts: %+v", *inc)
	}

	// A compensable record names both the activity and its handler node; the handler is
	// dereferenced against the definition when compensation fires, so a stale one would
	// activate an element from the version the instance has left.
	var comps []model.CompensableValue
	if err := s.CompensablesOfScope(migScope, func(cv *model.CompensableValue) error {
		comps = append(comps, *cv)
		return nil
	}); err != nil {
		t.Fatalf("CompensablesOfScope: %v", err)
	}
	if len(comps) != 1 {
		t.Fatalf("compensables = %d, want 1", len(comps))
	}
	if c := comps[0]; c.ProcessDefKey != migTo || c.ElementId != 5 || c.HandlerNode != 6 {
		t.Errorf("compensable = %+v, want def %d element 5 handler 6", c, migTo)
	}
}

func TestMigrateInstanceMovesTheDefinitionCounters(t *testing.T) {
	s := openStore(t)
	migrationFixture(t, s)
	v := migrationMapping()
	commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&v) })

	from, err := s.DefInstanceCount(migFrom)
	if err != nil {
		t.Fatalf("DefInstanceCount(from): %v", err)
	}
	to, err := s.DefInstanceCount(migTo)
	if err != nil {
		t.Fatalf("DefInstanceCount(to): %v", err)
	}
	if from != 0 || to != 1 {
		t.Errorf("active instance counts = from %d / to %d, want 0 / 1", from, to)
	}
	// A message-start instance holds its correlation key open against its definition so
	// a second message cannot start a duplicate (ADR-0094). The hold moves with it, or
	// the old version blocks a key nothing is running under.
	var fromKey, toKey int32
	commit(t, s, func(tx *state.Tx) error {
		var err error
		if fromKey, err = tx.ActiveStartKeyCount(migFrom, "order-7"); err != nil {
			return err
		}
		toKey, err = tx.ActiveStartKeyCount(migTo, "order-7")
		return err
	})
	if fromKey != 0 || toKey != 1 {
		t.Errorf("active start-key counts = from %d / to %d, want 0 / 1", fromKey, toKey)
	}
}

// TestMigrateInstanceIsIdempotent is what makes a re-applied event safe. The fold runs
// on every replay of the log, and a second application that moved the counters again
// would leave a definition claiming instances it does not have.
func TestMigrateInstanceIsIdempotent(t *testing.T) {
	s := openStore(t)
	migrationFixture(t, s)
	v := migrationMapping()
	for i := 0; i < 3; i++ {
		commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&v) })
	}
	to, err := s.DefInstanceCount(migTo)
	if err != nil {
		t.Fatalf("DefInstanceCount: %v", err)
	}
	if to != 1 {
		t.Errorf("active instances on the target after three applications = %d, want 1", to)
	}
	want := map[int32]int64{2: 1, 5: 1, 9: 1}
	if got := tokensOf(t, s, migTo); !sameCounts(got, want) {
		t.Errorf("live tokens after three applications = %v, want %v", got, want)
	}
}

// TestMigrateInstanceLeavesAnAbsentInstanceAlone covers the two arms that make the fold
// safe to replay against state the instance has already left: it finished and was
// purged, or it is not on the version the event describes.
func TestMigrateInstanceLeavesAnAbsentInstanceAlone(t *testing.T) {
	s := openStore(t)
	v := migrationMapping()
	commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&v) })
	if _, ok, err := s.ProcessInstance(migPI); err != nil || ok {
		t.Fatalf("migrating an absent instance created state: ok=%v err=%v", ok, err)
	}

	// Present, but on a third version — so this event does not describe it.
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutProcessInstance(migPI, &model.ProcessInstanceValue{ProcessDefKey: 999, State: model.PIActive})
	})
	commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&v) })
	pi, ok, err := s.ProcessInstance(migPI)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance: %v (ok=%v)", err, ok)
	}
	if pi.ProcessDefKey != 999 {
		t.Errorf("instance was rebound to %d despite being on another version", pi.ProcessDefKey)
	}
}

// TestMigrateInstanceLeavesUncoveredRecordsAlone: a mapping that does not cover a
// record's index must leave it rather than guess a target. Validation at command time
// is what guarantees no live record carries an uncovered index; a fold that invented one
// is the way this corrupts an instance beyond repair.
func TestMigrateInstanceLeavesUncoveredRecordsAlone(t *testing.T) {
	s := openStore(t)
	migrationFixture(t, s)
	// Covers the first token and the scope, but not the second token or its handler.
	partial := model.NewProcessMigration(migPI, migFrom, migTo, map[int32]int32{1: 2, 7: 9})
	commit(t, s, func(tx *state.Tx) error { return tx.MigrateInstance(&partial) })

	ei, _, err := s.GetElementInstance(migElB)
	if err != nil {
		t.Fatalf("GetElementInstance: %v", err)
	}
	if ei.ElementId != 3 || ei.ProcessDefKey != migFrom {
		t.Errorf("uncovered element = def %d element %d, want it untouched at %d/3", ei.ProcessDefKey, ei.ElementId, migFrom)
	}
	var comps []model.CompensableValue
	if err := s.CompensablesOfScope(migScope, func(cv *model.CompensableValue) error {
		comps = append(comps, *cv)
		return nil
	}); err != nil {
		t.Fatalf("CompensablesOfScope: %v", err)
	}
	if len(comps) != 1 || comps[0].HandlerNode != 4 || comps[0].ProcessDefKey != migFrom {
		t.Errorf("compensable = %+v, want it untouched (its handler node is unmapped)", comps)
	}
}

func tokensOf(t *testing.T, s *state.Store, defKey uint64) map[int32]int64 {
	t.Helper()
	out := map[int32]int64{}
	if err := s.ElementLiveTokens(defKey, func(elementId int32, count int64) error {
		if count != 0 {
			out[elementId] = count
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementLiveTokens: %v", err)
	}
	return out
}

func sameCounts(got, want map[int32]int64) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
