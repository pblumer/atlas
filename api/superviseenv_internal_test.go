package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/logging"
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

// TestAScriptWorkerIsNeverGivenTheMailCredential is why the default supervises one
// worker per kind rather than one for all of them, and it is a security property
// rather than a tidiness one.
//
// A script task runs an interpreter that inherits its worker's whole environment
// (connector/script.CmdExec appends to os.Environ), so a model-authored script on a
// worker that also holds the mail credential could simply read the SMTP password out
// of it. Separate processes are what stop that: the secret is rendered only into the
// environment of the worker that sends mail.
func TestAScriptWorkerIsNeverGivenTheMailCredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "post", Kind: connectorKindMail, Provider: "smtp",
		Endpoint: "mx:587", Sender: "bot@x", CredentialsRef: "smtp-pw", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_SMTP_PW_TOKEN", "hunter2")

	scripts := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "script", Connectors: []string{"script"}})())
	for name, value := range scripts {
		if strings.HasPrefix(name, mailEnvPrefix) {
			t.Errorf("a script worker was handed %s=%q", name, value)
		}
	}

	// And the worker that does send mail holds it, so the boundary is a separation
	// rather than the credential having gone missing.
	mailer := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "mail", Connectors: []string{connectorKindMail}})())
	if got := mailer["ATLAS_MAIL_POST_SECRET"]; got != "hunter2" {
		t.Errorf("the mail worker's secret = %q, want it to hold the credential", got)
	}
}

// A supervised worker on a server that requires authentication is given a token, or
// it is refused at every poll and Atlas has started a worker that can do nothing.
func TestASupervisedWorkerOnAnAuthenticatedServerIsGivenAToken(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	if srv.InternalToken() == "" {
		t.Fatal("an authenticated server has no internal token to hand over")
	}

	env := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "csv", Connectors: []string{"csv"}})())
	if got := env["ATLAS_TOKEN"]; got != srv.InternalToken() {
		t.Errorf("ATLAS_TOKEN = %q, want this server's own token", got)
	}
}

// An operator who chose an identity for their workers keeps it: replacing their
// ATLAS_TOKEN would silently undo that choice.
func TestAnOperatorsOwnWorkerTokenIsNotReplaced(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	t.Setenv("ATLAS_TOKEN", "the-operators-own")

	env := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "csv", Connectors: []string{"csv"}})())
	if _, overridden := env["ATLAS_TOKEN"]; overridden {
		t.Error("the operator's own ATLAS_TOKEN was overridden")
	}
}

// A server that requires no authentication hands over no token: there is nothing to
// authenticate with, and a variable that looks like a credential but is not one is
// worse than none.
func TestAnUnauthenticatedServerHandsOverNoToken(t *testing.T) {
	srv, _ := newValidateServer(t)

	env := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "csv", Connectors: []string{"csv"}})())
	if _, handed := env["ATLAS_TOKEN"]; handed {
		t.Error("an unauthenticated server handed a worker a token")
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

// The payoff for the Remedy kind (ADR-0106/0168): an operator adds a Helix instance in
// the Console, and the supervised worker files tickets against it having been told
// nothing by hand. Remedy is mail's situation exactly — the base URL and the service
// account live in the connector store and the vault, which a supervised worker can
// read no more than it can read the engine's memory.
func TestASupervisedWorkerIsHandedTheRemedyConnectorsFromTheStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://localhost:8080", nil, nil))
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "Helix ITSM", Kind: connectorKindRemedy,
		Endpoint: "https://helix.example.com:8008", CredentialsRef: "remedy-creds",
		Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_REMEDY_CREDS_TOKEN", `{"username":"atlas-svc","password":"hunter2"}`)

	env := envOf(t, srv.remedyWorkerEnv())

	if got := env["ATLAS_REMEDY_CONNECTORS"]; got != "Helix ITSM" {
		t.Errorf("ATLAS_REMEDY_CONNECTORS = %q, want the connector's own name", got)
	}
	// The three values remedy.Connector is built from, under the name the worker folds
	// "Helix ITSM" into.
	for name, want := range map[string]string{
		"ATLAS_REMEDY_HELIX_ITSM_ENDPOINT": "https://helix.example.com:8008",
		"ATLAS_REMEDY_HELIX_ITSM_USERNAME": "atlas-svc",
		"ATLAS_REMEDY_HELIX_ITSM_PASSWORD": "hunter2",
	} {
		if got := env[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// The engine and the worker must agree on every variable name, or the handover is a
// set of variables nobody reads. Building a real worker from what the engine rendered
// is the only check that cannot drift.
func TestSupervisedRemedyEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "helix", Kind: connectorKindRemedy,
		Endpoint: "https://helix.example.com:8008", CredentialsRef: "remedy-creds",
		Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_REMEDY_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)

	env := envOf(t, srv.remedyWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindRemedy)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "helix") {
		t.Errorf("the worker holds %v, want the connector the engine handed it", built.Names)
	}
}

