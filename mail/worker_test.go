package mail_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/mail"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the messages a mail connector task sends.
type recordingClient struct{ sent []mail.Message }

func (r *recordingClient) Send(_ context.Context, m mail.Message) error {
	r.sent = append(r.sent, m)
	return nil
}

type erroringClient struct{}

func (erroringClient) Send(context.Context, mail.Message) error { return context.DeadlineExceeded }

const mailDefKey = 91

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

// mailProcess: Start → mail connector task → End (the token finishes once the
// message is sent, since a mail task produces no result variable).
func mailProcess(t *testing.T, cfg compiler.MailConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(mailDefKey, "notify", 1)
	start := b.AddStartEvent()
	call := b.AddMailConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, reg *mail.Registry, store *state.Store, log *wal.Log, vars ...model.VariableValue) error {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, mail.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg))
	p.CreateInstance(cp.Key, vars...)
	return runner.Drive()
}

// TestMailConnectorSendsMessage is the vertical slice end to end: a mail connector
// task creates a job, the in-process mail worker resolves the named provider and
// sends the model-authored message keyed by the job key, and the token finishes.
func TestMailConnectorSendsMessage(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: "ops@example.com, team@example.com"},
		Cc:        compiler.RestExpr{Literal: "audit@example.com"},
		Subject:   compiler.RestExpr{Literal: "Order shipped"},
		Body:      compiler.RestExpr{Literal: "On its way."},
		Retries:   3,
	})
	if jobType != compiler.MailJobTypeIndex {
		t.Fatalf("mail job type index = %d, want the reserved %d", jobType, compiler.MailJobTypeIndex)
	}
	rc := &recordingClient{}
	reg := mail.NewRegistry()
	reg.Register("office365", rc)

	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(rc.sent))
	}
	m := rc.sent[0]
	if len(m.To) != 2 || m.To[0] != "ops@example.com" || m.To[1] != "team@example.com" {
		t.Errorf("To = %v, want the two trimmed recipients", m.To)
	}
	if len(m.Cc) != 1 || m.Cc[0] != "audit@example.com" {
		t.Errorf("Cc = %v, want audit@example.com", m.Cc)
	}
	if m.Subject != "Order shipped" || m.Body != "On its way." {
		t.Errorf("subject/body = %q / %q", m.Subject, m.Body)
	}
	if m.MessageID == "" {
		t.Error("MessageID is empty, want the job key for idempotency")
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0 (instance finished)", pi, ei)
	}
}

// TestMailConnectorFeelFields is the end-to-end fx slice: the worker evaluates FEEL
// recipient/subject/body values over the instance's variables at send time.
func TestMailConnectorFeelFields(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "gmail",
		To:        compiler.RestExpr{Expr: compile(`email`)},
		Subject:   compiler.RestExpr{Expr: compile(`"Hi " + name`)},
		Body:      compiler.RestExpr{Literal: "Welcome."},
		Retries:   3,
	})
	rc := &recordingClient{}
	reg := mail.NewRegistry()
	reg.Register("gmail", rc)

	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "email", Kind: model.VarString, Text: "ada@example.com"},
		model.VariableValue{Name: "name", Kind: model.VarString, Text: "Ada"},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(rc.sent))
	}
	m := rc.sent[0]
	if len(m.To) != 1 || m.To[0] != "ada@example.com" {
		t.Errorf("To = %v, want the evaluated address", m.To)
	}
	if m.Subject != "Hi Ada" {
		t.Errorf("subject = %q, want the FEEL-computed subject", m.Subject)
	}
}

// TestMailConnectorBindsAllVarKinds exercises the FEEL binding path over every
// stored variable kind (string, number, bool, JSON) and the reserved
// processInstanceKey builtin, so a body composed from mixed-type variables renders.
func TestMailConnectorBindsAllVarKinds(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "smtp",
		To:        compiler.RestExpr{Literal: "a@b.c"},
		Subject:   compiler.RestExpr{Expr: compile(`"order " + string(qty) + " for instance " + processInstanceKey`)},
		Body:      compiler.RestExpr{Expr: compile(`if paid then "Paid: " + string(cart) else "Unpaid"`)},
		Retries:   3,
	})
	rc := &recordingClient{}
	reg := mail.NewRegistry()
	reg.Register("smtp", rc)
	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "qty", Kind: model.VarNumber, Text: "3"},
		model.VariableValue{Name: "paid", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "cart", Kind: model.VarJSON, Text: `{"n":1}`},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(rc.sent))
	}
	if !strings.HasPrefix(rc.sent[0].Subject, "order 3 for instance ") {
		t.Errorf("subject = %q, want the number and processInstanceKey rendered", rc.sent[0].Subject)
	}
	if !strings.HasPrefix(rc.sent[0].Body, "Paid: ") {
		t.Errorf("body = %q, want the bool branch taken and the JSON rendered", rc.sent[0].Body)
	}
}

// TestMailConnectorNoRecipient proves a task whose recipient resolves to empty (an
// absent FEEL variable) fails the job (incident) rather than sending to no one.
func TestMailConnectorNoRecipient(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return e
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "gmail",
		To:        compiler.RestExpr{Expr: compile(`missing`)}, // unbound → FEEL null → ""
		Subject:   compiler.RestExpr{Literal: "x"},
		Body:      compiler.RestExpr{Literal: "y"},
		Retries:   3,
	})
	rc := &recordingClient{}
	reg := mail.NewRegistry()
	reg.Register("gmail", rc)
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.sent) != 0 {
		t.Errorf("messages sent = %d, want 0", len(rc.sent))
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestMailConnectorUnknownConnector proves a task naming an unregistered connector
// fails the job (retry, then incident) rather than dropping the message silently.
func TestMailConnectorUnknownConnector(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "ghost",
		To:        compiler.RestExpr{Literal: "a@b.c"},
		Subject:   compiler.RestExpr{Literal: "x"},
		Body:      compiler.RestExpr{Literal: "y"},
		Retries:   3,
	})
	if err := drive(t, cp, jobType, mail.NewRegistry(), store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked: connector not registered)", pi)
	}
}

// TestMailConnectorClientError proves a provider send error fails the job (retried,
// then an incident) rather than completing it.
func TestMailConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "smtp",
		To:        compiler.RestExpr{Literal: "a@b.c"},
		Subject:   compiler.RestExpr{Literal: "x"},
		Body:      compiler.RestExpr{Literal: "y"},
		Retries:   3,
	})
	reg := mail.NewRegistry()
	reg.Register("smtp", erroringClient{})
	if err := drive(t, cp, jobType, reg, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestMailConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestMailConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "smtp",
		To:        compiler.RestExpr{Literal: "a@b.c"},
		Subject:   compiler.RestExpr{Literal: "x"},
		Body:      compiler.RestExpr{Literal: "y"},
		Retries:   3,
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, mail.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, mail.NewRegistry()))
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestMailHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestMailHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := mail.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, mail.NewRegistry())
	if err := h(job.Job{ElementInstanceKey: 424242}); err != nil {
		t.Fatalf("handler for a vanished element instance: err=%v, want nil", err)
	}
}
