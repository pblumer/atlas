package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/worker"
)

// fakeEnv is the worker's environment for a test: the only place a credential is
// allowed to come from.
func fakeEnv(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// TestMailWorkerHoldsItsOwnCredential is the point of ADR-0168's decision, made
// checkable from the worker's side: the SMTP host and password are read from this
// process's environment, and the leased job contributes only a connector *name*.
// A worker can therefore serve a provider the engine has no configuration for.
func TestMailWorkerHoldsItsOwnCredential(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":         "office365",
		"ATLAS_MAIL_OFFICE365_ENDPOINT": "smtp.example.com:587",
		"ATLAS_MAIL_OFFICE365_USERNAME": "atlas",
		"ATLAS_MAIL_OFFICE365_PASSWORD": "hunter2",
		"ATLAS_MAIL_OFFICE365_FROM":     "noreply@example.com",
	})
	execs, err := worker.BuiltinConnectors(env, "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := execs.Handlers[compiler.MailJobType]; !ok {
		t.Fatalf("no handler for %s; got %v", compiler.MailJobType, execs.Handlers)
	}
}

// A worker told to serve mail but given no mail connector would lease every mail job
// and fail it. That is a configuration mistake, and it is caught at startup where
// the operator is still looking, rather than in an incident an hour later.
func TestMailWorkerWithNoConfiguredConnectorIsRefusedAtStartup(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(nil), "mail")
	if err == nil {
		t.Fatal("a mail worker with no configured connector started anyway")
	}
	if !strings.Contains(err.Error(), "ATLAS_MAIL_CONNECTORS") {
		t.Errorf("error = %v, want it to name the variable that configures this", err)
	}
}

// A named connector missing its endpoint is likewise a startup error: half a
// configuration sends nothing, and finding that out per job would spend a retry
// budget to learn what was knowable before the first poll.
func TestMailWorkerRefusesAHalfConfiguredConnector(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":         "office365",
		"ATLAS_MAIL_OFFICE365_USERNAME": "atlas",
	})
	_, err := worker.BuiltinConnectors(env, "mail")
	if err == nil {
		t.Fatal("a connector with no endpoint was accepted")
	}
	if !strings.Contains(err.Error(), "office365") || !strings.Contains(err.Error(), "ENDPOINT") {
		t.Errorf("error = %v, want it to name the connector and the missing setting", err)
	}
}

// A connector name with characters that cannot appear in an environment variable is
// still configurable: the name is folded the one way, and the error says which
// variable was looked for so an operator can set exactly that.
func TestMailConnectorNamesFoldIntoEnvironmentVariables(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":              "internal-relay",
		"ATLAS_MAIL_INTERNAL_RELAY_ENDPOINT": "relay.internal:25",
	})
	execs, err := worker.BuiltinConnectors(env, "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := execs.Handlers[compiler.MailJobType]; !ok {
		t.Fatal("a hyphenated connector name was not configurable")
	}
}

// A mail job whose connector this worker does not hold fails with a message naming
// it. The engine can no longer catch this — it does not know what any worker holds —
// so the worker's message is the whole of what an operator gets.
func TestMailWorkerRefusesAConnectorItDoesNotHold(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":         "office365",
		"ATLAS_MAIL_OFFICE365_ENDPOINT": "smtp.example.com:587",
	})
	execs, err := worker.BuiltinConnectors(env, "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	_, err = execs.Handlers[compiler.MailJobType].Run(context.Background(), worker.Job{
		JobKey: 1, Type: compiler.MailJobType,
		Connector: &worker.ConnectorPayload{Kind: "mail", Fields: map[string]any{
			"connector": "some-other-provider",
			"to":        []any{"ops@example.com"},
		}},
	})
	if err == nil {
		t.Fatal("a job for an unheld connector succeeded")
	}
	if !strings.Contains(err.Error(), "some-other-provider") {
		t.Errorf("error = %v, want it to name the connector this worker lacks", err)
	}
}

