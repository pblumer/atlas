package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
)

// TestManagedConnectorKindsRegistry pins the consolidated kind registry that drives
// the create whitelist, the create validation, and the rebuild sequence: every entry
// is fully populated, lookup works for a managed and a non-managed kind, and the
// whitelist error message is derived from the registry in order.
func TestManagedConnectorKindsRegistry(t *testing.T) {
	if len(managedConnectorKinds) == 0 {
		t.Fatal("managedConnectorKinds is empty")
	}
	seen := map[string]bool{}
	for _, k := range managedConnectorKinds {
		// Every kind is created and validated in the Console, so name and validateCreate
		// are always required. The in-engine wiring (registry/rebuild/handlers) is only
		// required for a kind the engine runs itself; a worker-only kind has none of it
		// by design — its credential never enters the engine (ADR-0172).
		if k.name == "" || k.validateCreate == nil {
			t.Errorf("kind %q is incompletely defined: %+v", k.name, k)
		}
		if !k.workerOnly && (k.newRegistry == nil || k.rebuild == nil || k.registerHandlers == nil || k.problem == nil) {
			t.Errorf("engine-run kind %q is missing its in-engine wiring: %+v", k.name, k)
		}
		if k.workerOnly && (k.newRegistry != nil || k.rebuild != nil || k.registerHandlers != nil) {
			t.Errorf("worker-only kind %q must not wire an in-engine registry: %+v", k.name, k)
		}
		if seen[k.name] {
			t.Errorf("duplicate managed kind %q", k.name)
		}
		seen[k.name] = true
		if got, ok := lookupManagedConnectorKind(k.name); !ok || got.name != k.name {
			t.Errorf("lookupManagedConnectorKind(%q) = %+v, %v", k.name, got, ok)
		}
	}
	// The model-authored http.rest kind is not managed here.
	if _, ok := lookupManagedConnectorKind("http.rest"); ok {
		t.Error("http.rest should not be a managed connector kind")
	}
	// The whitelist error lists exactly the registered kinds, in order.
	want := "connector kind must be \"temis\", \"clio\", \"mail\", \"sharepoint\", \"remedy\", \"entra\", \"postgres\", \"mariadb\", or \"mssql\""
	if got := managedConnectorKindsError(); got != want {
		t.Errorf("managedConnectorKindsError() = %q, want %q", got, want)
	}
}

// TestConnectorStoreCRUD covers the durable store directly.
func TestConnectorStoreCRUD(t *testing.T) {
	st, err := newConnectorStore(filepath.Join(t.TempDir(), "connectors"))
	if err != nil {
		t.Fatalf("newConnectorStore: %v", err)
	}
	rec := connector{ID: "a", Name: "risk", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1}
	if err := st.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.Get("a")
	if err != nil || !ok || got.Name != "risk" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	all, err := st.LoadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("loadAll = %v, %v", all, err)
	}
	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.Get("a"); ok {
		t.Fatal("get after delete: still present")
	}
	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
}

// TestConnectorValidation covers the create endpoint's input checks.
func TestConnectorValidation(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if post(`{"endpoint":"http://x"}`) != http.StatusBadRequest {
		t.Error("missing name: want 400")
	}
	if post(`{"name":"a"}`) != http.StatusBadRequest {
		t.Error("missing endpoint: want 400")
	}
	if post(`{"name":"cliocon","kind":"clio","endpoint":"http://x"}`) != http.StatusOK {
		t.Error("clio kind is now configurable: want 200")
	}
	if post(`{"name":"a","kind":"http.rest","endpoint":"http://x"}`) != http.StatusBadRequest {
		t.Error("unsupported kind: want 400")
	}
	if post(`{"name":"dup","endpoint":"http://x"}`) != http.StatusOK {
		t.Fatal("first create: want 200")
	}
	if post(`{"name":"dup","endpoint":"http://y"}`) != http.StatusConflict {
		t.Error("duplicate name: want 409")
	}
}

