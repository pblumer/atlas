package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/worker"
)

// What a supervised worker is handed at spawn, and what it must never be handed.
//
// The rule ADR-0168 protects is about the *job payload*: a credential never travels
// with a leased job. Provisioning a child's environment does not touch that rule —
// but it is the place where it would be easiest to break by accident, so these tests
// pin both halves: the child gets everything it needs to build the same mail client
// the engine would, and a mail Job still has nowhere to put any of it.

// envOf turns the rendered environment into a map, so a test asserts on a variable
// rather than on the order they happened to be appended in.
func envOf(t *testing.T, lines []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Fatalf("environment entry %q is not NAME=value", l)
		}
		out[k] = v
	}
	return out
}

// The payoff: an operator configures a mail connector in the Console and the
// supervised worker can send through it, having been told nothing by hand.
func TestASupervisedWorkerIsHandedTheMailConnectorsFromTheStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://localhost:8080", nil, nil))
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "Haus-Post", Kind: connectorKindMail, Provider: "smtp",
		Endpoint: "mx.example.ch:587", Sender: "bot@example.ch", CredentialsRef: "smtp-pw",
		Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_SMTP_PW_TOKEN", "hunter2")

	env := envOf(t, srv.mailWorkerEnv())

	if got := env["ATLAS_MAIL_CONNECTORS"]; got != "Haus-Post" {
		t.Errorf("ATLAS_MAIL_CONNECTORS = %q, want the connector's own name", got)
	}
	// The four values mail.ProviderConfig is built from, under the name the worker
	// folds "Haus-Post" into.
	for name, want := range map[string]string{
		"ATLAS_MAIL_HAUS_POST_PROVIDER": "smtp",
		"ATLAS_MAIL_HAUS_POST_ENDPOINT": "mx.example.ch:587",
		"ATLAS_MAIL_HAUS_POST_SENDER":   "bot@example.ch",
		"ATLAS_MAIL_HAUS_POST_SECRET":   "hunter2",
	} {
		if got := env[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// And the address a preview connector delivers back to.
	if got := env["ATLAS_MAIL_OUTBOX_URL"]; got != "http://localhost:8080/api/v1/mail/outbox" {
		t.Errorf("ATLAS_MAIL_OUTBOX_URL = %q", got)
	}
}

// A record written before the provider field existed is an SMTP connector, and is
// handed over as one — the same default buildMailClients applies, so the worker's
// client and the engine's are built from the same values.
func TestAConnectorWithNoProviderIsHandedOverAsSMTP(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "old", Kind: connectorKindMail, Endpoint: "mx:25", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := envOf(t, srv.mailWorkerEnv())["ATLAS_MAIL_OLD_PROVIDER"]; got != mail.ProviderSMTP {
		t.Errorf("provider = %q, want %q", got, mail.ProviderSMTP)
	}
}

// A disabled connector is one an operator switched off. Handing it over would let a
// worker keep sending through it, which is the one thing switching it off means.
func TestADisabledConnectorIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "live", Kind: connectorKindMail, Provider: "smtp", Endpoint: "mx:587", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "off", Kind: connectorKindMail, Provider: "smtp", Endpoint: "mx:587", Enabled: false, CreatedAt: 2},
		{ID: "3", Name: "elsewhere", Kind: connectorKindClio, Endpoint: "http://clio", Enabled: true, CreatedAt: 3},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}

	env := envOf(t, srv.mailWorkerEnv())
	if got := env["ATLAS_MAIL_CONNECTORS"]; got != "live" {
		t.Errorf("ATLAS_MAIL_CONNECTORS = %q, want only the enabled mail connector", got)
	}
	for _, name := range []string{"ATLAS_MAIL_OFF_ENDPOINT", "ATLAS_MAIL_ELSEWHERE_ENDPOINT"} {
		if _, handed := env[name]; handed {
			t.Errorf("%s was handed to the worker", name)
		}
	}
}