// A mail job with no resolved detail points at the server's configuration, the same
// way the CSV kind does: the worker was asked for a kind the engine is still serving
// itself, so the payload never arrived.
func TestMailWorkerNeedsTheResolvedDetail(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":         "office365",
		"ATLAS_MAIL_OFFICE365_ENDPOINT": "smtp.example.com:587",
	})
	execs, err := worker.BuiltinConnectors(env, "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	_, err = execs.Handlers[compiler.MailJobType].Run(context.Background(), worker.Job{JobKey: 1, Type: compiler.MailJobType})
	if err == nil {
		t.Fatal("a mail job with no connector detail succeeded")
	}
	if !strings.Contains(err.Error(), "offloading") {
		t.Errorf("error = %v, want it to point at the server's offload configuration", err)
	}
}

// TestWorkerSendsAnOffloadedMailConnector is the slice end to end, and the first
// time a credential is on the far side of the seam. The engine resolved the message
// — recipients, subject and body, evaluated against the instance — and the worker
// sent it through a provider configured only here. The engine has no SMTP
// configuration in this test at all, which is the property that makes an offloaded
// mail connector deployable into a network the engine cannot reach.
func TestWorkerSendsAnOffloadedMailConnector(t *testing.T) {
	sent := make(chan mail.Message, 1)
	reg := mail.NewRegistry()
	reg.Register("office365", clientFunc(func(_ context.Context, m mail.Message) error {
		sent <- m
		return nil
	}))

	ts := liveAtlasWith(t, mailConnectorModel, `{"customer":"ada@example.com"}`,
		api.WithOffloadedConnectorKinds([]string{"mail"}))

	w := worker.New(worker.Options{
		Server: ts.URL, ID: "mailer-1",
		Handlers: map[string]worker.Exec{
			compiler.MailJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
				return worker.RunMailJob(ctx, j, reg)
			}),
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	select {
	case m := <-sent:
		if strings.Join(m.To, ",") != "ada@example.com" {
			t.Errorf("to = %v, want the FEEL-resolved recipient", m.To)
		}
		if m.Subject != "Order shipped" {
			t.Errorf("subject = %q", m.Subject)
		}
		if m.MessageID == "" {
			t.Error("no message id on the sent mail")
		}
	default:
		t.Fatal("the worker never sent the message")
	}
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the mail job was not completed", running)
	}
}

// clientFunc adapts a function to mail.Client.
type clientFunc func(context.Context, mail.Message) error

func (f clientFunc) Send(ctx context.Context, m mail.Message) error { return f(ctx, m) }

const mailConnectorModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="notify" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="office365" to="=customer" subject="Order shipped" body="On its way."/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestAWorkersConnectorNamesReachTheWorkersView closes the loop the API tests only
// half prove: a real worker, configured from its own environment, reports the names
// it holds on its poll, and the engine's Workers view shows them. Without this the
// two halves could agree on a JSON field and still not meet.
func TestAWorkersConnectorNamesReachTheWorkersView(t *testing.T) {
	ts := liveAtlasWith(t, mailConnectorModel, `{"customer":"ada@example.com"}`,
		api.WithOffloadedConnectorKinds([]string{"mail"}))

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":              "internal-relay",
		"ATLAS_MAIL_INTERNAL_RELAY_ENDPOINT": "relay.internal:25",
	}), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "mailer-1", Handlers: built.Handlers, Connectors: built.Names,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The send will fail — this worker holds "internal-relay" and the model asks for
	// "office365" — which is the situation being reported on, not a problem with it.
	_ = w.RunOnce(ctx)

	var view struct {
		Workers []struct {
			Worker     string   `json:"worker"`
			Connectors []string `json:"connectors"`
		} `json:"workers"`
		Unserved []struct {
			Name string `json:"name"`
		} `json:"unservedConnectors"`
	}
	if err := json.Unmarshal(get(t, ts, "/api/v1/workers"), &view); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(view.Workers) != 1 || strings.Join(view.Workers[0].Connectors, ",") != "internal-relay" {
		t.Fatalf("workers = %+v, want mailer-1 holding internal-relay", view.Workers)
	}
	if len(view.Unserved) != 1 || view.Unserved[0].Name != "office365" {
		t.Errorf("unserved = %+v, want office365 named as reachable by nothing", view.Unserved)
	}
}
