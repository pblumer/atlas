package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// amounts builds one case per amount, which is what the exclusive-gateway
// fixture branches on.
func amounts(v ...int) [][]model.VariableValue {
	out := make([][]model.VariableValue, len(v))
	for i, n := range v {
		out[i] = []model.VariableValue{{Name: "amount", Kind: model.VarNumber, Text: itoa(n)}}
	}
	return out
}

// elementCount finds one element's count, and fails if the heat map does not
// list it at all — a missing element and a cold one are different answers.
func elementCount(t *testing.T, h playground.HeatMap, id string) int64 {
	t.Helper()
	for _, e := range h.Elements {
		if e.Id == id {
			return e.Count
		}
	}
	t.Fatalf("the heat map does not list element %q at all; it has %+v", id, h.Elements)
	return 0
}

func flowCount(t *testing.T, h playground.HeatMap, from, to string) int64 {
	t.Helper()
	for _, f := range h.Flows {
		if f.From == from && f.To == to {
			return f.Count
		}
	}
	t.Fatalf("the heat map does not list the flow %s → %s; it has %+v", from, to, h.Flows)
	return 0
}

// The straight case: every element and every flow carried exactly one token.
// Sequence flows are the half the visit counters cannot answer — the engine
// counts elements, not edges — so this is where the causal history earns its
// keep.
func TestHeatMapCountsElementsAndFlows(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	runOneCase(t, sb)

	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	for _, id := range []string{"start", "set_a", "end"} {
		if got := elementCount(t, h, id); got != 1 {
			t.Errorf("element %q = %d, want 1", id, got)
		}
	}
	for _, f := range [][2]string{{"start", "set_a"}, {"set_a", "end"}} {
		if got := flowCount(t, h, f[0], f[1]); got != 1 {
			t.Errorf("flow %s → %s = %d, want 1", f[0], f[1], got)
		}
	}
}

// The counts add up over a batch, and a branch the data never took is listed at
// zero rather than left out. The zeroes are the point: "this path never ran with
// your data" is a statement the report can only make if it says what it did not
// see.
func TestHeatMapListsThePathsTheDataNeverTook(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: amounts(10, 20, 30)})

	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	if got := elementCount(t, h, "decide"); got != 3 {
		t.Errorf("the gateway ran %d times, want 3", got)
	}
	if got := elementCount(t, h, "autopay"); got != 3 {
		t.Errorf("the small branch ran %d times, want 3", got)
	}
	if got := flowCount(t, h, "decide", "autopay"); got != 3 {
		t.Errorf("the default flow was taken %d times, want 3", got)
	}
	// Nothing was over a thousand, so the review branch is cold — and listed.
	for _, id := range []string{"review", "reviewed"} {
		if got := elementCount(t, h, id); got != 0 {
			t.Errorf("element %q = %d, want 0: no case was large enough to reach it", id, got)
		}
	}
	for _, f := range [][2]string{{"decide", "review"}, {"review", "reviewed"}} {
		if got := flowCount(t, h, f[0], f[1]); got != 0 {
			t.Errorf("flow %s → %s = %d, want 0", f[0], f[1], got)
		}
	}
}

// A condition that does select the branch moves the count there instead, so the
// map reflects the data rather than the diagram's shape.
func TestHeatMapFollowsTheData(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: amounts(5000, 10, 9000)})

	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	if got := flowCount(t, h, "decide", "review"); got != 2 {
		t.Errorf("the large branch was taken %d times, want 2", got)
	}
	if got := flowCount(t, h, "decide", "autopay"); got != 1 {
		t.Errorf("the small branch was taken %d times, want 1", got)
	}
}

