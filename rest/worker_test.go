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

type erroringClient struct{}

func (erroringClient) Do(context.Context, rest.Request) (rest.Response, error) {
	return rest.Response{}, context.DeadlineExceeded
}

// noSecret is a resolver for tests that use no authentication.
func noSecret(string) string { return "" }

const restDefKey = 71

// restProcess: Start → REST connector task → End.
func restProcess(t *testing.T, method, url, resultVar string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(restDefKey, "customers", 1)
	start := b.AddStartEvent()
	call := b.AddRestConnectorTask(method, url, resultVar, nil, nil, compiler.RestAuth{}, 3)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

// restThenWaitProcess: Start → REST task (writes resultVar) → plain service task
// (parks, so the instance stays alive) → End. Parking after the REST task lets a
// test read the written result variable before the instance ends.
func restThenWaitProcess(t *testing.T, method, url, resultVar string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(restDefKey, "customers", 1)
	start := b.AddStartEvent()
	call := b.AddRestConnectorTask(method, url, resultVar, nil, nil, compiler.RestAuth{}, 3)
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

// TestRestConnectorWritesResponseToVariable is the vertical slice end to end: a
// POST connector task creates a job, the in-process REST worker calls the API with
// the instance's variables as the JSON body, writes the JSON response into the
// task's result variable, and the token advances. The process parks on a following
// task so the written variable is still readable.
func TestRestConnectorWritesResponseToVariable(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restThenWaitProcess(t, "POST", "https://api.example.com/customers", "created")
	if jobType != compiler.RestJobTypeIndex {
		t.Fatalf("REST job type index = %d, want the reserved %d", jobType, compiler.RestJobTypeIndex)
	}

	rc := &recordingClient{resp: rest.Response{Status: 201, Body: map[string]any{"id": json.Number("7"), "name": "Ada"}}}

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
	runner.HandleWithOutput(jobType, rest.Handler(store, lookup, rc, noSecret))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "name", Kind: model.VarString, Text: "Ada"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Method != "POST" || req.URL != "https://api.example.com/customers" {
		t.Errorf("request = %s %s, want POST the model URL", req.Method, req.URL)
	}
	if req.Body["name"] != "Ada" {
		t.Errorf("request body name = %#v, want Ada", req.Body["name"])
	}
	if req.IdempotencyKey == "" {
		t.Error("request idempotency key is empty, want the job key")
	}
	// The instance parked on the following task; the response is in "created".
	scope := soleInstanceKey(t, store)
	created := readVar(t, store, scope, "created")
	if created == nil || created.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON", created)
	}
	if !json.Valid([]byte(created.Text)) || !contains(created.Text, `"id"`) {
		t.Errorf("result variable text = %q, want the JSON response", created.Text)
	}
}

// TestRestConnectorGetNoBodyNoResult proves a GET carries no request body and, with
// no result variable, discards the response — the instance still finishes.
func TestRestConnectorGetNoBodyNoResult(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "GET", "https://api.example.com/customers/1", "")

	rc := &recordingClient{resp: rest.Response{Status: 200, Body: map[string]any{"id": json.Number("1")}}}

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, rc, noSecret))

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
	cp, jobType := restProcess(t, "POST", "https://api.example.com/customers", "")
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
	runner := job.NewRunner(store2, p2)
	runner.HandleWithOutput(jobType, rest.Handler(store2, lookup, rc, noSecret))
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

// TestRestConnectorClientError proves a client (transport/HTTP) error fails the
// job (retried, then an incident) rather than completing it.
func TestRestConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "GET", "https://api.example.com/x", "")
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, erroringClient{}, noSecret))

	p.CreateInstance(cp.Key)
	// A handler error does not abort Drive: it is routed into the incident model
	// (retry, then an incident that parks the token, ADR-0061), so the job never
	// completes and the instance stays active.
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestRestConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestRestConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restProcess(t, "GET", "https://api.example.com/x", "")
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret))

	p.CreateInstance(cp.Key)
	// An unresolvable definition is a handler error, routed into the incident model
	// like any other; Drive does not abort and the instance stays active.
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestRestHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestRestHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := rest.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret)
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