// A connector whose credential bundle is missing, malformed, or half-filled is left
// out entirely rather than handed over incomplete. The worker refuses a *named*
// instance missing a field at startup — which would take down every other kind it
// serves — so an unusable connector must not be named at all.
func TestAnUnusableRemedyConnectorIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "nosecret", Kind: connectorKindRemedy, Endpoint: "https://a:8008", CredentialsRef: "absent", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "broken", Kind: connectorKindRemedy, Endpoint: "https://b:8008", CredentialsRef: "broken-creds", Enabled: true, CreatedAt: 2},
		{ID: "3", Name: "halffilled", Kind: connectorKindRemedy, Endpoint: "https://c:8008", CredentialsRef: "half-creds", Enabled: true, CreatedAt: 3},
		{ID: "4", Name: "noendpoint", Kind: connectorKindRemedy, CredentialsRef: "good-creds", Enabled: true, CreatedAt: 4},
		{ID: "5", Name: "off", Kind: connectorKindRemedy, Endpoint: "https://d:8008", CredentialsRef: "good-creds", Enabled: false, CreatedAt: 5},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}
	t.Setenv("ATLAS_CONNECTOR_BROKEN_CREDS_TOKEN", `not json`)
	t.Setenv("ATLAS_CONNECTOR_HALF_CREDS_TOKEN", `{"username":"svc"}`)
	t.Setenv("ATLAS_CONNECTOR_GOOD_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)

	env := envOf(t, srv.remedyWorkerEnv())
	if len(env) != 0 {
		t.Errorf("environment = %v, want nothing handed over: not one of these connectors is usable", env)
	}
}

// A disabled connector is one an operator switched off. Handing it over would let a
// worker keep filing tickets through it, which is the one thing switching it off means.
func TestOnlyUsableRemedyConnectorsAreHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "live", Kind: connectorKindRemedy, Endpoint: "https://a:8008", CredentialsRef: "good-creds", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "off", Kind: connectorKindRemedy, Endpoint: "https://b:8008", CredentialsRef: "good-creds", Enabled: false, CreatedAt: 2},
		{ID: "3", Name: "elsewhere", Kind: connectorKindClio, Endpoint: "http://clio", Enabled: true, CreatedAt: 3},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}
	t.Setenv("ATLAS_CONNECTOR_GOOD_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)

	env := envOf(t, srv.remedyWorkerEnv())
	if got := env["ATLAS_REMEDY_CONNECTORS"]; got != "live" {
		t.Errorf("ATLAS_REMEDY_CONNECTORS = %q, want only the enabled remedy connector", got)
	}
	for _, name := range []string{"ATLAS_REMEDY_OFF_ENDPOINT", "ATLAS_REMEDY_ELSEWHERE_ENDPOINT"} {
		if _, handed := env[name]; handed {
			t.Errorf("%s was handed to the worker", name)
		}
	}
}

// An instance an operator configured on the host keeps working when a Console one is
// added: the child inherits its variables, and the rendered list is the union, so a
// store connector does not silently take the whole list away from it.
func TestAHostConfiguredRemedyInstanceSurvivesAStoreOne(t *testing.T) {
	srv, _ := newValidateServer(t)
	t.Setenv("ATLAS_REMEDY_CONNECTORS", "legacy")
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "console", Kind: connectorKindRemedy, Endpoint: "https://a:8008",
		CredentialsRef: "good-creds", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_GOOD_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)

	got := envOf(t, srv.remedyWorkerEnv())["ATLAS_REMEDY_CONNECTORS"]
	for _, want := range []string{"legacy", "console"} {
		if !strings.Contains(got, want) {
			t.Errorf("ATLAS_REMEDY_CONNECTORS = %q, want it to keep %q", got, want)
		}
	}
}

// A connector name with no letter or digit in it folds to no variable name at all.
// Rendered anyway it would become ATLAS_MAIL__ENDPOINT — a variable no operator could
// ever set, and one the next such name would collide with.
func TestAMailConnectorNameThatFoldsToNothingIsLeftOut(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "---", Kind: connectorKindMail, Provider: "smtp",
		Endpoint: "mx:587", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if env := srv.mailWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a name with no variable name in it", env)
	}
}