// TestManagedConnectorExecutesDecision is the point of managed configuration: a
// central decision parks until its connector is configured *in the Console* (no
// env), then executes once the connector instance exists; disabling it parks the
// decision again — all without a restart.
func TestManagedConnectorExecutesDecision(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"outputs":{"Dish":"Roastbeef"}}`))
	}))
	defer ts.Close()

	srv, _ := newValidateServer(t) // no ATLAS_TEMIS_* env
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Central")
	x.saveDraft(pid, centralDecisionBPMN) // references connector "risk"
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var rep projectDeployResp
	_ = json.Unmarshal(b, &rep)
	key := rep.Definitions[0].Key

	// runInstance creates an instance (always 200 now — an un-runnable decision no
	// longer fails the create). incidents counts the raised incidents: a central
	// decision whose connector isn't configured fails its job, which (ADR-0061)
	// parks the token at the business rule task with an incident; a configured
	// connector executes the decision so the instance completes with no new one.
	runInstance := func() {
		if code, cb := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
			t.Fatalf("create instance: %d %s", code, cb)
		}
	}
	incidents := func() int {
		_, b := x.do(http.MethodGet, "/api/v1/incidents", "")
		var r struct {
			Incidents []json.RawMessage `json:"incidents"`
		}
		_ = json.Unmarshal(b, &r)
		return len(r.Incidents)
	}

	// No connector yet → the decision can't run, so a new instance raises an incident.
	runInstance()
	if incidents() == 0 {
		t.Fatal("before configuring the connector: want an incident (decision can't run)")
	}

	// Configure the "risk" connector in the Console → a new instance now executes:
	// temis is called and no additional incident is raised.
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"risk","endpoint":"`+ts.URL+`"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	before := incidents()
	callsBefore := atomic.LoadInt32(&calls)
	runInstance()
	if atomic.LoadInt32(&calls) == callsBefore {
		t.Fatal("after configuring the connector: temis service was never called")
	}
	if incidents() != before {
		t.Fatalf("after configuring the connector: incidents changed %d→%d, want the decision to execute", before, incidents())
	}

	// Disable it → a new instance can't run again, raising another incident.
	var created connector
	_ = json.Unmarshal(cb, &created)
	if code, ub := x.do(http.MethodPatch, "/api/v1/connectors/"+created.ID, `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable connector: %d %s", code, ub)
	}
	before = incidents()
	runInstance()
	if incidents() == before {
		t.Fatal("after disabling the connector: want a new incident (decision can't run)")
	}

	// The list shows the (disabled) instance and never a secret.
	_, lb := x.do(http.MethodGet, "/api/v1/connectors", "")
	if !strings.Contains(string(lb), `"name":"risk"`) || strings.Contains(string(lb), "token") {
		t.Fatalf("connector list = %s, want risk and no secret", lb)
	}

	// Deleting it is refused while the deployed model still references it: removing
	// the thing a resolved reference points at parks every one of its tasks, with a
	// message that reads as if it had never been configured (ADR-0163). The refusal
	// names the model rather than a count, because that is what an operator decides on.
	code, db := x.do(http.MethodDelete, "/api/v1/connectors/"+created.ID, "")
	if code != http.StatusConflict {
		t.Fatalf("delete a referenced connector = %d %s, want 409", code, db)
	}
	if !strings.Contains(string(db), `"usedBy"`) || !strings.Contains(string(db), `"decide"`) {
		t.Fatalf("refusal = %s, want the referencing process and its element", db)
	}

	// Deleting anyway is the operator's call, made explicitly.
	if code, b := x.do(http.MethodDelete, "/api/v1/connectors/"+created.ID+"?force=true", ""); code != http.StatusNoContent {
		t.Fatalf("forced delete: %d %s, want 204", code, b)
	}
	if code, _ := x.do(http.MethodPatch, "/api/v1/connectors/"+created.ID, `{"enabled":true}`); code != http.StatusNotFound {
		t.Fatalf("update deleted connector: want 404")
	}
}

// TestConnectorUpdateFields covers create with an explicit enabled flag and a
// partial update of the endpoint and credential reference (the field-mutation
// branches of handleUpdateConnector).
func TestConnectorUpdateFields(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	code, b := do(http.MethodPost, "/api/v1/connectors", `{"name":"c","endpoint":"http://x","enabled":true}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, b)
	}
	var c connector
	_ = json.Unmarshal(b, &c)
	if !c.Enabled {
		t.Fatalf("create with enabled:true = %+v", c)
	}
	code, b = do(http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"endpoint":"http://y","credentialsRef":"cred"}`)
	if code != http.StatusOK {
		t.Fatalf("update: %d %s", code, b)
	}
	var up connector
	_ = json.Unmarshal(b, &up)
	if up.Endpoint != "http://y" || up.CredentialsRef != "cred" {
		t.Fatalf("update result = %+v, want endpoint http://y and ref cred", up)
	}
}

// TestMailConnectorValidationAndCreate covers the create endpoint's mail-specific
// input checks and a successful mail connector create (ADR-0079).
func TestMailConnectorValidationAndCreate(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	post := func(body string) (int, connector) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var c connector
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return rec.Code, c
	}
	if code, _ := post(`{"name":"m1","kind":"mail","endpoint":"smtp.example.com:587"}`); code != http.StatusBadRequest {
		t.Error("mail without sender: want 400")
	}
	if code, _ := post(`{"name":"m2","kind":"mail","endpoint":"smtp.example.com:587","sender":"bot@x","provider":"gmail-api"}`); code != http.StatusBadRequest {
		t.Error("mail with an unsupported provider: want 400")
	}
	code, c := post(`{"name":"office365","kind":"mail","endpoint":"smtp.office365.com:587","sender":"bot@example.com","credentialsRef":"o365_pw"}`)
	if code != http.StatusOK {
		t.Fatalf("valid mail create: want 200, got %d", code)
	}
	if c.Kind != "mail" || c.Provider != mail.ProviderSMTP || c.Sender != "bot@example.com" {
		t.Errorf("mail record = %+v, want kind mail, provider smtp, sender set", c)
	}
	// A native provider needs a credentialsRef (its vault auth bundle) but no endpoint.
	if code, _ := post(`{"name":"g1","kind":"mail","provider":"gmail","sender":"bot@x"}`); code != http.StatusBadRequest {
		t.Error("gmail without credentialsRef: want 400")
	}
	code, g := post(`{"name":"gmail-notify","kind":"mail","provider":"gmail","sender":"bot@example.com","credentialsRef":"gmail_bundle"}`)
	if code != http.StatusOK {
		t.Fatalf("valid gmail create (no endpoint): want 200, got %d", code)
	}
	if g.Provider != mail.ProviderGmail || g.Endpoint != "" {
		t.Errorf("gmail record = %+v, want provider gmail and no endpoint", g)
	}
	if code, _ := post(`{"name":"ms1","kind":"mail","provider":"microsoft","sender":"bot@example.com","credentialsRef":"graph_bundle"}`); code != http.StatusOK {
		t.Error("valid microsoft create (no endpoint): want 200")
	}
}

