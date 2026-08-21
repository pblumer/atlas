package clio_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the events a connector task writes and serves canned
// read/query results back.
type recordingClient struct {
	events     []clio.Event
	state      map[string]any
	queryOut   any
	readOut    []clio.InboundEvent
	querySubj  string
	queryWhere string
}

func (r *recordingClient) WriteEvent(_ context.Context, e clio.Event) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recordingClient) GetState(_ context.Context, _, _ string) (map[string]any, error) {
	return r.state, nil
}

func (r *recordingClient) Query(_ context.Context, subject, where string) (any, error) {
	r.querySubj, r.queryWhere = subject, where
	return r.queryOut, nil
}

func (r *recordingClient) ReadEvents(_ context.Context, _ clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	return r.readOut, nil
}

const connDefKey = 55

var errBoom = errors.New("clio unreachable")

// errClient fails every operation, so a handler that calls it returns an error and
// the job stays pending (at-least-once retry, then an incident).
type errClient struct{ err error }

func (e *errClient) WriteEvent(context.Context, clio.Event) error { return e.err }
func (e *errClient) GetState(context.Context, string, string) (map[string]any, error) {
	return nil, e.err
}
func (e *errClient) Query(context.Context, string, string) (any, error) { return nil, e.err }
func (e *errClient) ReadEvents(context.Context, clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	return nil, e.err
}

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
	runner.Handle(jobType, func(rd state.Reader) job.Handler { return clio.Handler(store, lookup, reg) })

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
	runner.Handle(jobType, func(rd state.Reader) job.Handler { return clio.Handler(store2, lookup, reg) })
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
	runner.Handle(jobType, func(rd state.Reader) job.Handler { return clio.Handler(store, lookup, clio.NewRegistry()) }) // empty registry

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive with an unregistered connector: %v, want nil (failure routed to an incident)", err)
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
	runner.Handle(jobType, func(rd state.Reader) job.Handler {
		return clio.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, reg)
	})

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive with an unresolvable definition: %v, want nil (failure routed to an incident)", err)
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

// soleInstanceKey returns the key of the single live process instance.
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

// readVar returns the variable named name in scope, or nil if absent.
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

// clioReadThenWaitProcess: Start → clio task (writes resultVar) → plain service
// task (parks) → End, so a test can read the written result before the instance
// ends. build wires the specific clio task under test.
func clioReadThenWaitProcess(t *testing.T, add func(b *compiler.Builder) int32) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(connDefKey, "orders", 1)
	start := b.AddStartEvent()
	call := add(b)
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

// TestClioQueryTaskWritesResult drives a clio query task end to end: the worker runs
// the query on the registered connector and writes the result into the task's result
// variable, which the parked instance still holds.
func TestClioQueryTaskWritesResult(t *testing.T) {
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

	cp, jobType := clioReadThenWaitProcess(t, func(b *compiler.Builder) int32 {
		return b.AddClioQueryTask("orders-clio", "", "", "select count(*)", "total", 3)
	})
	rc := &recordingClient{queryOut: map[string]any{"count": float64(7)}}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return clio.QueryHandler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
	})

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	scope := soleInstanceKey(t, store)
	total := readVar(t, store, scope, "total")
	if total == nil {
		t.Fatal("result variable total not written")
	}
	if total.Kind != model.VarJSON {
		t.Errorf("total kind = %v, want VarJSON (an object)", total.Kind)
	}
}

