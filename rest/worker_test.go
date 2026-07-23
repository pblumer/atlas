package rest_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/rest"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the requests a connector task makes and returns a
// canned response.
type recordingClient struct {
	requests []rest.Request
	resp     rest.Response
}

func (r *recordingClient) Do(_ context.Context, req rest.Request) (rest.Response, error) {
	r.requests = append(r.requests, req)
	return r.resp, nil
}

const restDefKey = 71

// restProcess: Start → REST connector task (method/path) → End.
func restProcess(t *testing.T, method, path string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(restDefKey, "customers", 1)
	start := b.AddStartEvent()
	call := b.AddRestConnectorTask("crm", method, path, 3)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
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

// TestRestConnectorTaskCallsAPI is the vertical slice end to end: a POST connector
// task creates a job, the in-process REST worker calls the registered connector
// with the instance's variables as the JSON body, reports the response to the
// sink, completes the job, and the instance finishes — proving Atlas drives a REST
// connector through the normal job path.
func TestRestConnectorTaskCallsAPI(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "POST", "/customers")

	rc := &recordingClient{resp: rest.Response{Status: 201, Body: map[string]any{"id": json.Number("7")}}}
	reg := rest.NewRegistry()
	reg.Register("crm", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	lookup := func(k uint64) *compiler.CompiledProcess {
		if k == cp.Key {
			return cp
		}
		return nil
	}
	var results []rest.Result
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, rest.Handler(store, lookup, reg, func(r rest.Result) { results = append(results, r) }))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "name", Kind: model.VarString, Text: "Ada"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Method != "POST" || req.Path != "/customers" {
		t.Errorf("request = %s %s, want POST /customers", req.Method, req.Path)
	}
	if req.Body["name"] != "Ada" {
		t.Errorf("request body name = %#v, want Ada", req.Body["name"])
	}
	if req.IdempotencyKey == "" {
		t.Error("request idempotency key is empty, want the job key")
	}
	if len(results) != 1 || results[0].Status != 201 {
		t.Fatalf("results = %+v, want one with status 201", results)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestRestConnectorGetSendsNoBody proves a GET connector task carries no request
// body (only methods that conventionally have one do). A nil sink is also
// exercised here.
func TestRestConnectorGetSendsNoBody(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "GET", "/customers/1")

	rc := &recordingClient{resp: rest.Response{Status: 200}}
	reg := rest.NewRegistry()
	reg.Register("crm", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg, nil))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "ignored", Kind: model.VarString, Text: "x"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	if rc.requests[0].Body != nil {
		t.Errorf("GET request body = %#v, want nil", rc.requests[0].Body)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestRestConnectorRecoversAcrossRestart runs to the waiting REST job, simulates a
// crash (reopen log and store), recovers by replaying the log, then lets the
// worker call the API and finish the instance — proving the connector job survives
// recovery like any other job. The idempotency key (the job key) is stable across
// replay, so a re-run after a crash would not double-call.
func TestRestConnectorRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := restProcess(t, "POST", "/customers")
	clock := &fixedClock{}
	lookup := func(uint64) *compiler.CompiledProcess { return cp }

	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p1 := engine.New(1, log1, store1, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "name", Kind: model.VarString, Text: "Bo"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi := mustActiveProcs(t, store1); pi != 1 {
		t.Fatalf("before crash: active=%d, want 1 (waiting on REST job)", pi)
	}
	store1.Close()
	log1.Close()

	// Replay into a fresh store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	t.Cleanup(func() { store2.Close(); log2.Close() })
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	rc := &recordingClient{resp: rest.Response{Status: 200}}
	reg := rest.NewRegistry()
	reg.Register("crm", rc)
	runner := job.NewRunner(store2, p2)
	runner.Handle(jobType, rest.Handler(store2, lookup, reg, nil))
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 || rc.requests[0].Body["name"] != "Bo" {
		t.Fatalf("after recovery requests = %+v, want one carrying name Bo", rc.requests)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestRestConnectorUnregistered proves a connector task whose connector is not
// registered leaves the job pending (the handler errors), so nothing is lost.
func TestRestConnectorUnregistered(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "POST", "/x")
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, rest.NewRegistry(), nil))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err == nil {
		t.Fatal("Drive with an unregistered connector: err = nil, want error")
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after failed Drive: active=%d, want 1 (job still pending)", pi)
	}
}

// TestRestConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestRestConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "POST", "/x")
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	reg := rest.NewRegistry()
	reg.Register("crm", &recordingClient{})
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, reg, nil))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err == nil {
		t.Fatal("Drive with an unresolvable definition: err = nil, want error")
	}
}

// TestRestHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestRestHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := rest.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, rest.NewRegistry(), nil)
	if err := h(job.Job{ElementInstanceKey: 424242}); err != nil {
		t.Fatalf("handler for a vanished element instance: err = %v, want nil", err)
	}
}

// TestRestConnectorClientError proves a client (transport/HTTP) error leaves the
// job pending so it is retried at-least-once.
func TestRestConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "GET", "/x")
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	reg := rest.NewRegistry()
	reg.Register("crm", &erroringClient{})
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg, nil))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err == nil {
		t.Fatal("Drive when the client errors: err = nil, want error")
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after failed Drive: active=%d, want 1 (job still pending)", pi)
	}
}

type erroringClient struct{}

func (erroringClient) Do(context.Context, rest.Request) (rest.Response, error) {
	return rest.Response{}, context.DeadlineExceeded
}