// TestBuildMailNativeClients proves buildMailClients dispatches native providers: a
// gmail/microsoft record whose credentialsRef resolves to a valid OAuth bundle becomes
// the matching client, while a record whose bundle is malformed is skipped (its tasks
// park) rather than failing the whole rebuild (ADR-0080).
func TestBuildMailNativeClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// The credential bundle lives in the vault; here it resolves from the env fallback
	// (ATLAS_CONNECTOR_<REF>_TOKEN), never in the record itself.
	t.Setenv("ATLAS_CONNECTOR_GMAIL_BUNDLE_TOKEN", `{"method":"refreshToken","clientId":"c","clientSecret":"s","refreshToken":"r"}`)
	t.Setenv("ATLAS_CONNECTOR_GRAPH_BUNDLE_TOKEN", `{"method":"clientCredentials","tenantId":"t","clientId":"c","clientSecret":"s"}`)
	t.Setenv("ATLAS_CONNECTOR_BAD_BUNDLE_TOKEN", `not valid json`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "gmail", Kind: "mail", Provider: "gmail", Sender: "a@x", CredentialsRef: "gmail_bundle", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "graph", Kind: "mail", Provider: "microsoft", Sender: "b@x", CredentialsRef: "graph_bundle", Enabled: true, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "broken", Kind: "mail", Provider: "gmail", Sender: "c@x", CredentialsRef: "bad_bundle", Enabled: true, CreatedAt: 3})

	clients, _, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients = %d, want 2 (gmail + graph; the broken bundle is skipped)", len(clients))
	}
	if _, ok := clients["gmail"].(*mail.GmailClient); !ok {
		t.Errorf("gmail connector = %T, want *mail.GmailClient", clients["gmail"])
	}
	if _, ok := clients["graph"].(*mail.GraphClient); !ok {
		t.Errorf("microsoft connector = %T, want *mail.GraphClient", clients["graph"])
	}
	if _, ok := clients["broken"]; ok {
		t.Error("a malformed credential bundle should be skipped, not built")
	}
}

// TestMailConnectorLifecycle drives a mail connector through the full Console
// management surface — create, sender update, list, delete — so the create branch
// (mail kind), the sender-update branch, and the mail arm of the registry rebuild
// are all exercised end to end (ADR-0079).
func TestMailConnectorLifecycle(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	code, b := do(http.MethodPost, "/api/v1/connectors",
		`{"name":"office365","kind":"mail","endpoint":"smtp.office365.com:587","sender":"bot@example.com","credentialsRef":"o365_pw"}`)
	if code != http.StatusOK {
		t.Fatalf("create mail connector: %d %s", code, b)
	}
	var c connector
	_ = json.Unmarshal(b, &c)

	// Update the sender (the mail-only field) — rebuilds the registry with the new value.
	code, b = do(http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"sender":"notifications@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("update mail sender: %d %s", code, b)
	}
	var up connector
	_ = json.Unmarshal(b, &up)
	if up.Sender != "notifications@example.com" {
		t.Fatalf("sender after update = %q, want the new sender", up.Sender)
	}

	// The list shows the mail connector and never a secret value.
	_, lb := do(http.MethodGet, "/api/v1/connectors", "")
	if !strings.Contains(string(lb), `"kind":"mail"`) || strings.Contains(string(lb), "o365_pw_value") {
		t.Fatalf("connector list = %s, want the mail connector and only the reference", lb)
	}

	// Delete it → the registry rebuilds without it.
	if code, _ := do(http.MethodDelete, "/api/v1/connectors/"+c.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete mail connector: want 204")
	}
}

// TestBuildMailClients covers the managed-connector → SMTP client build: only enabled
// records of kind "mail" with a non-empty endpoint and a supported provider become
// clients; a disabled, non-mail, endpoint-less, or unknown-provider record is skipped.
func TestBuildMailClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "on", Kind: "mail", Endpoint: "smtp.a:587", Sender: "a@x", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: "mail", Endpoint: "smtp.b:587", Sender: "b@x", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "clio", Kind: "clio", Endpoint: "http://c", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "noendpoint", Kind: "mail", Endpoint: "", Sender: "d@x", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "future", Kind: "mail", Endpoint: "graph:0", Provider: "microsoft-graph", Enabled: true, CreatedAt: 5})

	clients, _, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1 (only the enabled SMTP mail record)", len(clients))
	}
	if _, ok := clients["on"]; !ok {
		t.Errorf("clients = %v, want the 'on' connector", clients)
	}
}

// TestBuildMailClientsLoadError covers buildMailClients' store-read failure.
func TestBuildMailClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildMailClients(); err == nil {
		t.Error("buildMailClients with a broken store: want error")
	}
}

// TestRemedyConnectorValidationAndCreate covers the create-handler validation for the
// Remedy kind (ADR-0106): the kind is accepted, an endpoint and a credentialsRef are
// both required, and a valid create stores the record with only the credential
// reference (never a secret).
func TestRemedyConnectorValidationAndCreate(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	post := func(body string) (int, connector) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var c connector
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return rec.Code, c
	}
	// No endpoint → 400 (every non-mail kind needs one).
	if code, _ := post(`{"name":"r0","kind":"remedy","credentialsRef":"remedy_creds"}`); code != http.StatusBadRequest {
		t.Error("remedy without endpoint: want 400")
	}
	// Endpoint but no credentialsRef → 400 (Remedy must authenticate).
	if code, _ := post(`{"name":"r1","kind":"remedy","endpoint":"https://helix.example.com"}`); code != http.StatusBadRequest {
		t.Error("remedy without credentialsRef: want 400")
	}
	code, c := post(`{"name":"helix","kind":"remedy","endpoint":"https://helix.example.com","credentialsRef":"remedy_creds"}`)
	if code != http.StatusOK {
		t.Fatalf("valid remedy create: want 200, got %d", code)
	}
	if c.Kind != connectorKindRemedy || c.Endpoint != "https://helix.example.com" || c.CredentialsRef != "remedy_creds" {
		t.Errorf("remedy record = %+v, want kind remedy with endpoint and credential reference", c)
	}
}