// Two Remedy names that fold to one variable would give one instance the other's
// service account — the mail collision, on the kind whose credential opens a ticket
// system. The second is left out; the Workers view then shows it served nowhere.
func TestTwoRemedyConnectorsThatFoldToOneVariableDoNotShareACredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "helix itsm", Kind: connectorKindRemedy, Endpoint: "https://a:8008", CredentialsRef: "creds-a", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "helix-itsm", Kind: connectorKindRemedy, Endpoint: "https://b:8008", CredentialsRef: "creds-b", Enabled: true, CreatedAt: 2},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}
	t.Setenv("ATLAS_CONNECTOR_CREDS_A_TOKEN", `{"username":"svc-a","password":"pw-a"}`)
	t.Setenv("ATLAS_CONNECTOR_CREDS_B_TOKEN", `{"username":"svc-b","password":"pw-b"}`)

	env := envOf(t, srv.remedyWorkerEnv())
	names := strings.Split(env["ATLAS_REMEDY_CONNECTORS"], ",")
	if len(names) != 1 {
		t.Fatalf("ATLAS_REMEDY_CONNECTORS = %q, want exactly one of the two colliding names", env["ATLAS_REMEDY_CONNECTORS"])
	}
	// Whichever one won, the account handed over is its own and not the other's.
	want := map[string]string{"helix itsm": "svc-a", "helix-itsm": "svc-b"}[names[0]]
	if got := env["ATLAS_REMEDY_HELIX_ITSM_USERNAME"]; got != want {
		t.Errorf("username = %q, want %q — the surviving connector's own", got, want)
	}
}

// The same fold-to-nothing rule on the Remedy renderer.
func TestARemedyConnectorNameThatFoldsToNothingIsLeftOut(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "...", Kind: connectorKindRemedy, Endpoint: "https://a:8008",
		CredentialsRef: "good-creds", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_GOOD_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)

	if env := srv.remedyWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a name with no variable name in it", env)
	}
}

// The three SQL products are provisioned through one shared renderer parameterised by
// product. A copy-paste in that map would hand one product's connection strings to
// another product's worker, which would then try to connect with a DSN it cannot even
// parse — so each entry is checked to render its own records and only its own.
func TestEachSQLProductIsProvisionedFromItsOwnRecords(t *testing.T) {
	srv, _ := newValidateServer(t)
	kinds := []string{connectorKindPostgres, connectorKindMariaDB, connectorKindMSSQL}
	for i, kind := range kinds {
		ref := "sql/" + kind + "/dsn"
		if _, err := srv.vault.Set(ref, kind+"://u:p@db.example/hr"); err != nil {
			t.Fatalf("vault.Set(%s): %v", ref, err)
		}
		if err := srv.connectors.Save(connector{
			ID: strconv.Itoa(i + 1), Name: "hr-db", Kind: kind,
			CredentialsRef: ref, Enabled: true, CreatedAt: int64(i + 1),
		}); err != nil {
			t.Fatalf("connectors.Save(%s): %v", kind, err)
		}
	}

	provisioned := srv.provisionedConnectorKinds()
	for _, kind := range kinds {
		render, ok := provisioned[kind]
		if !ok {
			t.Fatalf("%s is not in provisionedConnectorKinds; a supervised worker would hold no database", kind)
		}
		env := envOf(t, render())
		prefix := "ATLAS_" + strings.ToUpper(kind) + "_"
		if got, want := env[prefix+"HR_DB_DSN"], kind+"://u:p@db.example/hr"; got != want {
			t.Errorf("%sHR_DB_DSN = %q, want %q", prefix, got, want)
		}
		// And nothing from a sibling product: that is the copy-paste this test is for.
		for _, other := range kinds {
			if other == kind {
				continue
			}
			if _, handed := env["ATLAS_"+strings.ToUpper(other)+"_HR_DB_DSN"]; handed {
				t.Errorf("the %s renderer handed over a %s connection string", kind, other)
			}
		}
	}
}

