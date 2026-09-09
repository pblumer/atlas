package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// varText reads one variable's stored text, or "" when it was never written.
func varText(t *testing.T, s *state.Store, scopeKey uint64, name string) string {
	t.Helper()
	var out string
	if err := s.VariablesOfScope(scopeKey, func(v *model.VariableValue) error {
		if v.Name == name {
			out = v.Text
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}

// TestAVariableIsRefusedAtItsBudget is the acceptance criterion for a budget: below,
// exactly at, and past. The limit itself is allowed, or it would be a budget of one
// byte less.
//
// The refusal is the point, not the failure. Before this budget an oversized value
// went all the way to the write-ahead log and failed *there*, against the 64 MiB
// per-record cap — which aborts the batch, so the instance stopped with an error
// nobody could resolve. Now it is an incident: nothing is written, the instance is
// intact, and correcting the data and resolving writes it after all.
func TestAVariableIsRefusedAtItsBudget(t *testing.T) {
	const limit = 1 << 12
	for _, tc := range []struct {
		name    string
		size    int
		refused bool
	}{
		{"one under the limit", limit - 1, false},
		{"exactly the limit", limit, false},
		{"one over the limit", limit + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)
			cp, _ := linearProcess(t)

			p := engine.New(1, h.log, h.store, &manualClock{})
			p.SetJobNotifier(func(int32) {})
			p.SetMaxVariable(limit)
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			value := strings.Repeat("x", tc.size)
			p.CreateInstance(cp.Key, model.VariableValue{Name: "record", Kind: model.VarString, Text: value})
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v — an oversized value must not take the batch down", err)
			}

			instance := model.NewKey(1, 1)
			got, incs := varText(t, h.store, instance, "record"), incidents(t, h.store)
			if tc.refused {
				if got != "" {
					t.Errorf("a %d-byte value was written under a %d-byte budget", tc.size, limit)
				}
				if len(incs) != 1 {
					t.Fatalf("incidents = %d, want exactly one naming the refusal", len(incs))
				}
				for _, inc := range incs {
					if inc.Reason != model.IncidentVariableTooLarge {
						t.Errorf("incident reason = %v, want IncidentVariableTooLarge", inc.Reason)
					}
					if !strings.Contains(inc.Message, "record") {
						t.Errorf("message = %q, want it to name the variable — an instance has many", inc.Message)
					}
				}
				return
			}
			if got != value {
				t.Errorf("a %d-byte value under a %d-byte budget was not written", tc.size, limit)
			}
			if len(incs) != 0 {
				t.Errorf("incidents = %d, want none", len(incs))
			}
		})
	}
}

// TestDeletingAVariableIsNeverRefused: a delete carries no value, and refusing to
// shrink an instance would be the budget working backwards — the one write that
// makes the problem smaller must always get through.
func TestDeletingAVariableIsNeverRefused(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "record", Kind: model.VarString, Text: "small"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	instance := model.NewKey(1, 1)

	// A budget below even the stored value: a delete still has to go through.
	p.SetMaxVariable(1)
	p.SetVariables(instance, instance, "operator",
		model.VariableValue{Name: "record", Kind: model.VarString, Text: ""})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := varText(t, h.store, instance, "record"); got != "" {
		t.Errorf("record = %q after clearing it under a one-byte budget, want it gone", got)
	}
}

// TestTheBudgetDefaults pins that an engine nobody configured still has both
// ceilings, and that zero or less means the default rather than "no writes at all".
func TestTheBudgetDefaults(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	for _, set := range []int64{0, -1} {
		p.SetMaxVariable(set)
		p.SetMaxCollection(set)
		cp, _ := linearProcess(t)
		p.Deploy(cp)
		if err := p.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		// A value far above what a misconfigured zero would admit, and far below the
		// default: it must be written.
		p.CreateInstance(cp.Key, model.VariableValue{
			Name: "record", Kind: model.VarString, Text: strings.Repeat("x", 4096)})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		if n := len(incidents(t, h.store)); n != 0 {
			t.Fatalf("setting the budget to %d refused an ordinary value (%d incidents)", set, n)
		}
	}
}

// TestAnOutputCollectionIsRefusedAtItsOwnBudget: the collection budget is separate
// from the variable one, and larger, because the two bound different things — one
// asks what a business record may weigh, the other what a legitimate loop at the
// iteration ceiling may accumulate. This pins that the collection is measured against
// *its* budget and not the smaller one, which is the whole reason there are two.
func TestAnOutputCollectionIsRefusedAtItsOwnBudget(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := miCollectProcess(t, "[1, 2, 3]", false)

	p := engine.New(1, h.log, h.store, &manualClock{})
	// Small enough to refuse "[10,20,30]" (ten bytes), while the per-variable budget
	// stays wide open: only the collection's own ceiling can be the reason.
	p.SetMaxCollection(4)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v — an oversized collection must not take the batch down", err)
	}
	incs := incidents(t, h.store)
	if len(incs) == 0 {
		t.Fatal("an oversized output collection was assembled without an incident")
	}
	for _, inc := range incs {
		if inc.Reason != model.IncidentVariableTooLarge {
			t.Errorf("incident reason = %v, want IncidentVariableTooLarge", inc.Reason)
		}
	}
}

// TestACollectionUnderItsBudgetIsUnchanged guards the common case the two budgets must
// not disturb: an ordinary loop collects its results and completes.
func TestACollectionUnderItsBudgetIsUnchanged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := miCollectProcess(t, "[1, 2, 3]", false)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := varText(t, h.store, model.NewKey(1, 1), "results"); got != "[10,20,30]" {
		t.Errorf("results = %q, want [10,20,30]", got)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Errorf("incidents = %d, want none for an ordinary loop", n)
	}
}

// TestATaskWhoseResultDoesNotFitStaysParked is the other half of a refusal, and the
// half that is easy to leave out. Terminating an element clears the incident it
// carries (engine/apply.go), so a task that completed after its result was refused
// would take the only report with it: the job would look successful, and the missing
// variable would be the sole evidence.
//
// The job is done and cannot be redone, so the element stays activated with the
// incident on it. Resolving is what moves it on.
func TestATaskWhoseResultDoesNotFitStaysParked(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, jobKey, _ := startedJob(t, h)
	p.SetMaxVariable(8)

	p.CompleteJob(jobKey, model.VariableValue{
		Name: "result", Kind: model.VarString, Text: strings.Repeat("x", 64)})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	incs := incidents(t, h.store)
	if len(incs) != 1 {
		t.Fatalf("incidents = %d, want one — the refusal has to survive the completion", len(incs))
	}
	for _, inc := range incs {
		if inc.Reason != model.IncidentVariableTooLarge {
			t.Errorf("incident reason = %v, want IncidentVariableTooLarge", inc.Reason)
		}
	}
	// The element is still there holding its token: the instance did not quietly finish.
	if pi, ei := counts(t, h.store); pi != 1 || ei == 0 {
		t.Errorf("process=%d element=%d, want the instance and its task still present", pi, ei)
	}
}
