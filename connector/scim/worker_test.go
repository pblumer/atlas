package scim_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/scim"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the requests a task makes and returns a canned
// response.
type recordingClient struct {
	requests []scim.Request
	resp     scim.Response
}

func (r *recordingClient) Do(_ context.Context, req scim.Request) (scim.Response, error) {
	r.requests = append(r.requests, req)
	return r.resp, nil
}

type erroringClient struct{}

func (erroringClient) Do(context.Context, scim.Request) (scim.Response, error) {
	return scim.Response{}, context.DeadlineExceeded
}

func noSecret(string) string { return "" }

const scimDefKey = 83

const scimBase = "https://idp.example.com/scim/v2"

// scimProcess: Start → SCIM task → End.
func scimProcess(t *testing.T, cfg compiler.ScimConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	return scimProcessWith(t, cfg, false)
}

// scimThenWaitProcess parks on a plain service task after the SCIM task, so a test
// can read a written result variable before the instance ends.
func scimThenWaitProcess(t *testing.T, cfg compiler.ScimConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	return scimProcessWith(t, cfg, true)
}

func scimProcessWith(t *testing.T, cfg compiler.ScimConfig, park bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	b := compiler.NewBuilder(scimDefKey, "identities", 1)
	start := b.AddStartEvent()
	call := b.AddScimConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, call)
	if park {
		wait := b.AddServiceTask("wait", 3)
		b.Connect(call, wait)
		b.Connect(wait, end)
	} else {
		b.Connect(call, end)
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

func lit(s string) compiler.RestExpr { return compiler.RestExpr{Literal: s} }

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
	var err error
	if pi, err = s.ActiveProcessInstanceCount(); err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	if ei, err = s.ActiveElementInstanceCount(); err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
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

// drive deploys cp, registers the SCIM worker, creates one instance with vars, and
// drives the runner to quiescence.
func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, client scim.Client, secret scim.SecretResolver, store *state.Store, log *wal.Log, vars ...model.VariableValue) {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return scim.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, client, secret)
	})
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
}

// TestScimCreateWritesResponseToVariable is the vertical slice end to end: a create
// (POST) task creates a job, the in-process SCIM worker calls the provider with the
// named body variable as the scim+json payload, writes the JSON response into the
// task's result variable, and the token advances. The process parks so the written
// variable is readable.
func TestScimCreateWritesResponseToVariable(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimThenWaitProcess(t, compiler.ScimConfig{
		BaseURL:   lit(scimBase),
		Resource:  lit("Users"),
		Op:        "create",
		BodyVar:   "resource",
		ResultVar: "created",
	})
	if jobType != compiler.ScimJobTypeIndex {
		t.Fatalf("SCIM job type index = %d, want the reserved %d", jobType, compiler.ScimJobTypeIndex)
	}
	rc := &recordingClient{resp: scim.Response{Status: 201, Body: map[string]any{"id": "u-7", "userName": "ada"}}}

	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "resource", Kind: model.VarJSON, Text: `{"userName":"ada","active":true}`})

	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Method != "POST" || req.URL != scimBase+"/Users" {
		t.Errorf("request = %s %s, want POST the Users collection", req.Method, req.URL)
	}
	if req.Body["userName"] != "ada" || req.Body["active"] != true {
		t.Errorf("request body = %#v, want the resource object", req.Body)
	}
	if req.IdempotencyKey == "" {
		t.Error("request idempotency key is empty, want the job key")
	}
	scope := soleInstanceKey(t, store)
	created := readVar(t, store, scope, "created")
	if created == nil || created.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON", created)
	}
}

// TestScimGetByIdNoBody proves a get (GET) addresses /{id}, carries no body, and
// with no result variable discards the response — the instance still finishes.
func TestScimGetByIdNoBody(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL:    lit(scimBase),
		Resource:   lit("Users"),
		Op:         "get",
		ResourceID: lit("u-9"),
	})
	rc := &recordingClient{resp: scim.Response{Status: 200, Body: map[string]any{"id": "u-9"}}}
	drive(t, cp, jobType, rc, noSecret, store, log)

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Method != "GET" || req.URL != scimBase+"/Users/u-9" {
		t.Errorf("request = %s %s, want GET the resource URL", req.Method, req.URL)
	}
	if req.Body != nil {
		t.Errorf("GET body = %#v, want nil", req.Body)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestScimSearchAppliesFilter proves a search evaluates its FEEL filter over the
// instance's variables and sends it as a query parameter.
func TestScimSearchAppliesFilter(t *testing.T) {
	log, store := openStore(t)
	// A FEEL filter built from the instance's variable, evaluated at call time.
	filter, err := expr.CompileAuto(`"userName eq " + userName`)
	if err != nil {
		t.Fatalf("compile filter: %v", err)
	}
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL:   lit(scimBase),
		Resource:  lit("Users"),
		Op:        "search",
		Filter:    compiler.RestExpr{Expr: filter},
		ResultVar: "hits",
	})
	rc := &recordingClient{resp: scim.Response{Status: 200, Body: map[string]any{"totalResults": json.Number("0"), "Resources": []any{}}}}
	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "userName", Kind: model.VarString, Text: "ada"})

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Method != "GET" || req.URL != scimBase+"/Users" {
		t.Errorf("request = %s %s, want GET the Users collection", req.Method, req.URL)
	}
	if req.Query["filter"] == "" {
		t.Errorf("filter query = %q, want the SCIM filter", req.Query["filter"])
	}
}

