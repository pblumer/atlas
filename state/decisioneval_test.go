package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestDecisionEvaluationHistory records decision evaluations for two instances and
// checks that DecisionEvaluationHistory returns one instance's evaluations in
// (timestamp, position) order, isolated from the other's (ADR-0066). Written out of
// order to prove the scan sorts, with two evaluations on the same instance to show
// each is retained as a distinct record.
func TestDecisionEvaluationHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	evals := []struct {
		ts  int64
		pos uint64
		v   model.DecisionEvaluationValue
	}{
		{ts: 300, pos: 30, v: model.DecisionEvaluationValue{ProcessInstanceKey: i1, ElementInstanceKey: model.NewKey(1, 3), DecisionId: "dish", InputsJSON: `{"guests":8}`, OutputsJSON: `{"dish":"Stew"}`, TraceJSON: `{"tables":[]}`}},
		{ts: 100, pos: 10, v: model.DecisionEvaluationValue{ProcessInstanceKey: i1, ElementInstanceKey: model.NewKey(1, 2), DecisionId: "greeting", InputsJSON: `{"name":"Pat"}`, OutputsJSON: `{"msg":"Hi Pat"}`}},
		{ts: 150, pos: 15, v: model.DecisionEvaluationValue{ProcessInstanceKey: i2, ElementInstanceKey: model.NewKey(1, 5), DecisionId: "greeting", InputsJSON: `{"name":"Sam"}`, OutputsJSON: `{"msg":"Hi Sam"}`}},
	}
	commit(t, s, func(tx *state.Tx) error {
		for i := range evals {
			e := evals[i]
			if err := tx.RecordDecisionEvaluation(e.ts, e.pos, &e.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts       int64
		pos      uint64
		decision string
		outputs  string
	}
	fold := func(scope uint64) []row {
		var out []row
		if err := s.DecisionEvaluationHistory(scope, func(ts int64, pos uint64, v *model.DecisionEvaluationValue) error {
			out = append(out, row{ts, pos, v.DecisionId, v.OutputsJSON})
			return nil
		}); err != nil {
			t.Fatalf("DecisionEvaluationHistory(%d): %v", scope, err)
		}
		return out
	}

	if got, want := fold(i1), []row{{100, 10, "greeting", `{"msg":"Hi Pat"}`}, {300, 30, "dish", `{"dish":"Stew"}`}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-1 evaluations = %v, want %v", got, want)
	}
	if got, want := fold(i2), []row{{150, 15, "greeting", `{"msg":"Hi Sam"}`}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-2 evaluations = %v, want %v", got, want)
	}
	if got := fold(model.NewKey(1, 99)); len(got) != 0 {
		t.Errorf("unknown scope evaluations = %v, want none", got)
	}

	// EachDecisionEvaluation folds the whole column family, across instances, and
	// recovers each record's owning scope from its key — the global "which decisions
	// ran, how often" access pattern the Operations overview uses. All three records
	// must appear, each tagged with the scope it was written under.
	type g struct {
		scope    uint64
		decision string
	}
	var all []g
	if err := s.EachDecisionEvaluation(func(scope uint64, _ int64, v *model.DecisionEvaluationValue) error {
		all = append(all, g{scope, v.DecisionId})
		return nil
	}); err != nil {
		t.Fatalf("EachDecisionEvaluation: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("global evaluations = %d, want 3: %v", len(all), all)
	}
	counts := map[uint64]int{}
	for _, e := range all {
		counts[e.scope]++
	}
	if counts[i1] != 2 || counts[i2] != 1 {
		t.Errorf("per-scope counts = %v, want {i1:2, i2:1}", counts)
	}
}