// TestClioResultDiscarded drives query and read tasks whose result variable is
// empty: the worker performs the call but writes nothing back, and the instance
// still advances (the discard branch each OutputHandler takes).
func TestClioResultDiscarded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		add     func(b *compiler.Builder) int32
		handler func(state.Reader, clio.ProcessLookup, *clio.Registry) job.OutputHandler
	}{
		{"query", func(b *compiler.Builder) int32 { return b.AddClioQueryTask("orders-clio", "", "", "q", "", 3) }, clio.QueryHandler},
		{"read", func(b *compiler.Builder) int32 { return b.AddClioReadTask("orders-clio", "s", "", 0, 3) }, clio.ReadHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

			cp, jobType := clioReadThenWaitProcess(t, tc.add)
			reg := clio.NewRegistry()
			reg.Register("orders-clio", &recordingClient{})
			p := engine.New(1, log, store, &fixedClock{})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			runner := job.NewRunner(store, p)
			runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
				return tc.handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
			})
			p.CreateInstance(cp.Key)
			if err := runner.Drive(); err != nil {
				t.Fatalf("Drive: %v", err)
			}
			// The instance advanced past the clio task and parks on the wait task.
			if pi := mustActiveProcs(t, store); pi != 1 {
				t.Fatalf("active procs = %d, want 1 (parked on wait)", pi)
			}
		})
	}
}

// TestClioHandlerCallErrors proves that when the clio call fails, each output
// handler surfaces the error so the job stays pending (the instance parks), covering
// the run_query, get_state, and read_events error branches.
func TestClioHandlerCallErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		add     func(b *compiler.Builder) int32
		handler func(state.Reader, clio.ProcessLookup, *clio.Registry) job.OutputHandler
	}{
		{"run_query", func(b *compiler.Builder) int32 { return b.AddClioQueryTask("orders-clio", "", "", "q", "r", 3) }, clio.QueryHandler},
		{"get_state", func(b *compiler.Builder) int32 { return b.AddClioQueryTask("orders-clio", "s", "spec", "", "r", 3) }, clio.QueryHandler},
		{"read", func(b *compiler.Builder) int32 { return b.AddClioReadTask("orders-clio", "s", "r", 0, 3) }, clio.ReadHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

			cp, jobType := clioReadThenWaitProcess(t, tc.add)
			reg := clio.NewRegistry()
			reg.Register("orders-clio", &errClient{err: errBoom})
			p := engine.New(1, log, store, &fixedClock{})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			runner := job.NewRunner(store, p)
			runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
				return tc.handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
			})
			p.CreateInstance(cp.Key)
			if err := runner.Drive(); err != nil {
				t.Fatalf("Drive: %v", err)
			}
			// The clio call failed, so the token parks on the clio task (job pending).
			if pi := mustActiveProcs(t, store); pi != 1 {
				t.Fatalf("active procs = %d, want 1 (parked on the failed clio job)", pi)
			}
		})
	}
}

// TestClioQueryTaskGetStateBranch drives a query task with no query string, so the
// worker takes the get_state branch (subject + reduce spec) instead of run_query.
func TestClioQueryTaskGetStateBranch(t *testing.T) {
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

	cp, jobType := clioReadThenWaitProcess(t, func(b *compiler.Builder) int32 {
		return b.AddClioQueryTask("orders-clio", "orders/42", "orderTotals", "", "state", 3)
	})
	rc := &recordingClient{state: map[string]any{"total": float64(9)}}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return clio.QueryHandler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
	})

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	scope := soleInstanceKey(t, store)
	if got := readVar(t, store, scope, "state"); got == nil || got.Kind != model.VarJSON {
		t.Fatalf("state variable = %+v, want a VarJSON object", got)
	}
}

// TestClioReadTaskWritesEvents drives a clio read task: the worker reads the
// subject's events and writes them into the result variable as a JSON array.
func TestClioReadTaskWritesEvents(t *testing.T) {
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

	cp, jobType := clioReadThenWaitProcess(t, func(b *compiler.Builder) int32 {
		return b.AddClioReadTask("orders-clio", "orders/new", "events", 10, 3)
	})
	rc := &recordingClient{readOut: []clio.InboundEvent{
		{ID: "e1", Type: "OrderPlaced", Subject: "orders/new", Data: map[string]any{"orderId": "c-1"}},
		{ID: "e2", Type: "OrderPlaced", Subject: "orders/new", Data: map[string]any{"orderId": "c-2"}},
	}}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return clio.ReadHandler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg)
	})

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	scope := soleInstanceKey(t, store)
	events := readVar(t, store, scope, "events")
	if events == nil || events.Kind != model.VarJSON {
		t.Fatalf("events variable = %+v, want a VarJSON array", events)
	}
}

