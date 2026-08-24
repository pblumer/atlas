package sharepoint_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the item-creation requests a connector task makes and
// returns a canned created item.
type recordingClient struct {
	requests []sharepoint.ItemRequest
	item     any
}

func (r *recordingClient) CreateItem(_ context.Context, req sharepoint.ItemRequest) (any, error) {
	r.requests = append(r.requests, req)
	return r.item, nil
}

type erroringClient struct{}

func (erroringClient) CreateItem(context.Context, sharepoint.ItemRequest) (any, error) {
	return nil, context.DeadlineExceeded
}

const spDefKey = 93

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

func mustActiveProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}

func soleInstanceKey(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("live instances = %d, want exactly 1", len(keys))
	}
	return keys[0]
}

func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var found *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			cp := *v
			found = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return found
}

// spProcess: Start → SharePoint connector task → End.
func spProcess(t *testing.T, cfg compiler.SharePointConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(spDefKey, "incidents", 1)
	start := b.AddStartEvent()
	call := b.AddSharePointConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

// spThenWaitProcess: Start → SharePoint task (writes resultVar) → plain service task
// (parks, so the instance stays alive) → End, so a test can read the written result
// variable before the instance ends.
func spThenWaitProcess(t *testing.T, cfg compiler.SharePointConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(spDefKey, "incidents", 1)
	start := b.AddStartEvent()
	call := b.AddSharePointConnectorTask(cfg)
	wait := b.AddServiceTask("wait", 3)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, reg *sharepoint.Registry, store *state.Store, log *wal.Log, vars ...model.VariableValue) error {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return sharepoint.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
	})
	p.CreateInstance(cp.Key, vars...)
	return runner.Drive()
}

func compileExpr(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	e, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return e
}

// TestSharePointConnectorCreatesItem is the vertical slice end to end: a SharePoint
// connector task creates a job, the in-process worker resolves the named provider and
// creates the model-authored list item keyed by the job key, writes the created
// item's JSON into the result variable, and the token advances (parking on a
// following task so the written variable is still readable).
func TestSharePointConnectorCreatesItem(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spThenWaitProcess(t, compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Literal: "contoso.sharepoint.com,site,web"},
		List:      compiler.RestExpr{Literal: "Incidents"},
		Fields: []compiler.RestKV{
			{Name: "Title", Val: compiler.RestExpr{Literal: "Order shipped"}},
		},
		ResultVar: "created",
		Retries:   3,
	})
	if jobType != compiler.SharePointJobTypeIndex {
		t.Fatalf("SharePoint job type index = %d, want the reserved %d", jobType, compiler.SharePointJobTypeIndex)
	}
	rc := &recordingClient{item: map[string]any{"id": "42", "webUrl": "https://contoso.sharepoint.com/x"}}
	reg := sharepoint.NewRegistry()
	reg.Register("contoso", rc)

	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Site != "contoso.sharepoint.com,site,web" || req.List != "Incidents" {
		t.Errorf("request site/list = %q / %q, want the model values", req.Site, req.List)
	}
	if req.Fields["Title"] != "Order shipped" {
		t.Errorf("request field Title = %q, want the literal", req.Fields["Title"])
	}
	if req.RequestID == "" {
		t.Error("request id is empty, want the job key")
	}
	scope := soleInstanceKey(t, store)
	created := readVar(t, store, scope, "created")
	if created == nil || created.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON", created)
	}
	if !contains(created.Text, `"id"`) {
		t.Errorf("result variable text = %q, want the created item JSON", created.Text)
	}
}

