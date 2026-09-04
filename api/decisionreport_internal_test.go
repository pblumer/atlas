package api

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The completion contract widened for exactly one kind (ADR-0233, slice 7): a worker
// that evaluated a central decision reports it, and the engine folds the durable
// record (ADR-0066). That is the first time a completion carries anything but
// variables, so what the engine believes and what it stamps itself is the whole
// design — and these pin it.

// decisionReport is the payload shape handleCompleteJob accepts, spelled here so a
// test can build one without going through HTTP.
type decisionReport = struct {
	DecisionID string         `json:"decisionId"`
	Inputs     map[string]any `json:"inputs"`
	Outputs    map[string]any `json:"outputs"`
	Trace      string         `json:"trace"`
}

// A report on a job that is not a business rule task is dropped, and the completion
// still stands. Refusing it would turn a worker sending a field the engine does not
// want into a failed job — a worse answer than ignoring it, because the work was
// done and the variables are good.
func TestADecisionReportOnANonDecisionJobIsIgnored(t *testing.T) {
	srv, _ := newValidateServer(t)
	rep := &decisionReport{DecisionID: "Rate", Outputs: map[string]any{"rate": 1.13}}

	var got *model.DecisionEvaluationValue
	srv.do(func() {
		got = srv.decisionFromReport(&model.JobValue{JobType: compiler.RestJobTypeIndex}, rep)
	})
	if got != nil {
		t.Errorf("decision = %#v, want none: a REST call did not evaluate a decision", got)
	}
}

// A report with no decision id is nothing to fold. It is the shape every other kind's
// worker sends — no decision field at all — and must not produce an empty record.
func TestAnEmptyDecisionReportFoldsNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	jv := &model.JobValue{JobType: compiler.TemisDecisionJobTypeIndex}

	for _, tc := range []struct {
		name string
		rep  *decisionReport
	}{
		{"no report at all", nil},
		{"a report naming no decision", &decisionReport{Outputs: map[string]any{"rate": 1.13}}},
		{"a decision id of only spaces", &decisionReport{DecisionID: "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *model.DecisionEvaluationValue
			srv.do(func() { got = srv.decisionFromReport(jv, tc.rep) })
			if got != nil {
				t.Errorf("decision = %#v, want none", got)
			}
		})
	}
}

// A report about an element instance that is not there folds nothing rather than a
// record with zero keys. Which element a decision belongs to is read from the store,
// never from the report — so when the store cannot say, there is no record to write.
func TestADecisionReportForAnUnknownElementFoldsNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	rep := &decisionReport{DecisionID: "Rate", Outputs: map[string]any{"rate": 1.13}}

	var got *model.DecisionEvaluationValue
	srv.do(func() {
		got = srv.decisionFromReport(&model.JobValue{
			JobType:            compiler.TemisDecisionJobTypeIndex,
			ElementInstanceKey: 999999, // nothing was ever started here
		}, rep)
	})
	if got != nil {
		t.Errorf("decision = %#v, want none: the engine could not say which element this was", got)
	}
}

// Both decision job types are accepted by the type check, and nothing else is. The
// local one is in the list because the *check* is about what kind of element
// evaluates a decision, not about where it runs — keeping the two questions separate
// is what stops this becoming a second, drifting definition of the carve-out.
func TestOnlyBusinessRuleJobTypesMayCarryADecision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		jobType int32
		want    bool
	}{
		{"central decision", compiler.TemisDecisionJobTypeIndex, true},
		{"local decision", compiler.DMNJobTypeIndex, true},
		{"a REST call", compiler.RestJobTypeIndex, false},
		{"a user task", compiler.UserTaskJobTypeIndex, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBusinessRuleJobType(tc.jobType); got != tc.want {
				t.Errorf("isBusinessRuleJobType(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
