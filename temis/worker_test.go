package temis_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/temis"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// fakeClient is a stand-in temis service: it derives a Risk from the mapped Amount
// input, so a test can prove the central path carried the input and produced a
// result that routes downstream. It counts calls to confirm evaluation happened.
type fakeClient struct{ calls int }

func (c *fakeClient) Evaluate(_ context.Context, _ string, inputs map[string]any) (map[string]any, error) {
	c.calls++
	amount, _ := inputs["Amount"].(float64)
	risk := "low"
	if amount >= 100 {
		risk = "high"
	}
	return map[string]any{"Risk": risk}, nil
}

// loanProcess builds Start → BusinessRuleTask(temis connector "risk-service",
// decision "Risk", result "risk", input Amount ← amount) → ExclusiveGateway with a
// High branch to End and a default branch that parks on an unworked service task.
// The gateway routing is the observable proof the central decision's result reached
// a process variable.
func loanProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(7, "loan", 1)
	start := b.AddStartEvent()
	amountSrc, err := expr.Compile("amount", "amount")
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	rule, err := b.AddTemisDecisionTask("risk-service", "Risk", "risk", nil,
		[]compiler.DecisionInputMapping{{Target: "Amount", Source: amountSrc}}, 3)
	if err != nil {
		t.Fatalf("AddTemisDecisionTask: %v", err)
	}
	gw := b.AddExclusiveGateway()
	end := b.AddEndEvent()
	park := b.AddServiceTask("io.parks", 3) // no worker → a token parks here

	b.Connect(start, rule)
	b.Connect(rule, gw)
	toEnd := b.Connect(gw, end)
	cond, err := expr.Compile(`risk = "high"`, "risk")
	if err != nil {
		t.Fatalf("compile condition: %v", err)
	}
	b.SetFlowCondition(toEnd, cond)
	toPark := b.Connect(gw, park)
	b.SetFlowDefault(toPark)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.BusinessRuleTask(cp.Node(rule).Detail).JobType
}

func openEngine(t *testing.T) (*engine.Processor, *state.Store) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })
	p := engine.New(1, log, store, &fixedClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p, store
}

// TestTemisConnectorRoutesOnResult is the end-to-end proof of the central path: a
// connector business rule task sends its mapped input to a (fake) temis service and
// the returned Risk — written back as the process variable "risk" — drives a
// downstream exclusive gateway. A high amount routes to End (instance completes); a
// low amount falls to the default and parks.
func TestTemisConnectorRoutesOnResult(t *testing.T) {
	cases := []struct {
		name       string
		amount     string
		wantActive int
	}{
		{"high routes to End", "150", 0},
		{"low parks on default", "50", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, store := openEngine(t)
			cp, jobType := loanProcess(t)
			p.Deploy(cp)

			reg := temis.NewRegistry()
			client := &fakeClient{}
			reg.Register("risk-service", client)

			runner := job.NewRunner(store, p)
			lookup := func(uint64) *compiler.CompiledProcess { return cp }
			runner.HandleWithOutput(jobType, temis.Handler(store, lookup, reg, nil))

			p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: tc.amount})
			if err := runner.Drive(); err != nil {
				t.Fatalf("Drive: %v", err)
			}
			if client.calls != 1 {
				t.Fatalf("temis client calls = %d, want 1", client.calls)
			}
			ei, err := store.ActiveElementInstanceCount()
			if err != nil {
				t.Fatalf("ActiveElementInstanceCount: %v", err)
			}
			if ei != tc.wantActive {
				t.Fatalf("active elements = %d, want %d", ei, tc.wantActive)
			}
		})
	}
}

// TestTemisConnectorUnregistered leaves the job pending (Drive errors) when the
// task's connector is not registered — a misconfiguration surfaces as a worker
// error, not a silent no-op.
func TestTemisConnectorUnregistered(t *testing.T) {
	p, store := openEngine(t)
	cp, jobType := loanProcess(t)
	p.Deploy(cp)

	runner := job.NewRunner(store, p)
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	runner.HandleWithOutput(jobType, temis.Handler(store, lookup, temis.NewRegistry(), nil)) // empty registry

	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "150"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive with an unregistered connector: %v, want nil (failure routed to an incident, ADR-0061)", err)
	}
	// The token is still parked on the business rule task, now with an incident.
	ei, err := store.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	if ei != 1 {
		t.Fatalf("active elements = %d, want 1 (parked on the pending job)", ei)
	}
}