// TestBuildRemedyClients covers the managed-connector → Remedy client build: only an
// enabled record of kind "remedy" with a non-empty endpoint and a credentialsRef that
// resolves to a valid {username,password} bundle becomes a client; a disabled,
// non-remedy, endpoint-less, credential-less, or malformed-bundle record is skipped
// (its tasks park) rather than failing the whole rebuild.
func TestBuildRemedyClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// The credential bundle lives in the vault; here it resolves from the env fallback
	// (ATLAS_CONNECTOR_<REF>_TOKEN), never in the record itself.
	t.Setenv("ATLAS_CONNECTOR_HELIX_CREDS_TOKEN", `{"username":"svc","password":"pw"}`)
	t.Setenv("ATLAS_CONNECTOR_BAD_CREDS_TOKEN", `not valid json`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "on", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "helix_creds", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "helix_creds", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "mail", Kind: "mail", Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "noendpoint", Kind: "remedy", Endpoint: "", CredentialsRef: "helix_creds", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "nocreds", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "", Enabled: true, CreatedAt: 5})
	_ = srv.connectors.Save(connector{ID: "6", Name: "broken", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "bad_creds", Enabled: true, CreatedAt: 6})

	clients, _, err := srv.buildRemedyClients()
	if err != nil {
		t.Fatalf("buildRemedyClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1 (only the enabled, credentialed remedy record)", len(clients))
	}
	if _, ok := clients["on"]; !ok {
		t.Errorf("clients = %v, want the 'on' connector", clients)
	}
}

// TestBuildRemedyClientsLoadError covers buildRemedyClients' store-read failure.
func TestBuildRemedyClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildRemedyClients(); err == nil {
		t.Error("buildRemedyClients with a broken store: want error")
	}
}

// TestConnectorStoreLoadAllOrdering exercises loadAll's sort comparator with
// records that both differ in and share CreatedAt (the tie-break by id).
func TestConnectorStoreLoadAllOrdering(t *testing.T) {
	st, _ := newConnectorStore(filepath.Join(t.TempDir(), "c"))
	_ = st.Save(connector{ID: "b", Name: "b", Kind: "temis", Endpoint: "http://x", CreatedAt: 2})
	_ = st.Save(connector{ID: "a", Name: "a", Kind: "temis", Endpoint: "http://x", CreatedAt: 2})
	_ = st.Save(connector{ID: "z", Name: "z", Kind: "temis", Endpoint: "http://x", CreatedAt: 1})
	all, err := st.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	got := fmt.Sprintf("%s,%s,%s", all[0].ID, all[1].ID, all[2].ID)
	if got != "z,a,b" { // earliest CreatedAt first, then id tie-break a<b
		t.Fatalf("order = %s, want z,a,b", got)
	}
}

// TestConnectorStoreLoadAllReadError covers loadAll's per-file read-error branch:
// a hex-named .json record that is a directory passes the name filter but can't
// be read.
func TestConnectorStoreLoadAllReadError(t *testing.T) {
	st, _ := newConnectorStore(filepath.Join(t.TempDir(), "c"))
	// A dangling symlink named like a record: ReadDir reports it as a non-directory
	// entry that clears the hex/.json filter, but ReadFile follows it and fails.
	if err := os.Symlink(filepath.Join(st.Dir(), "missing"), st.FileFor("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadAll(); err == nil {
		t.Error("loadAll over an unreadable record: want error")
	}
}

// TestBuildTemisClientsLoadError covers buildTemisClients' store-read failure.
func TestBuildTemisClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildTemisClients(); err == nil {
		t.Error("buildTemisClients with a broken store: want error")
	}
}

