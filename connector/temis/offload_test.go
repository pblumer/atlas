package temis_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/temis"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The offload seam, tested in the package that owns it. Its two halves answer
// different questions and are worth separating: Resolve is what the engine alone can
// do — build the decision's input context out of the instance's scope — and Run is
// what moved out of it.

// errClient fails every evaluation, for the paths that must not complete a job.
type errClient struct{ err error }

func (c *errClient) Evaluate(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, c.err
}

func registryWith(name string, c temis.Client) *temis.Registry {
	reg := temis.NewRegistry()
	reg.Register(name, c)
	return reg
}

// Resolve builds the input context from the instance's variables: the mapping
// Amount ← amount is compiled FEEL, evaluated over the scope chain, and it is the
// reason this half stays in the engine at all — a worker has no scope to read.
func TestResolveBuildsTheInputContextFromTheInstance(t *testing.T) {
	p, store := openEngine(t)
	cp, _ := loanProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "50000"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The business rule task's element instance is the one whose node carries a
	// business rule detail naming the decision.
	var (
		ei    *model.ElementInstanceValue
		eiKey uint64
	)
	_ = store.ActiveElementInstances(func(key uint64, v *model.ElementInstanceValue) error {
		if d := cp.BusinessRuleTask(cp.Node(v.ElementId).Detail); d != nil && cp.Intern(d.DecisionId) == "Risk" {
			ei, eiKey = v, key
		}
		return nil
	})
	if ei == nil {
		t.Fatal("the instance did not park at the business rule task")
	}

	j, err := temis.Resolve(store, cp, cp.BusinessRuleTask(cp.Node(ei.ElementId).Detail), ei, eiKey)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.DecisionID != "Risk" || j.Connector != "risk-service" {
		t.Errorf("job = %+v, want the authored decision and service", j)
	}
	if j.Result != "risk" {
		t.Errorf("result variable = %q, want the authored one", j.Result)
	}
	if got, ok := j.Inputs["Amount"]; !ok || got == nil {
		t.Errorf("inputs = %#v, want Amount mapped from the instance's amount", j.Inputs)
	}
}

// A task with no compiled detail is a payload arm asking for a kind this node is not.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := temis.Resolve(nil, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}

