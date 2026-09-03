package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/worker"
)

// The provider-shaped mail environment, which is what an Atlas supervising this
// worker writes for it (ADR-0168).
//
// The older environment names an SMTP host directly, and every test beside this file
// still uses it, because that is what an operator's hand-written worker environment
// says and it has to keep working. This one names a *provider* and describes it, so a
// worker builds whatever client the engine would have built — and so a mail worker
// an operator configured in the Console can be served out of process without them
// re-typing any of it into a worker's environment.

// TestAWorkerBuildsWhicheverProviderItWasHanded is the property that lets the engine
// hand its own worker store over: not "the worker can do SMTP", but "the worker
// builds the same client from the same four values", for every provider a managed
// mail worker can have.
func TestAWorkerBuildsWhicheverProviderItWasHanded(t *testing.T) {
	// A Gmail worker's credential is an OAuth bundle rather than a password, so
	// this also shows the secret arriving as whatever that provider's secret *is*.
	bundle := `{"method":"refreshToken","clientId":"c","clientSecret":"s","refreshToken":"r"}`
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"smtp", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "smtp",
			"ATLAS_MAIL_POST_ENDPOINT": "mx.example.ch:587",
			"ATLAS_MAIL_POST_SENDER":   "bot@example.ch",
			"ATLAS_MAIL_POST_SECRET":   "hunter2",
		}},
		{"gmail", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "gmail",
			"ATLAS_MAIL_POST_SENDER":   "bot@example.ch",
			"ATLAS_MAIL_POST_SECRET":   bundle,
		}},
		{"microsoft", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "microsoft",
			"ATLAS_MAIL_POST_SENDER":   "bot@example.ch",
			"ATLAS_MAIL_POST_SECRET":   `{"method":"clientCredentials","clientId":"c","clientSecret":"s","tenantId":"t"}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, err := worker.BuiltinConnectors(fakeEnv(tc.env), "mail")
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			if _, ok := built.Handlers[compiler.MailJobType]; !ok {
				t.Fatalf("no handler for %s", compiler.MailJobType)
			}
			if len(built.Names) != 1 || built.Names[0] != "post" {
				t.Errorf("workers held = %v, want [post]", built.Names)
			}
		})
	}
}

// A provider the worker cannot build is a configuration mistake, and it is refused
// at startup — where the operator is still watching — rather than discovered one
// parked job at a time.
func TestAnUnbuildableProviderIsRefusedAtStartup(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		env        map[string]string
	}{
		{"unknown provider", "unknown provider", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "carrier-pigeon",
		}},
		{"smtp without a host", "post", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "smtp",
		}},
		{"gmail without a credential", "credential", map[string]string{
			"ATLAS_MAIL_CONNECTORS":    "post",
			"ATLAS_MAIL_POST_PROVIDER": "gmail",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := worker.BuiltinConnectors(fakeEnv(tc.env), "mail")
			if err == nil {
				t.Fatal("the worker started with a worker it cannot build")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The older SMTP-only environment keeps working untouched. An operator who wrote it
// by hand did not opt into anything when the engine learned to write a richer one.
func TestTheSMTPOnlyEnvironmentStillConfiguresAWorker(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":    "post",
		"ATLAS_MAIL_POST_ENDPOINT": "mx.example.ch:587",
		"ATLAS_MAIL_POST_USERNAME": "atlas",
		"ATLAS_MAIL_POST_PASSWORD": "hunter2",
		"ATLAS_MAIL_POST_FROM":     "bot@example.ch",
	}), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(built.Names) != 1 || built.Names[0] != "post" {
		t.Errorf("workers held = %v, want [post]", built.Names)
	}
}

// A preview worker is the one provider whose output only the engine can show, so
// a worker running one has to hand the framed message back. This is that round trip:
// the worker frames it exactly as an in-process preview would and delivers it to the
// server's outbox, so the operator reads it where ADR-0150 promised it would be.
func TestAPreviewConnectorOnAWorkerDeliversToTheServersOutbox(t *testing.T) {
	var (
		mu   sync.Mutex
		got  []mail.OutboxMessage
		auth string
	)
	outbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m mail.OutboxMessage
		if err := json.Unmarshal(body, &m); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		got, auth = append(got, m), r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer outbox.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":        "Vorschau",
		"ATLAS_MAIL_VORSCHAU_PROVIDER": "preview",
		"ATLAS_MAIL_VORSCHAU_SENDER":   "bot@example.ch",
		"ATLAS_MAIL_OUTBOX_URL":        outbox.URL,
		"ATLAS_TOKEN":                  "s3cret",
	}), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}

	if _, err := built.Handlers[compiler.MailJobType].Run(context.Background(), mailJob(t, mail.Job{
		Connector: "Vorschau", To: []string{"a@example.ch"}, Subject: "Probe", Body: "hallo",
	})); err != nil {
		t.Fatalf("preview send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("the outbox received %d messages, want one", len(got))
	}
	m := got[0]
	if m.Connector != "Vorschau" || m.Subject != "Probe" || m.From != "bot@example.ch" {
		t.Errorf("delivered message = %+v", m)
	}
	// The framing is what a preview run is *for*: headers and MIME structure are the
	// parts an author cannot check by re-reading their model.
	if !strings.Contains(m.Raw, "Subject: Probe") || !strings.Contains(m.Raw, "To: a@example.ch") {
		t.Errorf("framed message = %q, want the headers a real provider would send", m.Raw)
	}
	if auth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the worker's own token", auth)
	}
}

// An outbox that cannot be reached fails the job. A preview whose message went
// nowhere must not report success: the operator's next act is to go and look for it.
func TestAPreviewWhoseOutboxIsUnreachableFailsTheJob(t *testing.T) {
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer refuse.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":        "Vorschau",
		"ATLAS_MAIL_VORSCHAU_PROVIDER": "preview",
		"ATLAS_MAIL_VORSCHAU_SENDER":   "bot@example.ch",
		"ATLAS_MAIL_OUTBOX_URL":        refuse.URL,
	}), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}

	_, err = built.Handlers[compiler.MailJobType].Run(context.Background(), mailJob(t, mail.Job{
		Connector: "Vorschau", To: []string{"a@example.ch"}, Subject: "Probe",
	}))
	if err == nil {
		t.Fatal("a preview that never reached the outbox reported success")
	}
	if !strings.Contains(err.Error(), "outbox") {
		t.Errorf("error = %q, want it to say where the message did not arrive", err)
	}
}

// An outbox that is not there at all — the engine restarting under the worker — is
// the same failure and reads the same way.
func TestAPreviewWhoseOutboxIsGoneFailsTheJob(t *testing.T) {
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := gone.URL
	gone.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":        "Vorschau",
		"ATLAS_MAIL_VORSCHAU_PROVIDER": "preview",
		"ATLAS_MAIL_VORSCHAU_SENDER":   "bot@example.ch",
		"ATLAS_MAIL_OUTBOX_URL":        url,
	}), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}

	_, err = built.Handlers[compiler.MailJobType].Run(context.Background(), mailJob(t, mail.Job{
		Connector: "Vorschau", To: []string{"a@example.ch"}, Subject: "Probe",
	}))
	if err == nil {
		t.Fatal("a preview that reached no outbox at all reported success")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error = %q, want it to name the address that did not answer", err)
	}
}

// A preview worker with nowhere to deliver is refused at startup, naming itself,
// rather than leasing preview jobs it can only fail.
func TestAPreviewConnectorWithNoOutboxAddressIsRefusedAtStartup(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":        "Vorschau",
		"ATLAS_MAIL_VORSCHAU_PROVIDER": "preview",
	}), "mail")
	if err == nil {
		t.Fatal("a preview worker with no outbox started anyway")
	}
	if !strings.Contains(err.Error(), "Vorschau") {
		t.Errorf("error = %q, want it to name the worker", err)
	}
}

// mailJob wraps a resolved mail job in the payload a leased job carries.
func mailJob(t *testing.T, j mail.Job) worker.Job {
	t.Helper()
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal job: %v", err)
	}
	return worker.Job{
		Type:      compiler.MailJobType,
		Connector: &worker.ConnectorPayload{Kind: "mail", Fields: payload},
	}
}
