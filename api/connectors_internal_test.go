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

	"github.com/pblumer/atlas/mail"
)

// TestConnectorStoreCRUD covers the durable store directly.
func TestConnectorStoreCRUD(t *testing.T) {
	st, err := newConnectorStore(filepath.Join(t.TempDir(), "connectors"))
	if err != nil {
		t.Fatalf("newConnectorStore: %v", err)
	}
	rec := connector{ID: "a", Name: "risk", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1}
	if err := st.save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.get("a")
	if err != nil || !ok || got.Name != "risk" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	all, err := st.loadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("loadAll = %v, %v", all, err)
	}
	if err := st.delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.get("a"); ok {
		t.Fatal("get after delete: still present")
	}
	if err := st.delete("a"); err != nil {
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

	// Delete it.
	if code, _ := x.do(http.MethodDelete, "/api/v1/connectors/"+created.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete connector: want 204")
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

	_ = srv.connectors.save(connector{ID: "1", Name: "gmail", Kind: "mail", Provider: "gmail", Sender: "a@x", CredentialsRef: "gmail_bundle", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.save(connector{ID: "2", Name: "graph", Kind: "mail", Provider: "microsoft", Sender: "b@x", CredentialsRef: "graph_bundle", Enabled: true, CreatedAt: 2})
	_ = srv.connectors.save(connector{ID: "3", Name: "broken", Kind: "mail", Provider: "gmail", Sender: "c@x", CredentialsRef: "bad_bundle", Enabled: true, CreatedAt: 3})

	clients, err := srv.buildMailClients()
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
	_ = srv.connectors.save(connector{ID: "1", Name: "on", Kind: "mail", Endpoint: "smtp.a:587", Sender: "a@x", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.save(connector{ID: "2", Name: "off", Kind: "mail", Endpoint: "smtp.b:587", Sender: "b@x", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.save(connector{ID: "3", Name: "clio", Kind: "clio", Endpoint: "http://c", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.save(connector{ID: "4", Name: "noendpoint", Kind: "mail", Endpoint: "", Sender: "d@x", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.save(connector{ID: "5", Name: "future", Kind: "mail", Endpoint: "graph:0", Provider: "microsoft-graph", Enabled: true, CreatedAt: 5})

	clients, err := srv.buildMailClients()
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
	srv.connectors = &connectorStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := srv.buildMailClients(); err == nil {
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

	_ = srv.connectors.save(connector{ID: "1", Name: "on", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "helix_creds", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.save(connector{ID: "2", Name: "off", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "helix_creds", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.save(connector{ID: "3", Name: "mail", Kind: "mail", Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.save(connector{ID: "4", Name: "noendpoint", Kind: "remedy", Endpoint: "", CredentialsRef: "helix_creds", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.save(connector{ID: "5", Name: "nocreds", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "", Enabled: true, CreatedAt: 5})
	_ = srv.connectors.save(connector{ID: "6", Name: "broken", Kind: "remedy", Endpoint: "https://helix", CredentialsRef: "bad_creds", Enabled: true, CreatedAt: 6})

	clients, err := srv.buildRemedyClients()
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
	srv.connectors = &connectorStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := srv.buildRemedyClients(); err == nil {
		t.Error("buildRemedyClients with a broken store: want error")
	}
}

// TestConnectorStoreLoadAllOrdering exercises loadAll's sort comparator with
// records that both differ in and share CreatedAt (the tie-break by id).
func TestConnectorStoreLoadAllOrdering(t *testing.T) {
	st, _ := newConnectorStore(filepath.Join(t.TempDir(), "c"))
	_ = st.save(connector{ID: "b", Name: "b", Kind: "temis", Endpoint: "http://x", CreatedAt: 2})
	_ = st.save(connector{ID: "a", Name: "a", Kind: "temis", Endpoint: "http://x", CreatedAt: 2})
	_ = st.save(connector{ID: "z", Name: "z", Kind: "temis", Endpoint: "http://x", CreatedAt: 1})
	all, err := st.loadAll()
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
	if err := os.Symlink(filepath.Join(st.dir, "missing"), st.fileFor("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.loadAll(); err == nil {
		t.Error("loadAll over an unreadable record: want error")
	}
}

// TestBuildTemisClientsLoadError covers buildTemisClients' store-read failure.
func TestBuildTemisClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = &connectorStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := srv.buildTemisClients(); err == nil {
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
	_ = stSave.save(connector{ID: "u", Name: "u", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
	if err := os.Mkdir(stSave.fileFor("u")+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stSave
	if do(http.MethodPatch, "/api/v1/connectors/u", `{"endpoint":"http://y"}`) != http.StatusInternalServerError {
		t.Error("update with a failing save: want 500")
	}

	// Update rebuild failure: the target record reads and saves fine, but a corrupt
	// *other* record makes the post-save buildTemisClients loadAll fail.
	stUp, _ := newConnectorStore(filepath.Join(t.TempDir(), "up"))
	_ = stUp.save(connector{ID: "u2", Name: "u2", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
	if err := os.WriteFile(stUp.fileFor("junk"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stUp
	if do(http.MethodPatch, "/api/v1/connectors/u2", `{"endpoint":"http://y"}`) != http.StatusInternalServerError {
		t.Error("update with a failing registry rebuild: want 500")
	}

	// Delete rebuild failure: deleting a (missing) id succeeds, but a corrupt record
	// makes the post-delete rebuild fail.
	stDel, _ := newConnectorStore(filepath.Join(t.TempDir(), "del"))
	if err := os.WriteFile(stDel.fileFor("junk"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = stDel
	if do(http.MethodDelete, "/api/v1/connectors/other", "") != http.StatusInternalServerError {
		t.Error("delete with a failing registry rebuild: want 500")
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
	if err := os.Mkdir(filepath.Join(st.dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.dir, "zz.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err) // non-hex name → skipped
	}
	_ = st.save(connector{ID: "a", Name: "n", Kind: "temis", Endpoint: "http://x", CreatedAt: 1})
	all, err := st.loadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("loadAll = %v, %v, want 1 record", all, err)
	}

	// get on a corrupt record errors.
	if err := os.WriteFile(st.fileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.get("bad"); err == nil {
		t.Error("get of corrupt record: want error")
	}
	if _, err := st.loadAll(); err == nil {
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
	_ = os.RemoveAll(gone.dir)
	if _, err := gone.loadAll(); err == nil {
		t.Error("loadAll of a missing dir: want error")
	}

	// A record path that is a (non-empty) directory triggers the read/remove error
	// branches of get and delete.
	fresh, _ := newConnectorStore(filepath.Join(t.TempDir(), "d"))
	dirRec := fresh.fileFor("dir")
	if err := os.Mkdir(dirRec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirRec, "x"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.get("dir"); err == nil {
		t.Error("get on a directory record: want error")
	}
	if err := fresh.delete("dir"); err == nil {
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
	srv.connectors = &connectorStore{dir: filepath.Join(t.TempDir(), "gone")}
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
	if err := os.WriteFile(good.fileFor("u"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.connectors = good
	if do(http.MethodPatch, "/api/v1/connectors/u", `{"enabled":false}`) != http.StatusInternalServerError {
		t.Error("update over a corrupt record: want 500")
	}
}
