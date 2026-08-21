package soap_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/soap"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the requests a connector task makes and returns a canned
// response.
type recordingClient struct {
	requests []soap.Request
	resp     soap.Response
}

func (r *recordingClient) Do(_ context.Context, req soap.Request) (soap.Response, error) {
	r.requests = append(r.requests, req)
	return r.resp, nil
}

type erroringClient struct{}

func (erroringClient) Do(context.Context, soap.Request) (soap.Response, error) {
	return soap.Response{}, context.DeadlineExceeded
}

func noSecret(string) string { return "" }

const soapDefKey = 91

const soapEndpoint = "https://ws.example.com/UserService"

func lit(s string) compiler.RestExpr { return compiler.RestExpr{Literal: s} }

// soapProcess: Start → SOAP connector task → End.
func soapProcess(t *testing.T, cfg compiler.SoapConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	return soapProcessWith(t, cfg, false)
}

// soapThenWaitProcess parks on a plain service task after the SOAP task, so a test can
// read a written result variable before the instance ends.
func soapThenWaitProcess(t *testing.T, cfg compiler.SoapConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	return soapProcessWith(t, cfg, true)
}

func soapProcessWith(t *testing.T, cfg compiler.SoapConfig, park bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	if cfg.Version == "" {
		cfg.Version = "1.1"
	}
	b := compiler.NewBuilder(soapDefKey, "identities", 1)
	start := b.AddStartEvent()
	call := b.AddSoapConnectorTask(cfg)
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

// drive deploys cp, registers the SOAP worker, creates one instance with vars, and
// drives the runner to quiescence.
func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, client soap.Client, secret soap.SecretResolver, store *state.Store, log *wal.Log, vars ...model.VariableValue) {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return soap.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, client, secret)
	})
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
}

// TestSoapInvokeWritesResponseToVariable is the vertical slice end to end: a SOAP task
// creates a job, the in-process SOAP worker wraps the FEEL-built body in an envelope,
// calls the service, writes the parsed response into the task's result variable, and the
// token advances. The process parks so the written variable is readable.
func TestSoapInvokeWritesResponseToVariable(t *testing.T) {
	log, store := openStore(t)
	// The body is a FEEL expression interpolating the instance's userId into the XML.
	bodyExpr, err := expr.CompileAuto(`"<tns:GetUser xmlns:tns=\"urn:x\"><id>" + userId + "</id></tns:GetUser>"`)
	if err != nil {
		t.Fatalf("compile body: %v", err)
	}
	cp, jobType := soapThenWaitProcess(t, compiler.SoapConfig{
		Endpoint:  lit(soapEndpoint),
		Op:        "GetUser",
		Body:      compiler.RestExpr{Expr: bodyExpr},
		ResultVar: "user",
	})
	if jobType != compiler.SoapJobTypeIndex {
		t.Fatalf("SOAP job type index = %d, want the reserved %d", jobType, compiler.SoapJobTypeIndex)
	}
	rc := &recordingClient{resp: soap.Response{Status: 200, Body: map[string]any{"GetUserResponse": map[string]any{"id": "7"}}}}

	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "userId", Kind: model.VarString, Text: "7"})

	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.Endpoint != soapEndpoint || req.Operation != "GetUser" {
		t.Errorf("request = %s %s, want the SOAP endpoint and operation", req.Operation, req.Endpoint)
	}
	// A blank SOAPAction defaults to the operation name.
	if req.Action != "GetUser" {
		t.Errorf("action = %q, want the operation name as the default SOAPAction", req.Action)
	}
	if req.Version != "1.1" {
		t.Errorf("version = %q, want 1.1", req.Version)
	}
	if want := "<id>7</id>"; !strings.Contains(req.Body, want) {
		t.Errorf("request body = %q, want the FEEL-interpolated %q", req.Body, want)
	}
	scope := soleInstanceKey(t, store)
	got := readVar(t, store, scope, "user")
	if got == nil || got.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON", got)
	}
}

// TestSoapNoResultVarDiscardsResponse proves a task with no result variable discards the
// response — the instance still finishes.
func TestSoapNoResultVarDiscardsResponse(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "Ping", Body: lit("<Ping/>"),
	})
	rc := &recordingClient{resp: soap.Response{Status: 200, Body: map[string]any{"ok": "true"}}}
	drive(t, cp, jobType, rc, noSecret, store, log)

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestSoapExplicitActionAndVersion proves an explicit SOAPAction and 1.2 version reach
// the client verbatim.
func TestSoapExplicitActionAndVersion(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "Provision", Action: lit("urn:Provision"), Version: "1.2", Body: lit("<Provision/>"),
	})
	rc := &recordingClient{resp: soap.Response{Status: 200}}
	drive(t, cp, jobType, rc, noSecret, store, log)
	req := rc.requests[0]
	if req.Action != "urn:Provision" || req.Version != "1.2" {
		t.Errorf("request action/version = %q/%q, want urn:Provision/1.2", req.Action, req.Version)
	}
}