// Run asks the named service and hands back what it said.
func TestRunEvaluatesThroughTheNamedService(t *testing.T) {
	res, err := temis.Run(context.Background(),
		temis.Job{Connector: "risk-service", DecisionID: "Risk", Inputs: map[string]any{"Amount": 90000.0}, Result: "risk"},
		registryWith("risk-service", &fakeClient{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["Risk"] == nil {
		t.Errorf("outputs = %#v, want the service's answer", res.Outputs)
	}
	if res.ResultVariable != "risk" {
		t.Errorf("result variable = %q, want the one the job carried", res.ResultVariable)
	}
}

// The two refusals before any service is asked, and the one after.
func TestRunRefusesWhatItCannotEvaluate(t *testing.T) {
	t.Run("no registry at all", func(t *testing.T) {
		_, err := temis.Run(context.Background(), temis.Job{Connector: "risk-service"}, nil)
		if err == nil {
			t.Fatal("a job ran against no registry")
		}
		if !strings.Contains(err.Error(), "risk-service") {
			t.Errorf("error = %v, want it to name the service the job asked for", err)
		}
	})
	t.Run("a service this process does not hold", func(t *testing.T) {
		_, err := temis.Run(context.Background(), temis.Job{Connector: "anderswo"},
			registryWith("risk-service", &fakeClient{}))
		if err == nil {
			t.Fatal("a job naming an unknown service was accepted")
		}
		if !strings.Contains(err.Error(), "anderswo") {
			t.Errorf("error = %v, want it to name the unknown service", err)
		}
	})
	t.Run("a service that fails", func(t *testing.T) {
		boom := errors.New("decision service unreachable")
		_, err := temis.Run(context.Background(), temis.Job{Connector: "risk-service"},
			registryWith("risk-service", &errClient{err: boom}))
		if !errors.Is(err, boom) {
			t.Errorf("error = %v, want the service's own failure", err)
		}
	})
}

// A single-output decision reaches the model as a scalar, because that is what a
// gateway condition on it reads; a decision the model keeps nothing of writes
// nothing. Both halves render through this, so neither can drift.
func TestResultVariablesRenderLikeAnInEngineDecision(t *testing.T) {
	single := temis.Result{ResultVariable: "risk", Outputs: map[string]any{"Risk": "high"}}
	vars := single.Variables()
	if len(vars) != 1 || vars[0].Name != "risk" {
		t.Fatalf("variables = %+v, want one named for the result variable", vars)
	}
	if vars[0].Text != "high" {
		t.Errorf("value = %q, want the single output unwrapped to a scalar", vars[0].Text)
	}

	if got := (temis.Result{Outputs: map[string]any{"Risk": "high"}}).Variables(); got != nil {
		t.Errorf("variables = %+v, want none when the model named no result variable", got)
	}
}

// A decision service that answers with an error leaves the token parked with an
// incident, exactly as an unregistered one does — the in-engine path goes through the
// same Run as the worker's, so a service that is reachable but failing and one that
// is not configured at all reach a model the same way: as work not done, never as a
// decision with no answer.
func TestTemisConnectorEvaluationFailureParksTheToken(t *testing.T) {
	p, store := openEngine(t)
	cp, jobType := loanProcess(t)
	p.Deploy(cp)

	runner := job.NewRunner(store, p)
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	reg := registryWith("risk-service", &errClient{err: errors.New("decision service unreachable")})
	runner.HandleCompleting(jobType, func(rd state.Reader) job.CompletingHandler {
		return temis.Handler(store, lookup, reg, nil)
	})

	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "150"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive with a failing service: %v, want nil (failure routed to an incident, ADR-0061)", err)
	}
	ei, err := store.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	if ei != 1 {
		t.Fatalf("active elements = %d, want 1: the token stays on the business rule task", ei)
	}
}

// scopeErrStore reports the element instance but fails the variable read, which is
// the shape of a read view that went away mid-job.
type scopeErrStore struct {
	inner state.Reader
	err   error
}

func (s scopeErrStore) GetElementInstance(k uint64) (*model.ElementInstanceValue, bool, error) {
	return s.inner.GetElementInstance(k)
}

func (s scopeErrStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error {
	return s.err
}

// Resolve fails rather than sending a decision an empty input context. The failure
// mode this prevents is the quiet one: a decision asked with no inputs still gets an
// answer — the table's default rule — and that answer would route the process.
func TestResolveFailsRatherThanSendingAnEmptyContext(t *testing.T) {
	p, store := openEngine(t)
	cp, _ := loanProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "50000"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var (
		ei    *model.ElementInstanceValue
		eiKey uint64
	)
	_ = store.ActiveElementInstances(func(key uint64, v *model.ElementInstanceValue) error {
		if d := cp.BusinessRuleTask(cp.Node(v.ElementId).Detail); d != nil && cp.Intern(d.DecisionId) == "Risk" {
			ei, eiKey = v, key
		}
		return nil
	})
	if ei == nil {
		t.Fatal("the instance did not park at the business rule task")
	}

	boom := errors.New("read view is gone")
	_, err := temis.Resolve(scopeErrStore{inner: store, err: boom},
		cp, cp.BusinessRuleTask(cp.Node(ei.ElementId).Detail), ei, eiKey)
	if err == nil {
		t.Fatal("a decision resolved with no readable inputs; it would have been asked an empty question")
	}
	if !strings.Contains(err.Error(), "build inputs") {
		t.Errorf("error = %v, want it to say the inputs could not be built", err)
	}
}