// restConfiguredProcess: Start → REST GET task carrying headers/query/auth → plain
// service task (parks). Lets a test assert the request the worker built.
func restConfiguredProcess(t *testing.T, headers, query map[string]string, auth compiler.RestAuth) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(restDefKey, "todos", 1)
	start := b.AddStartEvent()
	call := b.AddRestConnectorTask("GET", "https://api.example.com/todos", "", headers, query, auth, 3)
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

func driveRest(t *testing.T, cp *compiler.CompiledProcess, jobType int32, rc rest.Client, secret rest.SecretResolver, store *state.Store, log *wal.Log) error {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, rest.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, rc, secret))
	p.CreateInstance(cp.Key)
	return runner.Drive()
}

// TestRestConnectorAppliesHeadersQueryAuth proves the worker sends the model
// headers and query, and turns a bearer auth's secret *reference* into an
// Authorization header via the resolver (the token never being in the model).
func TestRestConnectorAppliesHeadersQueryAuth(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restConfiguredProcess(t,
		map[string]string{"Accept": "application/json"},
		map[string]string{"page": "1"},
		compiler.RestAuth{Type: "bearer", SecretRef: "TODO_TOKEN"})
	rc := &recordingClient{resp: rest.Response{Status: 200}}
	secret := func(ref string) string {
		if ref == "TODO_TOKEN" {
			return "s3cr3t"
		}
		return ""
	}
	if err := driveRest(t, cp, jobType, rc, secret, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Headers["Accept"] != "application/json" {
		t.Errorf("Accept header = %q, want application/json", req.Headers["Accept"])
	}
	if req.Headers["Authorization"] != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the resolved bearer token", req.Headers["Authorization"])
	}
	if req.Query["page"] != "1" {
		t.Errorf("query page = %q, want 1", req.Query["page"])
	}
}

// TestRestConnectorBasicAuth checks HTTP Basic: username (model) + resolved secret
// become a base64 Authorization header.
func TestRestConnectorBasicAuth(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restConfiguredProcess(t, nil, nil,
		compiler.RestAuth{Type: "basic", Username: "ada", SecretRef: "PW"})
	rc := &recordingClient{resp: rest.Response{Status: 200}}
	if err := driveRest(t, cp, jobType, rc, func(string) string { return "lovelace" }, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	// base64("ada:lovelace") == "YWRhOmxvdmVsYWNl"
	if got := rc.requests[0].Headers["Authorization"]; got != "Basic YWRhOmxvdmVsYWNl" {
		t.Errorf("Authorization = %q, want Basic base64(ada:lovelace)", got)
	}
}

// TestRestConnectorApiKeyAuth checks an api-key: the resolved secret goes into the
// named header.
func TestRestConnectorApiKeyAuth(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restConfiguredProcess(t, nil, nil,
		compiler.RestAuth{Type: "apikey", ApiKeyName: "X-API-Key", SecretRef: "K"})
	rc := &recordingClient{resp: rest.Response{Status: 200}}
	if err := driveRest(t, cp, jobType, rc, func(string) string { return "abc123" }, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if got := rc.requests[0].Headers["X-API-Key"]; got != "abc123" {
		t.Errorf("X-API-Key = %q, want the resolved key", got)
	}
}

// TestRestConnectorAuthSecretMissing proves a configured auth whose secret is not
// resolvable fails the job (incident) rather than calling the API unauthenticated:
// no request is made and the instance stays active.
func TestRestConnectorAuthSecretMissing(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := restConfiguredProcess(t, nil, nil,
		compiler.RestAuth{Type: "bearer", SecretRef: "MISSING"})
	rc := &recordingClient{resp: rest.Response{Status: 200}}
	if err := driveRest(t, cp, jobType, rc, noSecret, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 0 {
		t.Errorf("requests = %d, want 0 (unauthenticated call refused)", len(rc.requests))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}
