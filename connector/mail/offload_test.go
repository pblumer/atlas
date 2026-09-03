package mail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// resolveFixture deploys a one-task process, starts an instance, and hands back what
// Resolve needs: the compiled process, the task's detail, and the live scope.
func resolveFixture(t *testing.T, cfg compiler.MailConfig, vars ...model.VariableValue) (*mail.Registry, mail.Job) {
	t.Helper()
	log, store := openStore(t)
	cp, jobType := mailProcess(t, cfg)
	reg := mail.NewRegistry()

	var resolved mail.Job
	var resolveErr error
	captured := false
	// Drive the process with a handler that resolves rather than sends, so the
	// resolution runs against a real instance's variables and element instance.
	driveResolving(t, cp, jobType, store, log, func(ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64, detail *compiler.ConnectorTaskDetail) {
		resolved, resolveErr = mail.Resolve(store, cp, detail, ei, elementInstanceKey, jobKey)
		captured = true
	}, vars...)
	if !captured {
		t.Fatal("the mail task never produced a job to resolve")
	}
	if resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	return reg, resolved
}

// TestResolveCarriesTheValuesButNeverTheCredential is the security claim ADR-0168
// rests on, made checkable. The engine resolves what to say and who to say it to;
// the worker travels only as a *name*, and the worker looks up its own
// endpoint and password under that name. Nothing in the resolved job can be a
// secret, because the engine never puts one there.
func TestResolveCarriesTheValuesButNeverTheCredential(t *testing.T) {
	_, j := resolveFixture(t, compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: "ops@example.com, team@example.com"},
		Cc:        compiler.RestExpr{Literal: "audit@example.com"},
		Subject:   compiler.RestExpr{Literal: "Order shipped"},
		Body:      compiler.RestExpr{Literal: "On its way."},
		Retries:   3,
	})

	if j.Connector != "office365" {
		t.Errorf("worker = %q, want the name the worker will look up", j.Connector)
	}
	if strings.Join(j.To, ",") != "ops@example.com,team@example.com" {
		t.Errorf("to = %v, want both recipients split and trimmed", j.To)
	}
	if strings.Join(j.Cc, ",") != "audit@example.com" {
		t.Errorf("cc = %v", j.Cc)
	}
	if j.Subject != "Order shipped" || j.Body != "On its way." {
		t.Errorf("subject/body = %q / %q", j.Subject, j.Body)
	}
	if j.MessageID == "" {
		t.Error("no message id: the job key is what makes a resend identifiable")
	}
}

// FEEL is resolved by the engine, because only the engine has the compiled
// expression and the scope to evaluate it against. What reaches the worker is the
// text, not the expression.
func TestResolveEvaluatesFeelBeforeItTravels(t *testing.T) {
	_, j := resolveFixture(t,
		compiler.MailConfig{
			Connector: "office365",
			To:        compiler.RestExpr{Expr: mustExpr(t, "recipient")},
			Subject:   compiler.RestExpr{Expr: mustExpr(t, "subject")},
			Retries:   3,
		},
		model.VariableValue{Name: "recipient", Kind: model.VarString, Text: "ada@example.com"},
		model.VariableValue{Name: "subject", Kind: model.VarString, Text: "Ready"},
	)

	if strings.Join(j.To, ",") != "ada@example.com" {
		t.Errorf("to = %v, want the evaluated recipient", j.To)
	}
	if j.Subject != "Ready" {
		t.Errorf("subject = %q, want the evaluated text", j.Subject)
	}
}

// Run is the worker's half: it looks its own worker up by name and sends. This
// is the same Client the in-process path uses, so the two cannot drift about what a
// Message is.
func TestRunSendsThroughTheWorkersOwnConnector(t *testing.T) {
	reg, j := resolveFixture(t, compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: "ops@example.com"},
		Subject:   compiler.RestExpr{Literal: "Order shipped"},
		Body:      compiler.RestExpr{Literal: "On its way."},
		Retries:   3,
	})
	rc := &recordingClient{}
	reg.Register("office365", rc)

	if err := mail.Run(context.Background(), j, reg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rc.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(rc.sent))
	}
	got := rc.sent[0]
	if strings.Join(got.To, ",") != "ops@example.com" || got.Subject != "Order shipped" {
		t.Errorf("message = %+v, want the resolved fields", got)
	}
}

// A name the worker holds no configuration for is the failure this whole split
// creates: the engine used to refuse it, and now only the worker can know. The
// message has to name the worker, because "configure it somewhere" is the fix
// and the operator needs to know where.
func TestRunRefusesAConnectorTheWorkerDoesNotHold(t *testing.T) {
	reg, j := resolveFixture(t, compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: "ops@example.com"},
		Retries:   3,
	})
	// Nothing registered: this worker was started without that worker.
	err := mail.Run(context.Background(), j, reg)
	if err == nil {
		t.Fatal("sending through an unconfigured worker succeeded")
	}
	if !strings.Contains(err.Error(), "office365") {
		t.Errorf("error = %v, want it to name the worker that is missing", err)
	}
}

// A task that resolved to nobody is refused rather than sent into the void — and it
// is refused *after* the worker lookup, so an operator who has both problems is
// told about the configuration first, which is the one they can act on.
func TestRunRefusesAMessageWithNoRecipient(t *testing.T) {
	reg, j := resolveFixture(t, compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: "  ,  "},
		Retries:   3,
	})
	rc := &recordingClient{}
	reg.Register("office365", rc)

	err := mail.Run(context.Background(), j, reg)
	if err == nil {
		t.Fatal("a message with no recipient was accepted")
	}
	if !strings.Contains(err.Error(), "recipient") {
		t.Errorf("error = %v, want it to say there is no recipient", err)
	}
	if len(rc.sent) != 0 {
		t.Errorf("%d messages sent, want none", len(rc.sent))
	}
}

func mustExpr(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	e, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("CompileAuto(%q): %v", src, err)
	}
	return e
}

// driveResolving runs the process with a handler that hands the task's scope, job
// key and detail to fn instead of sending. It is how a test reaches the exact inputs
// the engine's own lease-time resolution has, without reimplementing the lookup.
func driveResolving(t *testing.T, cp *compiler.CompiledProcess, jobType int32, store *state.Store, log *wal.Log,
	fn func(ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64, detail *compiler.ConnectorTaskDetail), vars ...model.VariableValue) {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(state.Reader) job.Handler {
		return func(j job.Job) error {
			ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
			if err != nil || !ok {
				t.Fatalf("element instance %d: err=%v ok=%v", j.ElementInstanceKey, err, ok)
			}
			detail, err := cp.ConnectorTaskOf(ei.ElementId)
			if err != nil {
				t.Fatalf("ConnectorTaskOf: %v", err)
			}
			fn(ei, j.ElementInstanceKey, j.Key, detail)
			return nil
		}
	})
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
}