// TestSoapAuthAppliesBasic turns a basic auth's secret *reference* into an Authorization
// header via the resolver (the credential never being in the model).
func TestSoapAuthAppliesBasic(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "GetUser", Body: lit("<x/>"),
		Auth: compiler.RestAuth{Type: "basic", Username: "svc", SecretRef: "WS_PASSWORD"},
	})
	rc := &recordingClient{resp: soap.Response{Status: 200}}
	secret := func(ref string) string {
		if ref == "WS_PASSWORD" {
			return "s3cr3t"
		}
		return ""
	}
	drive(t, cp, jobType, rc, secret, store, log)
	// base64("svc:s3cr3t") == "c3ZjOnMzY3IzdA=="
	if got := rc.requests[0].Headers["Authorization"]; got != "Basic c3ZjOnMzY3IzdA==" {
		t.Errorf("Authorization = %q, want the resolved basic credential", got)
	}
}

// TestSoapAuthSecretMissing proves a configured auth whose secret is not resolvable fails
// the job (incident) rather than calling the service unauthenticated.
func TestSoapAuthSecretMissing(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "GetUser", Body: lit("<x/>"),
		Auth: compiler.RestAuth{Type: "bearer", SecretRef: "MISSING"},
	})
	rc := &recordingClient{resp: soap.Response{Status: 200}}
	drive(t, cp, jobType, rc, noSecret, store, log)
	if len(rc.requests) != 0 {
		t.Errorf("requests = %d, want 0 (unauthenticated call refused)", len(rc.requests))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestSoapEmptyEndpoint proves the worker refuses a task whose FEEL endpoint evaluates
// to empty (the referenced variable is unset) rather than calling an empty URL — the job
// fails into an incident.
func TestSoapEmptyEndpoint(t *testing.T) {
	log, store := openStore(t)
	endpoint, err := expr.CompileAuto("serviceUrl") // unset on the instance → empty
	if err != nil {
		t.Fatalf("compile endpoint: %v", err)
	}
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: compiler.RestExpr{Expr: endpoint}, Op: "GetUser", Body: lit("<x/>"),
	})
	rc := &recordingClient{resp: soap.Response{Status: 200}}
	drive(t, cp, jobType, rc, noSecret, store, log)
	if len(rc.requests) != 0 {
		t.Errorf("requests = %d, want 0 (empty endpoint refused)", len(rc.requests))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestSoapClientError proves a client (transport/HTTP) error fails the job (retried, then
// an incident) rather than completing it.
func TestSoapClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "GetUser", Body: lit("<x/>"),
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return soap.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, erroringClient{}, noSecret)
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestSoapNoCompiledProcess covers the worker's guard for a job whose definition can't be
// resolved: the handler errors, leaving the job pending.
func TestSoapNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "GetUser", Body: lit("<x/>"),
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return soap.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret)
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestSoapHandlerElementInstanceGone covers the handler's guard for a job whose element
// instance has already completed: a no-op, not an error.
func TestSoapHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := soap.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{}, noSecret)
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

// TestSoapRecoversAcrossRestart runs to the waiting SOAP job, simulates a crash (reopen
// log and store), recovers by replaying the log, then lets the worker call the service
// and finish the instance — proving the connector job survives recovery like any other
// job.
func TestSoapRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := soapProcess(t, compiler.SoapConfig{
		Endpoint: lit(soapEndpoint), Op: "Provision", Body: lit("<Provision><id>bo</id></Provision>"),
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
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi := mustActiveProcs(t, store1); pi != 1 {
		t.Fatalf("before crash: active=%d, want 1 (waiting on SOAP job)", pi)
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
	rc := &recordingClient{resp: soap.Response{Status: 200}}
	runner := job.NewRunner(store2, p2)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler { return soap.Handler(store2, lookup, rc, noSecret) })
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 || !strings.Contains(rc.requests[0].Body, "<id>bo</id>") {
		t.Fatalf("after recovery requests = %+v, want one carrying the provision body", rc.requests)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestSoapConnectorSeesInputMappedLocal proves the worker resolves its FEEL endpoint,
// action and body up the scope chain, so an input-mapped local is visible to them
// beside what the task inherits (ADR-0068).
func TestSoapConnectorSeesInputMappedLocal(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	b := compiler.NewBuilder(soapDefKey, "billing", 1)
	start := b.AddStartEvent()
	call := b.AddSoapConnectorTask(compiler.SoapConfig{
		Endpoint: lit(soapEndpoint),
		Op:       "GetUser",
		Body:     compiler.RestExpr{Expr: compile(`"<id>" + tag + "</id>"`)},
		Retries:  3,
	})
	b.AddInputMapping(call, "tag", compile(`"u-" + userId`))
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(call).Detail).JobType

	rc := &recordingClient{resp: soap.Response{Status: 200}}
	drive(t, cp, jobType, rc, noSecret, store, log,
		model.VariableValue{Name: "userId", Kind: model.VarString, Text: "7"})

	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	if want := "<id>u-7</id>"; !strings.Contains(rc.requests[0].Body, want) {
		t.Errorf("request body = %q, want the input-mapped %q", rc.requests[0].Body, want)
	}
}