// mustCompile compiles a FEEL expression for an I/O mapping source, failing the
// test rather than the mapping if it does not compile.
func mustCompile(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("CompileAuto(%q): %v", src, err)
	}
	return c
}

// driveClio deploys cp, drives its clio write job with a recording client, and
// returns the client so a test can assert on the event that was written. vars seed
// the process instance.
func driveClio(t *testing.T, cp *compiler.CompiledProcess, jobType int32, vars ...model.VariableValue) *recordingClient {
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

	rc := &recordingClient{}
	reg := clio.NewRegistry()
	reg.Register("orders-clio", rc)

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(rd state.Reader) job.Handler { return clio.Handler(rd, lookup, reg) })

	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.events) != 1 {
		t.Fatalf("events written = %d, want 1", len(rc.events))
	}
	return rc
}

// TestWriteEventBodyIsTheInputMappings proves a write-events task's zeebe:ioMapping
// inputs *are* the event body: the mapped locals are sent, and the process variables
// they were computed from are not — the model says exactly what leaves the process
// (ADR-draft-clio-event-payload-is-the-input-mapping). Before this, the worker read
// the process-instance scope only, so a task whose payload came from input mappings
// wrote an empty body.
func TestWriteEventBodyIsTheInputMappings(t *testing.T) {
	b := compiler.NewBuilder(connDefKey, "orders", 1)
	start := b.AddStartEvent()
	write := b.AddClioWriteTask("orders-clio", "orders/new", "OrderPlaced", 3)
	b.AddInputMapping(write, "id", mustCompile(t, `orderId`))
	b.AddInputMapping(write, "label", mustCompile(t, `"order " + orderId`))
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(write).Detail).JobType

	rc := driveClio(t, cp, jobType,
		model.VariableValue{Name: "orderId", Kind: model.VarString, Text: "c-1"},
		model.VariableValue{Name: "internalNote", Kind: model.VarString, Text: "do not ship"})

	data := rc.events[0].Data
	if data["id"] != "c-1" || data["label"] != "order c-1" {
		t.Errorf("event data = %#v, want the two input-mapped values", data)
	}
	if len(data) != 2 {
		t.Errorf("event data = %#v, want exactly the mapped inputs (no process variables)", data)
	}
}

// TestWriteEventBodyWithoutMappingsIsTheVisibleScope covers the unmapped default: a
// task with no input mappings still sends every variable it *sees*, resolved up its
// scope chain (ADR-0068) — here a subprocess-scoped local alongside a process-level
// one, where reading the process-instance scope alone would have dropped the local.
func TestWriteEventBodyWithoutMappingsIsTheVisibleScope(t *testing.T) {
	b := compiler.NewBuilder(connDefKey, "orders", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.AddInputMapping(sub, "innerVal", mustCompile(t, `orderId + "-inner"`))
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	write := b.AddClioWriteTask("orders-clio", "orders/new", "OrderPlaced", 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, write)
	b.Connect(write, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(write).Detail).JobType

	rc := driveClio(t, cp, jobType, model.VariableValue{Name: "orderId", Kind: model.VarString, Text: "c-7"})

	data := rc.events[0].Data
	if data["orderId"] != "c-7" {
		t.Errorf("event data orderId = %#v, want c-7 (inherited from the process scope)", data["orderId"])
	}
	if data["innerVal"] != "c-7-inner" {
		t.Errorf("event data innerVal = %#v, want c-7-inner (the enclosing subprocess scope)", data["innerVal"])
	}
}