// TestScimAuthAppliesBearer turns a bearer auth's secret *reference* into an
// Authorization header via the resolver (the token never being in the model).
func TestScimAuthAppliesBearer(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL:    lit(scimBase),
		Resource:   lit("Users"),
		Op:         "get",
		ResourceID: lit("u-1"),
		Auth:       compiler.RestAuth{Type: "bearer", SecretRef: "SCIM_TOKEN"},
	})
	rc := &recordingClient{resp: scim.Response{Status: 200}}
	secret := func(ref string) string {
		if ref == "SCIM_TOKEN" {
			return "s3cr3t"
		}
		return ""
	}
	drive(t, cp, jobType, rc, secret, store, log)
	if got := rc.requests[0].Headers["Authorization"]; got != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the resolved bearer token", got)
	}
}

// TestScimAuthSecretMissing proves a configured auth whose secret is not resolvable
// fails the job (incident) rather than calling the provider unauthenticated.
func TestScimAuthSecretMissing(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL:    lit(scimBase),
		Resource:   lit("Users"),
		Op:         "get",
		ResourceID: lit("u-1"),
		Auth:       compiler.RestAuth{Type: "bearer", SecretRef: "MISSING"},
	})
	rc := &recordingClient{resp: scim.Response{Status: 200}}
	drive(t, cp, jobType, rc, noSecret, store, log)
	if len(rc.requests) != 0 {
		t.Errorf("requests = %d, want 0 (unauthenticated call refused)", len(rc.requests))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestScimClientError proves a client (transport/HTTP) error fails the job (retried,
// then an incident) rather than completing it.
func TestScimClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL: lit(scimBase), Resource: lit("Users"), Op: "get", ResourceID: lit("u-1"),
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return scim.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, erroringClient{}, noSecret)
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestScimNoCompiledProcess covers the worker's guard for a job whose definition
// can't be resolved: the handler errors, leaving the job pending.
func TestScimNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL: lit(scimBase), Resource: lit("Users"), Op: "get", ResourceID: lit("u-1"),
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return scim.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret)
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestScimHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: a no-op, not an error.
func TestScimHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := scim.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret)
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

// TestScimRecoversAcrossRestart runs to the waiting SCIM job, simulates a crash
// (reopen log and store), recovers by replaying the log, then lets the worker call
// the provider and finish the instance — proving the worker job survives recovery
// like any other job.
func TestScimRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := scimProcess(t, compiler.ScimConfig{
		BaseURL: lit(scimBase), Resource: lit("Users"), Op: "create", BodyVar: "resource",
	})
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
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "resource", Kind: model.VarJSON, Text: `{"userName":"bo"}`})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi := mustActiveProcs(t, store1); pi != 1 {
		t.Fatalf("before crash: active=%d, want 1 (waiting on SCIM job)", pi)
	}
	store1.Close()
	log1.Close()

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
	rc := &recordingClient{resp: scim.Response{Status: 201}}
	runner := job.NewRunner(store2, p2)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler { return scim.Handler(store2, lookup, rc, noSecret) })
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 || rc.requests[0].Body["userName"] != "bo" {
		t.Fatalf("after recovery requests = %+v, want one carrying userName bo", rc.requests)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestScimBodyIsTheInputMappings proves a SCIM task's zeebe:ioMapping inputs *are*
// its request body when it names no body variable: the mapped values are sent and the
// process variables they came from are not
// (ADR-0174).
func TestScimBodyIsTheInputMappings(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	b := compiler.NewBuilder(scimDefKey, "identities", 1)
	start := b.AddStartEvent()
	call := b.AddScimConnectorTask(compiler.ScimConfig{
		BaseURL: lit(scimBase), Resource: lit("Users"), Op: "create", Retries: 3,
	})
	b.AddInputMapping(call, "userName", compile(`login`))
	b.AddInputMapping(call, "active", compile(`true`))
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(call).Detail).JobType

	rc := &recordingClient{resp: scim.Response{Status: 201, Body: map[string]any{"id": "u-7"}}}
	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "login", Kind: model.VarString, Text: "ada"},
		model.VariableValue{Name: "internalNote", Kind: model.VarString, Text: "do not sync"})

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	body := rc.requests[0].Body
	if body["userName"] != "ada" || body["active"] != true {
		t.Errorf("body = %#v, want the two input-mapped values", body)
	}
	if len(body) != 2 {
		t.Errorf("body = %#v, want exactly the mapped inputs (no process variables)", body)
	}
}

// TestScimBodyVarFromInputMapping proves a named body variable is resolved up the
// scope chain too: an input mapping builds the SCIM resource, and the task sends it
// (ADR-0068). Reading the process-instance scope flat made the local invisible, so
// the worker failed the job as "body variable is not set on the instance".
func TestScimBodyVarFromInputMapping(t *testing.T) {
	log, store := openStore(t)
	src, err := expr.CompileAuto(`{userName: login, active: true}`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(scimDefKey, "identities", 1)
	start := b.AddStartEvent()
	call := b.AddScimConnectorTask(compiler.ScimConfig{
		BaseURL: lit(scimBase), Resource: lit("Users"), Op: "create", BodyVar: "resource", Retries: 3,
	})
	b.AddInputMapping(call, "resource", src)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(call).Detail).JobType

	rc := &recordingClient{resp: scim.Response{Status: 201, Body: map[string]any{"id": "u-7"}}}
	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "login", Kind: model.VarString, Text: "ada"})

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	if body := rc.requests[0].Body; body["userName"] != "ada" {
		t.Errorf("body = %#v, want the resource the input mapping built", body)
	}
}