// TestConnectorHandlerRegistryRebuildErrors covers the save-failure and
// post-save/-delete registry-rebuild failure branches of the update and delete
// handlers (all surfaced as 500s).
func TestConnectorHandlerRegistryRebuildErrors(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Update save failure: a valid record whose temp-write path is a directory, so
	// the atomic write can't create its temp file (get succeeds, save fails).
	stSave, _ := newConnectorStore(filepath.Join(t.TempDir(), "save"))
	_ = stSave.Save(connector{ID: "u", Name: "u", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
	if err := os.Mkdir(stSave.FileFor("u")+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stSave
	if do(http.MethodPatch, "/api/v1/connectors/u", `{"endpoint":"http://y"}`) != http.StatusInternalServerError {
		t.Error("update with a failing save: want 500")
	}

	// Update rebuild failure: the target record reads and saves fine, but a corrupt
	// *other* record makes the post-save buildTemisClients loadAll fail.
	stUp, _ := newConnectorStore(filepath.Join(t.TempDir(), "up"))
	_ = stUp.Save(connector{ID: "u2", Name: "u2", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
	if err := os.WriteFile(stUp.FileFor("junk"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stUp
	if do(http.MethodPatch, "/api/v1/connectors/u2", `{"endpoint":"http://y"}`) != http.StatusInternalServerError {
		t.Error("update with a failing registry rebuild: want 500")
	}

	// Delete rebuild failure: deleting a (missing) id succeeds, but a corrupt record
	// makes the post-delete rebuild fail.
	stDel, _ := newConnectorStore(filepath.Join(t.TempDir(), "del"))
	if err := os.WriteFile(stDel.FileFor("junk"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stDel
	if do(http.MethodDelete, "/api/v1/connectors/other", "") != http.StatusInternalServerError {
		t.Error("delete with a failing registry rebuild: want 500")
	}

	// Delete read failure: the reference guard reads the record before deleting it
	// (ADR-0163), so a record that will not decode fails the request rather than
	// silently skipping the check and deleting anyway.
	stRead, _ := newConnectorStore(filepath.Join(t.TempDir(), "read"))
	if err := os.WriteFile(stRead.FileFor("u3"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stRead
	if do(http.MethodDelete, "/api/v1/connectors/u3", "") != http.StatusInternalServerError {
		t.Error("delete whose reference check cannot read the record: want 500")
	}
}

// TestResolveConnectorSecret covers the env-reference secret resolution.
func TestResolveConnectorSecret(t *testing.T) {
	if envConnectorSecret("") != "" {
		t.Error("empty ref should resolve to no token")
	}
	t.Setenv("ATLAS_CONNECTOR_RISK_TOKEN_TOKEN", "sekret")
	if got := envConnectorSecret("risk_token"); got != "sekret" {
		t.Errorf("envConnectorSecret(risk_token) = %q, want sekret", got)
	}
	if envConnectorSecret("unset") != "" {
		t.Error("unset ref should resolve to no token")
	}
}

// TestConnectorStoreErrors covers the store's non-happy paths.
func TestConnectorStoreErrors(t *testing.T) {
	// loadAll ignores foreign files (subdir, non-json, non-hex name).
	st, _ := newConnectorStore(filepath.Join(t.TempDir(), "c"))
	if err := os.Mkdir(filepath.Join(st.Dir(), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "zz.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err) // non-hex name → skipped
	}
	_ = st.Save(connector{ID: "a", Name: "n", Kind: "temis", Endpoint: "http://x", CreatedAt: 1})
	all, err := st.LoadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("loadAll = %v, %v, want 1 record", all, err)
	}

	// get on a corrupt record errors.
	if err := os.WriteFile(st.FileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Get("bad"); err == nil {
		t.Error("get of corrupt record: want error")
	}
	if _, err := st.LoadAll(); err == nil {
		t.Error("loadAll over a corrupt record: want error")
	}

	// newConnectorStore under a file (can't create dir) errors.
	f := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	if _, err := newConnectorStore(filepath.Join(f, "sub")); err == nil {
		t.Error("newConnectorStore under a file: want error")
	}

	// loadAll on a missing dir errors.
	gone, _ := newConnectorStore(filepath.Join(t.TempDir(), "gone"))
	_ = os.RemoveAll(gone.Dir())
	if _, err := gone.LoadAll(); err == nil {
		t.Error("loadAll of a missing dir: want error")
	}

	// A record path that is a (non-empty) directory triggers the read/remove error
	// branches of get and delete.
	fresh, _ := newConnectorStore(filepath.Join(t.TempDir(), "d"))
	dirRec := fresh.FileFor("dir")
	if err := os.Mkdir(dirRec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirRec, "x"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Get("dir"); err == nil {
		t.Error("get on a directory record: want error")
	}
	if err := fresh.Delete("dir"); err == nil {
		t.Error("delete of a non-empty directory record: want error")
	}
}

// TestConnectorHandlerErrors covers the create/update endpoints' input-error paths.
func TestConnectorHandlerErrors(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string, r io.Reader) int {
		var req *http.Request
		if r != nil {
			req = httptest.NewRequest(method, path, r)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if do(http.MethodPost, "/api/v1/connectors", "{not json", nil) != http.StatusBadRequest {
		t.Error("create invalid JSON: want 400")
	}
	if do(http.MethodPost, "/api/v1/connectors", "", errReader{}) != http.StatusBadRequest {
		t.Error("create read-body error: want 400")
	}
	if do(http.MethodPatch, "/api/v1/connectors/x", "{not json", nil) != http.StatusBadRequest {
		t.Error("update invalid JSON: want 400")
	}
	if do(http.MethodPatch, "/api/v1/connectors/x", "", errReader{}) != http.StatusBadRequest {
		t.Error("update read-body error: want 400")
	}
	if do(http.MethodGet, "/api/v1/connectors", "", nil) != http.StatusOK {
		t.Error("list: want 200")
	}
}

// TestConnectorHandlerStoreErrors covers the endpoints' store-failure (500)
// branches by pointing the server at an unusable connector store.
func TestConnectorHandlerStoreErrors(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// A store whose directory does not exist: list/create/delete all fail.
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if do(http.MethodGet, "/api/v1/connectors", "") != http.StatusInternalServerError {
		t.Error("list with a broken store: want 500")
	}
	if do(http.MethodPost, "/api/v1/connectors", `{"name":"n","endpoint":"http://x"}`) != http.StatusInternalServerError {
		t.Error("create with a broken store: want 500")
	}
	if do(http.MethodDelete, "/api/v1/connectors/x", "") != http.StatusInternalServerError {
		t.Error("delete with a broken store: want 500")
	}

	// A store with a corrupt record for an existing id: update fails on read.
	good, _ := newConnectorStore(filepath.Join(t.TempDir(), "c2"))
	if err := os.WriteFile(good.FileFor("u"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = good
	if do(http.MethodPatch, "/api/v1/connectors/u", `{"enabled":false}`) != http.StatusInternalServerError {
		t.Error("update over a corrupt record: want 500")
	}
}

// TestMailProblemsExplainASkippedConnector is the fix for the report that started
// this: a mail task parked with "no connector registered as %q" while the connector
// sat right there in the list. Every reason a configured connector is left out of the
// registry must now come back as a sentence an operator can act on — and the
// never-configured case must stay distinguishable from all of them.
func TestMailProblemsExplainASkippedConnector(t *testing.T) {
	srv, _ := newValidateServer(t)
	t.Setenv("ATLAS_CONNECTOR_BAD_BUNDLE_TOKEN", `not valid json`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "works", Kind: "mail", Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "a@x", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: "mail", Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "b@x", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "no creds", Kind: "mail", Provider: "gmail", Sender: "c@x", CredentialsRef: "missing_ref", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "bad bundle", Kind: "mail", Provider: "gmail", Sender: "d@x", CredentialsRef: "bad_bundle", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "elsewhere", Kind: "clio", Endpoint: "https://clio.example", Enabled: true, CreatedAt: 5})

	clients, problems, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	if _, ok := clients["works"]; !ok || len(clients) != 1 {
		t.Fatalf("clients = %v, want exactly the usable one", clients)
	}
	if why, ok := problems["works"]; ok {
		t.Errorf("the usable connector carries a problem: %q", why)
	}
	for name, want := range map[string]string{
		"off":        "disabled",
		"no creds":   "no credential",
		"bad bundle": "not valid JSON",
		"elsewhere":  `not "mail"`,
	} {
		why, ok := problems[name]
		if !ok {
			t.Errorf("no problem recorded for %q — it would report as never configured", name)
			continue
		}
		if !strings.Contains(why, want) {
			t.Errorf("problem for %q = %q, want it to mention %q", name, why, want)
		}
	}

	// The message the parked token carries: the reason, not a denial that it exists.
	srv.mailRegistry = mail.NewRegistry()
	srv.mailRegistry.ReplaceWith(clients, problems)
	if got := srv.mailRegistry.Unresolved("mail", "off").Error(); !strings.Contains(got, "configured but not usable") {
		t.Errorf("incident message for a disabled connector = %q", got)
	}
	if got := srv.mailRegistry.Unresolved("mail", "never heard of it").Error(); !strings.Contains(got, "no connector registered") {
		t.Errorf("incident message for an unconfigured connector = %q", got)
	}
}

// TestListConnectorsCarriesTheProblem: the operator list is where you look *before*
// a token parks, so a stored-but-unusable connector has to say so there too.
func TestListConnectorsCarriesTheProblem(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "off", Kind: "mail", Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "b@x", Enabled: false, CreatedAt: 1})
	if err := srv.rebuildConnectorRegistries(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var views []connectorView
	srv.do(func() {
		recs, _ := srv.connectors.LoadAll()
		for _, c := range recs {
			views = append(views, connectorView{connector: c, Problem: srv.connectorProblem(c.Kind, c.Name)})
		}
	})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if !strings.Contains(views[0].Problem, "disabled") {
		t.Errorf("listed connector's problem = %q, want it to name the disabled state", views[0].Problem)
	}
}

// deployWarnBPMN references a mail connector by name, the way every model refers to a
// server-registered connector (ADR-0036/0041).
const deployWarnBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="warnme" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="notify">
      <extensionElements><atlas:mailConnector connector="Patrick Blumer" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="end"/>
  </process>
</definitions>`

// TestConnectorWarningsCatchTheMismatchAtDeploy is the other half of the fix: a model
// that names a connector nobody configured deploys fine and then parks its first token.
// The deploy is the moment somebody is looking, so it says so there — without refusing,
// since deploying before the connectors exist is legitimate.
func TestConnectorWarningsCatchTheMismatchAtDeploy(t *testing.T) {
	srv, _ := newValidateServer(t)
	cp, err := compiler.Parse(1, 1, strings.NewReader(deployWarnBPMN))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// Nothing configured at all.
	warns := srv.connectorWarnings(cp)
	if len(warns) != 1 || !strings.Contains(warns[0], "not configured on this server") {
		t.Fatalf("warnings with no connector = %v, want one naming it as unconfigured", warns)
	}
	if !strings.Contains(warns[0], "notify") || !strings.Contains(warns[0], "Patrick Blumer") {
		t.Errorf("warning names neither the element nor the connector: %q", warns[0])
	}

	// Configured under the wrong kind — the mistake that reads as "no such connector".
	_ = srv.connectors.Save(connector{ID: "1", Name: "Patrick Blumer", Kind: "clio", Endpoint: "https://clio.example", Enabled: true, CreatedAt: 1})
	if err := srv.rebuildConnectorRegistries(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	warns = srv.connectorWarnings(cp)
	if len(warns) != 1 || !strings.Contains(warns[0], `configured as a "clio" connector`) {
		t.Fatalf("warnings for a wrong-kind connector = %v", warns)
	}

	// Configured, right kind, but disabled: stored and still not going to run.
	_ = srv.connectors.Save(connector{ID: "1", Name: "Patrick Blumer", Kind: "mail", Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "a@x", Enabled: false, CreatedAt: 1})
	if err := srv.rebuildConnectorRegistries(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	warns = srv.connectorWarnings(cp)
	if len(warns) != 1 || !strings.Contains(warns[0], "configured but not usable") || !strings.Contains(warns[0], "disabled") {
		t.Fatalf("warnings for a disabled connector = %v", warns)
	}

	// Usable: nothing to say.
	_ = srv.connectors.Save(connector{ID: "1", Name: "Patrick Blumer", Kind: "mail", Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "a@x", Enabled: true, CreatedAt: 1})
	if err := srv.rebuildConnectorRegistries(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if warns = srv.connectorWarnings(cp); len(warns) != 0 {
		t.Errorf("warnings for a working connector = %v, want none", warns)
	}
}

// TestConnectorProviderUpdate covers the fix an operator reaches for from a parked
// mail task: switch the connector's provider instead of re-creating it under the same
// name (which would be a different connector as far as every model referencing it is
// concerned). A move to the in-app preview transport drops the endpoint and credential
// it no longer dials; a move to a native provider without a credential bundle is
// refused at the moment it is typed rather than at the next send (ADR-0160).
func TestConnectorProviderUpdate(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	code, b := do(http.MethodPost, "/api/v1/connectors",
		`{"name":"Patrick Blumer","kind":"mail","endpoint":"smtp.office365.com:587","sender":"bot@example.com","credentialsRef":"o365_pw"}`)
	if code != http.StatusOK {
		t.Fatalf("create mail connector: %d %s", code, b)
	}
	var c connector
	_ = json.Unmarshal(b, &c)

	// Gmail without a credential bundle: refused, and the stored record is untouched.
	code, b = do(http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":"gmail","credentialsRef":""}`)
	if code != http.StatusBadRequest || !strings.Contains(string(b), "credentialsRef") {
		t.Fatalf("switch to gmail with no bundle = %d %s, want 400 naming the credentialsRef", code, b)
	}

	// Preview: nothing to dial, nothing to authenticate — the endpoint and credential
	// are cleared so no dead configuration is left reading as if it were in use.
	code, b = do(http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":"preview"}`)
	if code != http.StatusOK {
		t.Fatalf("switch to preview: %d %s", code, b)
	}
	var up connector
	_ = json.Unmarshal(b, &up)
	if up.Provider != "preview" || up.Endpoint != "" || up.CredentialsRef != "" {
		t.Fatalf("record after switching to preview = %+v, want provider preview and no endpoint/credential", up)
	}

	// And the switch is live: the rebuilt registry has a usable client under the name,
	// so a task referencing it stops parking.
	var (
		client any
		ok     bool
	)
	srv.do(func() { client, ok = srv.mailRegistry.Client("Patrick Blumer") })
	if !ok || client == nil {
		t.Fatalf("mail registry after the switch has no client for the connector")
	}

	// An unknown provider is rejected by the same validation a create runs.
	if code, b := do(http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":"carrier-pigeon"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown provider = %d %s, want 400", code, b)
	}
}

// TestElementConnectorRefEdges covers the answers the incident's connector lookup has
// to give when there is nothing to resolve: an instance whose definition is no longer
// deployed (no compiled process to ask), and a task whose reserved job type belongs to
// no managed connector kind — a REST task carries its URL in the model and resolves
// through no registry, so its reference has a name but no kind and can match no stored
// record (ADR-0160).
func TestElementConnectorRefEdges(t *testing.T) {
	if name, kind := elementConnectorRef(nil, 0); name != "" || kind != "" {
		t.Errorf("elementConnectorRef(nil) = %q/%q, want both empty", name, kind)
	}
	if kind := connectorKindOfJobType(-1); kind != "" {
		t.Errorf("connectorKindOfJobType(-1) = %q, want no kind", kind)
	}
	srv, _ := newValidateServer(t)
	name, kind, id := srv.incidentConnectorLookup()(nil, 0)
	if name != "" || kind != "" || id != "" {
		t.Errorf("lookup on no compiled process = %q/%q/%q, want all empty", name, kind, id)
	}
}

// TestConnectorUsageIsVisibleBeforeDeleting covers the reverse of the deploy-time
// check: the connector list says which deployed models reference each connector, and
// how many instances are running on them, so an operator can see what a delete would
// park before attempting it — and a connector nothing references deletes without a
// fight (ADR-0163).
func TestConnectorUsageIsVisibleBeforeDeleting(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Central")
	x.saveDraft(pid, centralDecisionBPMN) // references the temis connector "risk"
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}

	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"risk","endpoint":"http://risk.internal"}`)
	if code != http.StatusOK {
		t.Fatalf("create referenced connector: %d %s", code, cb)
	}
	var referenced connector
	_ = json.Unmarshal(cb, &referenced)

	code, ob := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"orphan","endpoint":"http://nobody.internal"}`)
	if code != http.StatusOK {
		t.Fatalf("create unreferenced connector: %d %s", code, ob)
	}
	var orphan connector
	_ = json.Unmarshal(ob, &orphan)

	// The list carries the usage per connector: the referenced one names the model and
	// the element, the unreferenced one carries nothing at all.
	_, lb := x.do(http.MethodGet, "/api/v1/connectors", "")
	var views []struct {
		Name   string         `json:"name"`
		UsedBy []connectorUse `json:"usedBy"`
	}
	if err := json.Unmarshal(lb, &views); err != nil {
		t.Fatalf("connector list: %v (%s)", err, lb)
	}
	byName := map[string][]connectorUse{}
	for _, v := range views {
		byName[v.Name] = v.UsedBy
	}
	uses := byName["risk"]
	if len(uses) != 1 {
		t.Fatalf("usedBy for the referenced connector = %+v, want exactly the deployed model", uses)
	}
	if len(uses[0].Elements) != 1 || uses[0].Elements[0] != "decide" {
		t.Errorf("usedBy elements = %v, want the business rule task", uses[0].Elements)
	}
	if uses[0].ProcessDefKey == 0 || uses[0].Version == 0 {
		t.Errorf("usedBy = %+v, want the definition key and version so the UI can link to it", uses[0])
	}
	if got := byName["orphan"]; len(got) != 0 {
		t.Errorf("usedBy for an unreferenced connector = %+v, want nothing", got)
	}

	// A second deployed model referencing the same connector aggregates onto it, in
	// definition-key order, so the operator sees every process a delete would park and
	// not just the first one found.
	pid2 := x.mkProject("Central again")
	x.saveDraft(pid2, centralDecisionBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid2+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("second deploy: %d %s", code, b)
	}
	_, lb2 := x.do(http.MethodGet, "/api/v1/connectors", "")
	var views2 []struct {
		Name   string         `json:"name"`
		UsedBy []connectorUse `json:"usedBy"`
	}
	if err := json.Unmarshal(lb2, &views2); err != nil {
		t.Fatalf("connector list: %v (%s)", err, lb2)
	}
	for _, v := range views2 {
		if v.Name != "risk" {
			continue
		}
		if len(v.UsedBy) != 2 {
			t.Fatalf("usedBy after a second deploy = %+v, want both definitions", v.UsedBy)
		}
		if v.UsedBy[0].ProcessDefKey >= v.UsedBy[1].ProcessDefKey {
			t.Errorf("usedBy = %+v, want definition-key order", v.UsedBy)
		}
	}

	// Nothing references it, so deleting it is uneventful — the guard must not stand
	// in the way of the ordinary case.
	if code, b := x.do(http.MethodDelete, "/api/v1/connectors/"+orphan.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete an unreferenced connector = %d %s, want 204", code, b)
	}
	// The referenced one is refused, and says by what.
	if code, b := x.do(http.MethodDelete, "/api/v1/connectors/"+referenced.ID, ""); code != http.StatusConflict {
		t.Fatalf("delete a referenced connector = %d %s, want 409", code, b)
	}
}

// TestConnectorTestDetail pins the operator-facing sentence a successful connection
// test reports, one per provider/recipient combination. The message is the whole
// point of the "test" button — it tells the operator whether a message actually left
// Atlas, or merely that a credential was accepted — so each branch is asserted for
// the distinction it draws, not just for a non-empty string.
func TestConnectorTestDetail(t *testing.T) {
	cases := []struct {
		name                     string
		provider, endpoint, to   string
		wantContains, wantAbsent string
	}{
		{
			name:     "preview provider with a recipient keeps it in the outbox",
			provider: mail.ProviderPreview, to: "a@b.ch",
			wantContains: "outbox", wantAbsent: "sent to a@b.ch",
		},
		{
			name:     "a real provider with a recipient reports the message left Atlas",
			provider: mail.ProviderSMTP, endpoint: "mx.example.ch:587", to: "a@b.ch",
			wantContains: "sent to a@b.ch",
		},
		{
			name:         "preview provider with no recipient dials nothing",
			provider:     mail.ProviderPreview,
			wantContains: "outbox", wantAbsent: "Connected",
		},
		{
			name:     "smtp with no recipient names the endpoint it authenticated against",
			provider: mail.ProviderSMTP, endpoint: "mx.example.ch:587",
			wantContains: "mx.example.ch:587",
		},
		{
			name:         "a token provider with no recipient reports the credential only",
			provider:     "graph",
			wantContains: "access token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connectorTestDetail(tc.provider, tc.endpoint, tc.to)
			if !strings.Contains(got, tc.wantContains) {
				t.Errorf("detail = %q, want it to contain %q", got, tc.wantContains)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("detail = %q, want it NOT to contain %q", got, tc.wantAbsent)
			}
		})
	}
}

// TestConnectorProblemFallback covers the two ways connectorProblem finds nothing to
// report: a kind no managed entry claims, and a managed kind that carries no problem
// probe. Both must return the empty string — connectorWarnings reads it as "usable"
// and stays silent — rather than a spurious warning.
func TestConnectorProblemFallback(t *testing.T) {
	srv, _ := newValidateServer(t)

	if why := srv.connectorProblem("no-such-kind", "whatever"); why != "" {
		t.Errorf("connectorProblem for an unmanaged kind = %q, want empty", why)
	}

	// Find a managed kind whose entry defines no problem probe, if one exists, and
	// confirm it too reports nothing rather than panicking on the nil probe.
	for _, k := range managedConnectorKinds {
		if k.problem == nil {
			if why := srv.connectorProblem(k.name, "whatever"); why != "" {
				t.Errorf("connectorProblem for probe-less kind %q = %q, want empty", k.name, why)
			}
			break
		}
	}
}

// An Entra tenant is created in the Console with nothing but a credentials
// reference: the tenant id, client id and client secret live together in one vault
// bundle, never in the record and never in a model (ADR-0172). The validator is what
// holds that shape, so it is what a test has to pin.
func TestEntraConnectorNeedsItsCredentialBundle(t *testing.T) {
	k, ok := lookupManagedConnectorKind("entra")
	if !ok {
		t.Fatal("entra is not a managed connector kind")
	}

	if msg := k.validateCreate(&createConnectorParams{Name: "tenant"}); msg == "" {
		t.Error("an entra connector without a credentialsRef was accepted")
	} else if !strings.Contains(msg, "credentialsRef") {
		t.Errorf("message = %q, want it to name the missing field", msg)
	}

	// With the bundle it passes — and the mail-only fields are cleared rather than
	// stored, so a record carried over from another kind cannot leave a provider or a
	// sender behind on a directory tenant.
	p := &createConnectorParams{Name: "tenant", CredentialsRef: "entra-bundle", Provider: "smtp", Sender: "bot@x"}
	if msg := k.validateCreate(p); msg != "" {
		t.Errorf("a complete entra connector was refused: %s", msg)
	}
	if p.Provider != "" || p.Sender != "" {
		t.Errorf("provider/sender = %q/%q, want both cleared for an entra record", p.Provider, p.Sender)
	}
}