// ATLAS_TOKEN set to a value this server does not accept is the quietest way to break
// every supervised worker at once: the children are handed it instead of the server's
// own credential, and are then refused at every poll. It is worth saying at startup,
// because the alternative is reading it out of a worker's poll errors.
func TestAnUnknownATLASTOKENIsSaidOutLoudAtStartup(t *testing.T) {
	const good = apiTokenPrefix + "known-to-this-server"

	for _, tc := range []struct {
		name  string
		auth  bool
		token string
		warn  bool
	}{
		{name: "auth off, so nothing is handed to anybody", token: "whatever"},
		{name: "auth on and no ATLAS_TOKEN set", auth: true},
		{name: "auth on and a token this server accepts", auth: true, token: good},
		{name: "auth on and a token this server does not accept", auth: true, token: apiTokenPrefix + "stale", warn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := captureWarnings(t)
			srv, _ := newValidateServer(t)
			srv.authEnabled = tc.auth
			srv.apiTokens.add(apiToken{ID: "1", Name: "worker", Hash: hashAPIToken(good), Scope: apiScopeWorker})
			t.Setenv("ATLAS_TOKEN", tc.token)

			srv.checkWorkerTokenEnv()

			warned := strings.Contains(sink.String(), logging.AuthWorkerTokenUnknown.String())
			if warned != tc.warn {
				t.Errorf("warned = %v, want %v; log was %q", warned, tc.warn, sink.String())
			}
		})
	}
}

// captureWarnings points the process logger at a buffer, so a test can assert on a
// warning that has no other observable effect, and restores stderr afterwards.
func captureWarnings(t *testing.T) *lockedBuffer {
	t.Helper()
	sink := &lockedBuffer{}
	if err := logging.Setup(sink, logging.FormatJSON); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat) })
	return sink
}

// lockedBuffer is a log sink safe to write from a server's run loop and read from the
// test goroutine — the process logger is global, so anything else running logs into it
// too.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A connector store that cannot be read hands over nothing and says so, rather than
// handing a worker a partial set of connectors or taking the server down.
//
// Every renderer is checked, because they each read the store separately and a new one
// is easy to write without the guard: a worker spawned with half its connectors is
// worse than one spawned with none, since the missing half looks configured in the
// Console and simply never serves.
func TestAnUnreadableConnectorStoreRendersNothingForEveryKind(t *testing.T) {
	srv, dir := newValidateServer(t)
	for ref, bundle := range map[string]string{
		"ad-good":     `{"bindDN":"cn=svc,dc=x","password":"pw"}`,
		"remedy-good": `{"username":"svc","password":"pw"}`,
		"entra-good":  `{"tenantId":"t","clientId":"c","clientSecret":"s"}`,
	} {
		if _, err := srv.vault.Set(ref, bundle); err != nil {
			t.Fatalf("vault.Set(%s): %v", ref, err)
		}
	}
	for _, c := range []connector{
		{ID: "1", Name: "forest", Kind: connectorKindAD, Endpoint: "ldaps://dc:636", CredentialsRef: "ad-good", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "post", Kind: connectorKindMail, Provider: "smtp", Endpoint: "mx:587", Enabled: true, CreatedAt: 2},
		{ID: "3", Name: "helix", Kind: connectorKindRemedy, Endpoint: "https://helix:8008", CredentialsRef: "remedy-good", Enabled: true, CreatedAt: 3},
		{ID: "4", Name: "tenant", Kind: connectorKindEntra, CredentialsRef: "entra-good", Enabled: true, CreatedAt: 4},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("connectors.Save(%s): %v", c.Name, err)
		}
	}

	renderers := map[string]struct {
		render func() []string
		lists  string
	}{
		"mail":   {srv.mailWorkerEnv, "ATLAS_MAIL_CONNECTORS"},
		"remedy": {srv.remedyWorkerEnv, "ATLAS_REMEDY_CONNECTORS"},
		"entra":  {srv.entraWorkerEnv, "ATLAS_ENTRA_CONNECTORS"},
		"ad":     {srv.adWorkerEnv, "ATLAS_AD_CONNECTORS"},
	}
	// Each renders its own connector while the store is readable, so "nothing" below is
	// a decision rather than an empty store.
	for name, r := range renderers {
		if envOf(t, r.render())[r.lists] == "" {
			t.Fatalf("%s rendered no connector before the store broke", name)
		}
	}

	// Now break it, in the way a store actually breaks: the directory is gone, replaced
	// by something that is not one. A malformed record file would not do it — the store
	// skips a name that is not an id, so LoadAll would happily succeed.
	conns := filepath.Join(dir, "connectors")
	if err := os.RemoveAll(conns); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.WriteFile(conns, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	sink := captureWarnings(t)
	for name, r := range renderers {
		t.Run(name, func(t *testing.T) {
			if got := envOf(t, r.render())[r.lists]; got != "" {
				t.Errorf("%s = %q, rendered from a store that cannot be read", r.lists, got)
			}
		})
	}
	// And it is said out loud: an operator whose workers quietly lost their connectors
	// has nothing else to go on.
	if !strings.Contains(sink.String(), logging.WorkerSupervisorFailed.String()) {
		t.Errorf("nothing was logged; the log was %q", sink.String())
	}
}