// Before a single case has run, the map already knows the diagram's shape: every
// element and flow at zero. Without that a fresh Playground would draw nothing
// and look broken rather than cold.
func TestHeatMapKnowsTheModelBeforeTheRun(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{})

	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	if len(h.Elements) != 6 {
		t.Errorf("elements = %d, want the diagram's six", len(h.Elements))
	}
	if len(h.Flows) != 5 {
		t.Errorf("flows = %d, want the diagram's five", len(h.Flows))
	}
	for _, e := range h.Elements {
		if e.Count != 0 {
			t.Errorf("element %q = %d before the run, want 0", e.Id, e.Count)
		}
	}
	for _, f := range h.Flows {
		if f.Count != 0 {
			t.Errorf("flow %s → %s = %d before the run, want 0", f.From, f.To, f.Count)
		}
	}
}

// Every read path reports a record it cannot decode rather than answering
// without it. A report that quietly drops the rows it could not read still looks
// complete, and that is the failure worth ruling out: the numbers would be wrong
// and nothing on the screen would say so.
func TestEveryReadPathReportsAnUnreadableCase(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: amounts(10, 5000, 20)})

	page, total, err := sb.Cases(0, 10)
	if err != nil || total != 3 {
		t.Fatalf("cases = %d/%v, want three readable rows before anything is broken", total, err)
	}
	broken := page[1].InstanceKey
	if err := sb.InjectUnreadableCase(broken); err != nil {
		t.Fatalf("inject: %v", err)
	}

	for _, tc := range []struct {
		name string
		read func() error
	}{
		{"the report", func() error { _, err := sb.Report(); return err }},
		{"the results page", func() error { _, _, err := sb.Cases(0, 10); return err }},
		{"one case", func() error { _, err := sb.Case(broken); return err }},
		{"the per-case rules", func() error {
			_, err := sb.JudgeRules([]playground.Rule{{Then: `end = "paid"`}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(); err == nil {
				t.Error("read a sandbox with an undecodable record and reported success")
			}
		})
	}

	// The heat map is deliberately *not* in that list. It is folded from the visit
	// counters and the token history, neither of which a case record damages, so it
	// still answers — and answers correctly, because the tokens this case moved are
	// recorded where the map reads them. A map that refused here would be refusing
	// over data it does not use.
	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	if got := elementCount(t, h, "decide"); got != 3 {
		t.Errorf("the gateway ran %d times, want all three — including the case whose record broke", got)
	}
}

// The heat map is the *root's*. A call activity's child runs on its own compiled
// graph, whose element and flow indices mean nothing against this one — folding
// its token history in would attribute the child's work to whatever the parent
// happens to have at the same index, which is worse than leaving it out.
//
// The diagram on screen is the parent's, and that is what the map is drawn onto.
func TestHeatMapCoversTheRootNotTheProcessesItCalls(t *testing.T) {
	// Two executable processes in one file, so the root is named rather than
	// guessed — which is the same thing the Modeler does for a collaboration.
	sb, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "call-activity.bpmn"), BaseDir: t.TempDir(),
		StartTime: simStart, Seed: 1, Root: "parent",
		Stubs: playground.StubSet{Human: &playground.Stub{Min: time.Minute, Max: time.Minute}},
	})
	if err != nil {
		t.Fatalf("open sandbox: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	runPlan(t, sb, playground.Plan{Cases: rows(2)})

	h, err := sb.HeatMap()
	if err != nil {
		t.Fatalf("heat map: %v", err)
	}
	if got := elementCount(t, h, "p_call"); got != 2 {
		t.Errorf("the call activity ran %d times, want 2", got)
	}
	if got := flowCount(t, h, "p_call", "p_end"); got != 2 {
		t.Errorf("the flow out of the call was taken %d times, want 2", got)
	}
	// Nothing of the child leaks in: not its elements, and not its flows.
	for _, e := range h.Elements {
		if e.Id == "c_work" || e.Id == "c_start" || e.Id == "c_end" {
			t.Errorf("the child's element %q is on the parent's heat map", e.Id)
		}
	}
	if len(h.Elements) != 3 || len(h.Flows) != 2 {
		t.Errorf("map = %d elements / %d flows, want the parent's three and two", len(h.Elements), len(h.Flows))
	}
}
