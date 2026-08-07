package state

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestHistoryScanDecodeErrors covers the decode-error propagation guard in each
// history/scope scan: a value too short to decode surfaces an error rather than
// being silently skipped. Mirrors TestIncidentDecodeError for the scan families
// that gained no such coverage. These guards protect the operator-facing history
// and scope-inspection reads against corrupt on-disk state.
func TestHistoryScanDecodeErrors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// A single byte is shorter than any of these payloads, so decode must fail.
	corrupt := []byte{0x01}
	seed := func(prefix []byte) {
		t.Helper()
		key := append(append([]byte(nil), prefix...), 0, 0, 0, 0, 0, 0, 0, 0)
		if err := s.db.Set(key, corrupt, pebble.NoSync); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	const scope = uint64(1)

	seed(messageFlowDefPrefix(scope))
	if err := s.MessageFlowHistory(scope, func(int64, uint64, *model.MessageFlowValue) error { return nil }); err == nil {
		t.Error("MessageFlowHistory over corrupt value: err = nil, want decode error")
	}

	seed(decisionEvaluationScopePrefix(scope))
	if err := s.DecisionEvaluationHistory(scope, func(int64, uint64, *model.DecisionEvaluationValue) error { return nil }); err == nil {
		t.Error("DecisionEvaluationHistory over corrupt value: err = nil, want decode error")
	}

	seed(variableAuditScopePrefix(scope))
	if err := s.VariableAuditHistory(scope, func(int64, uint64, *model.VariableAuditValue) error { return nil }); err == nil {
		t.Error("VariableAuditHistory over corrupt value: err = nil, want decode error")
	}

	seed(dataObjectPrefix(scope))
	if err := s.DataObjectsOfScope(scope, func(*model.DataObjectValue) error { return nil }); err == nil {
		t.Error("DataObjectsOfScope over corrupt value: err = nil, want decode error")
	}

	seed(dataObjectSnapshotScopePrefix(scope))
	if err := s.DataObjectSnapshotHistory(scope, func(int64, uint64, *model.DataObjectValue) error { return nil }); err == nil {
		t.Error("DataObjectSnapshotHistory over corrupt value: err = nil, want decode error")
	}

	seed(variableSnapshotScopePrefix(scope))
	if err := s.VariableSnapshotHistory(scope, func(int64, uint64, *model.VariableValue) error { return nil }); err == nil {
		t.Error("VariableSnapshotHistory over corrupt value: err = nil, want decode error")
	}

	// The corrupt decision-evaluation value seeded above also sits under the
	// whole-column-family prefix EachDecisionEvaluation scans.
	if err := s.EachDecisionEvaluation(func(uint64, int64, *model.DecisionEvaluationValue) error { return nil }); err == nil {
		t.Error("EachDecisionEvaluation over corrupt value: err = nil, want decode error")
	}

	// ElementReplayHistory guards on an exact 33-byte width rather than a decode call.
	seed(elementReplayInstancePrefix(scope))
	if err := s.ElementReplayHistory(scope, func(int64, uint64, ElementReplayValue) error { return nil }); err == nil {
		t.Error("ElementReplayHistory over wrong-width value: err = nil, want error")
	}

	// A batch-read scan (Tx) surfaces the same decode error through its own iterator.
	seed(messageSubscriptionPrefix("order", "42"))
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.CorrelatableSubscriptions("order", "42", func(uint64, *model.MessageSubscriptionValue) error { return nil }); err == nil {
		t.Error("CorrelatableSubscriptions over corrupt value: err = nil, want decode error")
	}
}

// TestScopeScanCallbackErrors covers the callback-error propagation in the two
// scope scans that carry live variable/data-object state: a callback error is
// surfaced, not swallowed (the fail path a caller relies on to abort a scan).
func TestScopeScanCallbackErrors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const scope = uint64(1)
	tx := s.NewTransaction()
	if err := tx.PutVariable(&model.VariableValue{ScopeKey: scope, Name: "x", Kind: model.VarNumber, Text: "1"}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	tx.Close()

	sentinel := errors.New("boom")
	tx = s.NewTransaction()
	defer tx.Close()
	if err := tx.PutVariable(&model.VariableValue{ScopeKey: scope, Name: "y", Kind: model.VarNumber, Text: "2"}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := tx.VariablesOfScope(scope, func(*model.VariableValue) error { return sentinel }); err != sentinel {
		t.Errorf("VariablesOfScope callback error = %v, want sentinel", err)
	}
}