// Two names that fold to one variable would give one connector the other's password.
// The second is left out, and the worker then reports it as a name it does not hold —
// which the Workers view already shows as a connector served nowhere.
func TestTwoConnectorsThatFoldToOneVariableDoNotShareACredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "haus post", Kind: connectorKindMail, Provider: "smtp", Endpoint: "a:587", CredentialsRef: "a", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "haus-post", Kind: connectorKindMail, Provider: "smtp", Endpoint: "b:587", CredentialsRef: "b", Enabled: true, CreatedAt: 2},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}

	env := envOf(t, srv.mailWorkerEnv())
	names := strings.Split(env["ATLAS_MAIL_CONNECTORS"], ",")
	if len(names) != 1 {
		t.Fatalf("ATLAS_MAIL_CONNECTORS = %q, want exactly one of the two colliding names", env["ATLAS_MAIL_CONNECTORS"])
	}
	// Whichever one won, the endpoint handed over is its own and not the other's.
	want := map[string]string{"haus post": "a:587", "haus-post": "b:587"}[names[0]]
	if got := env["ATLAS_MAIL_HAUS_POST_ENDPOINT"]; got != want {
		t.Errorf("endpoint = %q, want %q — the surviving connector's own", got, want)
	}
}

// A server with no mail connector hands over nothing at all, rather than an empty
// ATLAS_MAIL_CONNECTORS the worker would refuse to start on.
func TestAServerWithNoMailConnectorsHandsOverNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	if env := srv.mailWorkerEnv(); env != nil {
		t.Errorf("environment = %v, want nothing", env)
	}
}

// Only a worker that serves mail is given mail's configuration. A --supervise worker
// running someone's Python handler has no business holding an SMTP password.
func TestOnlyAWorkerServingMailIsGivenMailsConfiguration(t *testing.T) {
	srv, _ := newValidateServer(t)
	if fn := srv.superviseEnv(SuperviseSpec{ID: "scripts", Connectors: []string{"script", "csv"}}); fn != nil {
		t.Error("a worker serving no mail was given an environment")
	}
	if fn := srv.superviseEnv(SuperviseSpec{ID: "connectors", Connectors: []string{"csv", connectorKindMail}}); fn == nil {
		t.Error("a worker serving mail was given no environment")
	}
}

// The names the engine writes are the names the worker reads. They are declared in
// two packages — the engine cannot import the worker, which is the right dependency
// direction — so nothing but a test holds them together.
func TestSupervisedMailEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "post", Kind: connectorKindMail, Provider: "preview", Sender: "b@x", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	env := envOf(t, srv.mailWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindMail)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "post") {
		t.Errorf("the worker holds %v, want the connector the engine handed it", built.Names)
	}
}

// A preview connector previews into the outbox of the server the operator is
// watching, even though the message was framed in another process — which is the
// whole promise of the preview provider (ADR-0150), and the reason mail could be
// offloaded by default without taking that promise away.
func TestAMessageFramedElsewhereLandsInThisServersOutbox(t *testing.T) {
	srv, _ := newValidateServer(t)

	msg := mail.OutboxMessage{
		Connector: "post", From: "bot@x", To: []string{"a@x"},
		Subject: "Rehearsal", Body: "hello", Raw: "From: bot@x\r\n\r\nhello",
		// A caller does not get to choose where in the list its message appears.
		Seq: 999, At: 1,
	}
	body, _ := json.Marshal(msg)
	if code, out := serveInternal(t, srv, http.MethodPost, "/api/v1/mail/outbox", string(body), "application/json"); code != http.StatusNoContent {
		t.Fatalf("POST /mail/outbox: status=%d body=%s", code, out)
	}

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/mail/outbox", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /mail/outbox: status=%d body=%s", code, raw)
	}
	var got struct {
		Messages []mail.OutboxMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode outbox: %v (%s)", err, raw)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("outbox holds %d messages, want the one delivered", len(got.Messages))
	}
	m := got.Messages[0]
	if m.Connector != "post" || m.Subject != "Rehearsal" || m.Raw != "From: bot@x\r\n\r\nhello" {
		t.Errorf("delivered message = %+v", m)
	}
	if m.Seq == 999 || m.At == 1 {
		t.Errorf("the caller's seq/at survived (%d/%d); the outbox stamps its own", m.Seq, m.At)
	}
}

// A delivery that names no connector is refused: the outbox is grouped by name, and a
// nameless entry is one nobody can trace back to a model.
func TestAnOutboxDeliveryWithoutAConnectorIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)

	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/mail/outbox", `{"to":["a@x"]}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a message with no connector", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/mail/outbox", `not json`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unparseable body", code)
	}
}
