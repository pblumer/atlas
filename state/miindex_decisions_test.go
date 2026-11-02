package state

import (
	"errors"
	"strconv"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

func (f *miFixture) decide(el uint64, ts int64, pos uint64, id string) {
	f.t.Helper()
	v := model.DecisionEvaluationValue{ProcessInstanceKey: f.pi, ElementInstanceKey: el, DecisionId: id, InputsJSON: `{"x":1}`, OutputsJSON: `{"y":2}`}
	f.must(f.tx.RecordDecisionEvaluation(ts, pos, &v))
	f.must(f.tx.RecordMIDecision(&v, ts, pos))
}

func (f *miFixture) decisions(owner uint64, limit int) ([]string, bool) {
	f.t.Helper()
	var ids []string
	more, err := f.s.MIDecisions(f.pi, owner, limit, func(_ int64, _ uint64, v *model.DecisionEvaluationValue) error {
		ids = append(ids, v.DecisionId)
		return nil
	})
	f.must(err)
	return ids, more
}

func TestMIDecisionsAreAttributedToTheirIteration(t *testing.T) {
	f := newMIFixture(t, 21)
	f.body(10, 21, 1)
	f.round(11, 10, 2, 1)
	f.round(12, 10, 3, 2)
	f.element(13, 12, 0) // a task inside iteration 12 (a subprocess iteration)
	f.decide(11, 100, 4, "risk")
	f.decide(13, 101, 5, "price")
	f.decide(11, 102, 6, "risk-again")
	f.decide(999, 103, 7, "elsewhere") // not inside any MI execution
	f.commit()

	if ids, more := f.decisions(11, 10); len(ids) != 2 || ids[0] != "risk" || ids[1] != "risk-again" || more {
		t.Fatalf("iteration 11: %v more=%v", ids, more)
	}
	if ids, _ := f.decisions(12, 10); len(ids) != 1 || ids[0] != "price" {
		t.Fatalf("iteration 12: %v", ids)
	}
	if ids, more := f.decisions(11, 1); len(ids) != 1 || !more {
		t.Fatalf("bounded page: %v more=%v", ids, more)
	}
	if _, err := f.s.MIDecisions(21, 11, 0, func(int64, uint64, *model.DecisionEvaluationValue) error { return nil }); err == nil {
		t.Fatal("an invalid limit was accepted")
	}
	// A reference to an evaluation that is not there is corruption, not "none".
	f.must(f.s.db.Delete(keyDecisionEvaluation(21, 100, 4), nil))
	if _, err := f.s.MIDecisions(21, 11, 10, func(int64, uint64, *model.DecisionEvaluationValue) error { return nil }); err == nil {
		t.Fatal("a dangling decision reference was accepted")
	}
	f.must(f.s.db.Set(appendBE64(appendBE64([]byte{byte(cfMIDecision)}, 21), 12), nil, nil))
	if _, err := f.s.MIDecisions(21, 12, 10, func(int64, uint64, *model.DecisionEvaluationValue) error { return nil }); err == nil {
		t.Fatal("a short decision key was accepted")
	}

	f.must(f.tx.PurgeInstanceHistory(21, 9, 0))
	f.commit()
	if err := f.s.scanPrefix(appendBE64([]byte{byte(cfMIDecision)}, 21), func(_, _ []byte) error {
		return errors.New("decision attribution survived purge")
	}); err != nil {
		t.Fatal(err)
	}
}

// A store whose MI history predates decision attribution keeps its iteration
// listing; only the decision list says it cannot be shown.
func TestMIDecisionReadinessIsSeparateFromTheIterationIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.NewTransaction()
	body := model.ElementInstanceValue{ProcessInstanceKey: 30, MultiInstance: 1}
	if err := tx.RecordMIActivation(10, &body, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetLastAppliedPosition(1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()
	if err := s.db.Delete(keyMeta(metaMIDecisionIndex), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ready, err := s.MIHistoryIndexReady(); err != nil || !ready {
		t.Fatalf("iteration index: %v %v", ready, err)
	}
	if ready, err := s.MIDecisionIndexReady(); err != nil || ready {
		t.Fatalf("decision index read as complete: %v %v", ready, err)
	}
	if _, ok, err := s.MIBody(30, 10); err != nil || !ok {
		t.Fatalf("iteration listing lost: %v %v", ok, err)
	}
	if _, err := s.MIDecisions(30, 10, 10, func(int64, uint64, *model.DecisionEvaluationValue) error { return nil }); !errors.Is(err, ErrMIDecisionIndexUnavailable) {
		t.Fatalf("decisions of a legacy store: %v", err)
	}
	if err := s.db.Set(keyMeta(metaMIDecisionIndex), []byte{9}, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MIDecisionIndexReady(); err == nil {
		t.Fatal("a corrupt decision marker was accepted")
	}
}

func TestVariableSnapshotPageIsBoundedToOneScope(t *testing.T) {
	f := newMIFixture(t, 22)
	for i := 1; i <= 5; i++ {
		v := model.VariableValue{ScopeKey: 11, Name: "v" + strconv.Itoa(i), Kind: model.VarNumber, Text: strconv.Itoa(i), Index: -1}
		f.must(f.tx.RecordVariableSnapshot(int64(i), uint64(i), &v))
	}
	other := model.VariableValue{ScopeKey: 12, Name: "other", Kind: model.VarString, Text: "x", Index: -1}
	f.must(f.tx.RecordVariableSnapshot(9, 9, &other))
	f.commit()
	var names []string
	more, err := f.s.VariableSnapshotPage(11, 3, func(_ int64, _ uint64, v *model.VariableValue) error {
		names = append(names, v.Name)
		return nil
	})
	if err != nil || !more || len(names) != 3 || names[0] != "v1" {
		t.Fatalf("page: %v more=%v err=%v", names, more, err)
	}
	if _, err := f.s.VariableSnapshotPage(11, 300, nil); err == nil {
		t.Fatal("an invalid limit was accepted")
	}
}