// TestSharePointConnectorFeelFields is the end-to-end fx slice: the worker evaluates
// FEEL site/list/field values over the instance's variables at call time.
func TestSharePointConnectorFeelFields(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spProcess(t, compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Expr: compileExpr(t, `siteRef`)},
		List:      compiler.RestExpr{Expr: compileExpr(t, `"List " + listName`)},
		Fields: []compiler.RestKV{
			{Name: "Title", Val: compiler.RestExpr{Expr: compileExpr(t, `"#" + string(num) + " for " + processInstanceKey`)}},
			{Name: "Static", Val: compiler.RestExpr{Literal: "x"}},
		},
		Retries: 3,
	})
	rc := &recordingClient{item: map[string]any{"id": "1"}}
	reg := sharepoint.NewRegistry()
	reg.Register("contoso", rc)
	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "siteRef", Kind: model.VarString, Text: "s-1"},
		model.VariableValue{Name: "listName", Kind: model.VarString, Text: "Ops"},
		model.VariableValue{Name: "num", Kind: model.VarNumber, Text: "7"},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Site != "s-1" || req.List != "List Ops" {
		t.Errorf("site/list = %q / %q, want the FEEL-computed values", req.Site, req.List)
	}
	if req.Fields["Title"][:2] != "#7" {
		t.Errorf("Title = %q, want the number and processInstanceKey rendered", req.Fields["Title"])
	}
	if req.Fields["Static"] != "x" {
		t.Errorf("Static = %q, want the literal", req.Fields["Static"])
	}
}

// TestSharePointConnectorNoResultVariable proves a task with no result variable
// discards the created item and the instance still finishes.
func TestSharePointConnectorNoResultVariable(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spProcess(t, compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Literal: "s"},
		List:      compiler.RestExpr{Literal: "l"},
		Retries:   3,
	})
	rc := &recordingClient{item: map[string]any{"id": "1"}}
	reg := sharepoint.NewRegistry()
	reg.Register("contoso", rc)
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0 (instance finished)", pi, ei)
	}
}

// TestSharePointConnectorUnknownConnector proves a task naming an unregistered
// connector fails the job (retry, then incident) rather than dropping it silently.
func TestSharePointConnectorUnknownConnector(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spProcess(t, compiler.SharePointConfig{
		Connector: "ghost",
		Site:      compiler.RestExpr{Literal: "s"},
		List:      compiler.RestExpr{Literal: "l"},
		Retries:   3,
	})
	if err := drive(t, cp, jobType, sharepoint.NewRegistry(), store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked: connector not registered)", pi)
	}
}

// TestSharePointConnectorClientError proves a provider error fails the job (retried,
// then an incident) rather than completing it.
func TestSharePointConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spProcess(t, compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Literal: "s"},
		List:      compiler.RestExpr{Literal: "l"},
		Retries:   3,
	})
	reg := sharepoint.NewRegistry()
	reg.Register("contoso", erroringClient{})
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestSharePointConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestSharePointConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := spProcess(t, compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Literal: "s"},
		List:      compiler.RestExpr{Literal: "l"},
		Retries:   3,
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return sharepoint.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, sharepoint.NewRegistry())
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestSharePointHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestSharePointHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := sharepoint.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, sharepoint.NewRegistry())
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestSharePointConnectorSeesInputMappedLocal proves the worker resolves its FEEL
// fields up the scope chain: an input mapping computes a local, and a field reads it
// beside an inherited process variable (ADR-0068). Reading the process-instance scope
// flat left the local invisible, so the field rendered without it.
func TestSharePointConnectorSeesInputMappedLocal(t *testing.T) {
	log, store := openStore(t)
	b := compiler.NewBuilder(spDefKey, "incidents", 1)
	start := b.AddStartEvent()
	call := b.AddSharePointConnectorTask(compiler.SharePointConfig{
		Connector: "contoso",
		Site:      compiler.RestExpr{Literal: "s-1"},
		List:      compiler.RestExpr{Expr: compileExpr(t, `listRef`)},
		Fields:    []compiler.RestKV{{Name: "Title", Val: compiler.RestExpr{Expr: compileExpr(t, `listRef + " / " + listName`)}}},
		Retries:   3,
	})
	b.AddInputMapping(call, "listRef", compileExpr(t, `"List " + listName`))
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(call).Detail).JobType

	rc := &recordingClient{item: map[string]any{"id": "1"}}
	reg := sharepoint.NewRegistry()
	reg.Register("contoso", rc)
	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "listName", Kind: model.VarString, Text: "Ops"},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.List != "List Ops" {
		t.Errorf("list = %q, want the input-mapped local", req.List)
	}
	if req.Fields["Title"] != "List Ops / Ops" {
		t.Errorf("Title = %q, want the mapped local beside the inherited variable", req.Fields["Title"])
	}
}
