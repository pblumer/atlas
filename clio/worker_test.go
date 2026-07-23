package clio_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/clio"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the events a connector task writes.
type recordingClient struct{ events []clio.Event }

func (r *recordingClient) WriteEvent(_ context.Context, e clio.Event) error {
	r.events = append(r.events, e)
	return nil
}

const connDefKey = 55

// connectorProcess: Start → clio write-events task → End.
func connectorProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(connDefKey, "orders", 1)
	start := b.AddStartEvent()
	write := b.AddClioWriteTask("orders-clio", "orders/new", "OrderPlaced", 3)
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(write).Detail).JobType
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

// TestConnectorTaskWritesToClio is the vertical slice end to end: a connector
// task creates a job, the in-process clio worker appends the instance's
// variables to the registered connector, completes the job, and the instance
// finishes — proving Atlas drives a clio connector through the normal job path.
func TestConnectorTaskWritesToClio(t *testing.T) {
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

	cp, jobType := connectorProcess(t)

	rc := &recordingClient{}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)

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
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, clio.Handler(store, lookup, reg))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "orderId", Kind: model.VarString, Text: "c-1"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(rc.events) != 1 {
		t.Fatalf("events written = %d, want 1", len(rc.events))
	}
	e := rc.events[0]
	if e.Subject != "orders/new" || e.Type != "OrderPlaced" {
		t.Errorf("event subject/type = %q/%q, want orders/new/OrderPlaced", e.Subject, e.Type)
	}
	if e.Data["orderId"] != "c-1" {
		t.Errorf("event data orderId = %#v, want c-1", e.Data["orderId"])
	}
	if e.IdempotencyKey == "" {
		t.Error("event idempotency key is empty, want the job key")
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestConnectorTaskRecoversAcrossRestart runs to the waiting clio job, simulates
// a crash (reopen log and store), recovers by replaying the log, then lets the
// worker write the event and finish the instance — proving the connector job
// survives recovery like any other job. The idempotency key (the job key) is
// stable across replay, so a re-run after a crash would not double-write.
func TestConnectorTaskRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := connectorProcess(t)
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
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "orderId", Kind: model.VarString, Text: "c-9"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	// The instance is parked on the connector job (nothing completed it yet).
	if pi := mustActiveProcs(t, store1); pi != 1 {
		t.Fatalf("before crash: active=%d, want 1 (waiting on connector job)", pi)
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

	rc := &recordingClient{}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)
	runner := job.NewRunner(store2, p2)
	runner.Handle(jobType, clio.Handler(store2, lookup, reg))
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.events) != 1 || rc.events[0].Data["orderId"] != "c-9" {
		t.Fatalf("after recovery events = %+v, want one carrying orderId c-9", rc.events)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestConnectorUnregistered proves that a connector task whose connector is not
// registered leaves the job pending (the handler errors), so nothing is lost:
// the instance stays parked and can proceed once the connector is configured.
func TestConnectorUnregistered(t *testing.T) {
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

	cp, jobType := connectorProcess(t)
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, clio.Handler(store, lookup, clio.NewRegistry())) // empty registry

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err == nil {
		t.Fatal("Drive with an unregistered connector: err = nil, want error")
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after failed Drive: active=%d, want 1 (job still pending)", pi)
	}
}

// TestConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestConnectorNoCompiledProcess(t *testing.T) {
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

	cp, jobType := connectorProcess(t)
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", &recordingClient{})
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, clio.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, reg))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err == nil {
		t.Fatal("Drive with an unresolvable definition: err = nil, want error")
	}
}

// TestHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestHandlerElementInstanceGone(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	h := clio.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, clio.NewRegistry())
	if err := h(job.Job{ElementInstanceKey: 424242}); err != nil {
		t.Fatalf("handler for a vanished element instance: err = %v, want nil", err)
	}
}

func mustActiveProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}
