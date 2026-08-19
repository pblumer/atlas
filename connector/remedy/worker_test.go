package remedy_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the entries a Remedy connector task creates and returns a
// canned entry id.
type recordingClient struct {
	created []remedy.Entry
	id      string
}

func (r *recordingClient) CreateEntry(_ context.Context, e remedy.Entry) (remedy.Result, error) {
	r.created = append(r.created, e)
	return remedy.Result{EntryID: r.id}, nil
}

type erroringClient struct{}

func (erroringClient) CreateEntry(context.Context, remedy.Entry) (remedy.Result, error) {
	return remedy.Result{}, context.DeadlineExceeded
}

const remedyDefKey = 77

func openStore(t *testing.T) (*wal.Log, *state.Store) {
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
	return log, store
}

func mustActiveProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}

func active(t *testing.T, s *state.Store) (pi, ei int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err = s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

// remedyProcess: Start → Remedy connector task → End.
func remedyProcess(t *testing.T, cfg compiler.RemedyConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(remedyDefKey, "ticketing", 1)
	start := b.AddStartEvent()
	call := b.AddRemedyConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, reg *remedy.Registry, store *state.Store, log *wal.Log, vars ...model.VariableValue) error {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, remedy.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg))
	p.CreateInstance(cp.Key, vars...)
	return runner.Drive()
}

// TestRemedyConnectorCreatesEntry is the vertical slice end to end: a Remedy connector
// task creates a job, the in-process Remedy worker resolves the named instance and
// creates the model-authored entry keyed by the job key, the entry id is written into
// the result variable, and the token finishes.
func TestRemedyConnectorCreatesEntry(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Literal: "HPD:IncidentInterface_Create"},
		Fields: []compiler.RestKV{
			{Name: "Description", Val: compiler.RestExpr{Literal: "Disk full"}},
			{Name: "Impact", Val: compiler.RestExpr{Literal: "2-Significant"}},
		},
		ResultVar: "incidentNumber",
		Retries:   3,
	})
	if jobType != compiler.RemedyJobTypeIndex {
		t.Fatalf("remedy job type index = %d, want the reserved %d", jobType, compiler.RemedyJobTypeIndex)
	}
	rc := &recordingClient{id: "INC000000000101"}
	reg := remedy.NewRegistry()
	reg.Register("helix", rc)

	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.created) != 1 {
		t.Fatalf("entries created = %d, want 1", len(rc.created))
	}
	e := rc.created[0]
	if e.Form != "HPD:IncidentInterface_Create" {
		t.Errorf("form = %q, want the incident form", e.Form)
	}
	if e.Values["Description"] != "Disk full" || e.Values["Impact"] != "2-Significant" {
		t.Errorf("values = %v, want the two literal fields", e.Values)
	}
	if e.RequestID == "" {
		t.Error("RequestID is empty, want the job key for idempotency")
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0 (instance finished)", pi, ei)
	}
}

// TestRemedyConnectorFeelFields is the end-to-end fx slice: the worker evaluates the
// FEEL form and field values over the instance's variables (including mixed kinds and
// the processInstanceKey builtin) at call time.
func TestRemedyConnectorFeelFields(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Expr: compile(`formName`)},
		Fields: []compiler.RestKV{
			{Name: "Summary", Val: compiler.RestExpr{Expr: compile(`"Ticket for " + customer`)}},
			{Name: "Priority", Val: compiler.RestExpr{Expr: compile(`if urgent then "Critical" else "Low"`)}},
			{Name: "Qty", Val: compiler.RestExpr{Expr: compile(`string(qty)`)}},
			{Name: "Instance", Val: compiler.RestExpr{Expr: compile(`processInstanceKey`)}},
		},
		Retries: 3,
	})
	rc := &recordingClient{id: "INC1"}
	reg := remedy.NewRegistry()
	reg.Register("helix", rc)
	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "formName", Kind: model.VarString, Text: "HPD:Help Desk"},
		model.VariableValue{Name: "customer", Kind: model.VarString, Text: "Ada"},
		model.VariableValue{Name: "urgent", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "qty", Kind: model.VarNumber, Text: "5"},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.created) != 1 {
		t.Fatalf("entries created = %d, want 1", len(rc.created))
	}
	e := rc.created[0]
	if e.Form != "HPD:Help Desk" {
		t.Errorf("form = %q, want the FEEL-resolved form", e.Form)
	}
	if e.Values["Summary"] != "Ticket for Ada" {
		t.Errorf("Summary = %v, want the FEEL-computed summary", e.Values["Summary"])
	}
	if e.Values["Priority"] != "Critical" {
		t.Errorf("Priority = %v, want the bool branch taken", e.Values["Priority"])
	}
	if e.Values["Qty"] != "5" {
		t.Errorf("Qty = %v, want the number rendered", e.Values["Qty"])
	}
	if e.Values["Instance"] == "" || e.Values["Instance"] == "0" {
		t.Errorf("Instance = %v, want the processInstanceKey rendered", e.Values["Instance"])
	}
}

// TestRemedyConnectorNoForm proves a task whose form resolves to empty (an absent FEEL
// variable) fails the job (incident) rather than calling Remedy with no form.
func TestRemedyConnectorNoForm(t *testing.T) {
	log, store := openStore(t)
	e, err := expr.CompileAuto(`missing`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Expr: e}, // unbound → FEEL null → ""
		Retries:   3,
	})
	rc := &recordingClient{}
	reg := remedy.NewRegistry()
	reg.Register("helix", rc)
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.created) != 0 {
		t.Errorf("entries created = %d, want 0", len(rc.created))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestRemedyConnectorUnknownConnector proves a task naming an unregistered connector
// fails the job (retry, then incident) rather than dropping the create silently.
func TestRemedyConnectorUnknownConnector(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "ghost",
		Form:      compiler.RestExpr{Literal: "HPD:Help Desk"},
		Retries:   3,
	})
	if err := drive(t, cp, jobType, remedy.NewRegistry(), store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked: connector not registered)", pi)
	}
}

// TestRemedyConnectorClientError proves an AR System error fails the job (retried,
// then an incident) rather than completing it.
func TestRemedyConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Literal: "HPD:Help Desk"},
		Retries:   3,
	})
	reg := remedy.NewRegistry()
	reg.Register("helix", erroringClient{})
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestRemedyConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestRemedyConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := remedyProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Literal: "HPD:Help Desk"},
		Retries:   3,
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, remedy.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, remedy.NewRegistry()))
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestRegistryReplace covers rebuilding the registry from managed configuration: a
// Replace swaps the whole client set, and a nil map clears it (ADR-0041).
func TestRegistryReplace(t *testing.T) {
	reg := remedy.NewRegistry()
	reg.Register("old", &recordingClient{})
	reg.Replace(map[string]remedy.Client{"new": &recordingClient{}})
	if _, ok := reg.Client("old"); ok {
		t.Error("old connector should be gone after Replace")
	}
	if _, ok := reg.Client("new"); !ok {
		t.Error("new connector should be present after Replace")
	}
	reg.Replace(nil)
	if _, ok := reg.Client("new"); ok {
		t.Error("Replace(nil) should clear the registry")
	}
}

// TestRemedyHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestRemedyHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := remedy.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, remedy.NewRegistry())
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil nil", out, err)
	}
}
