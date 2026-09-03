package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepoint"
)

// TestSharePointConnectorValidationAndCreate covers the create endpoint's
// SharePoint-specific input checks and a successful create (ADR-0141).
func TestSharePointConnectorValidationAndCreate(t *testing.T) {
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
	// A SharePoint worker needs a credentialsRef (its vault OAuth bundle).
	if code, _ := post(`{"name":"sp1","kind":"sharepoint"}`); code != http.StatusBadRequest {
		t.Error("sharepoint without credentialsRef: want 400")
	}
	// A valid create needs no endpoint (the Graph base defaults) and stores no
	// provider/sender (those are mail-only).
	code, c := post(`{"name":"contoso","kind":"sharepoint","credentialsRef":"sp_bundle","provider":"microsoft","sender":"ignored@x"}`)
	if code != http.StatusOK {
		t.Fatalf("valid sharepoint create: want 200, got %d", code)
	}
	if c.Kind != connectorKindSharePoint || c.Provider != "" || c.Sender != "" || c.Endpoint != "" {
		t.Errorf("sharepoint record = %+v, want kind sharepoint and no provider/sender/endpoint", c)
	}
	// An optional Graph base override is accepted.
	if code, _ := post(`{"name":"contoso-gov","kind":"sharepoint","credentialsRef":"sp_bundle","endpoint":"https://graph.microsoft.us/v1.0"}`); code != http.StatusOK {
		t.Error("valid sharepoint create with endpoint override: want 200")
	}
}

// TestBuildSharePointClients proves buildSharePointClients builds a Graph client from a
// valid OAuth bundle and skips a disabled, non-sharepoint, or malformed-bundle record
// (its tasks park) rather than failing the whole rebuild (ADR-0141).
func TestBuildSharePointClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	t.Setenv("ATLAS_CONNECTOR_SP_BUNDLE_TOKEN", `{"method":"clientCredentials","tenantId":"t","clientId":"c","clientSecret":"s"}`)
	t.Setenv("ATLAS_CONNECTOR_BAD_BUNDLE_TOKEN", `not valid json`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "contoso", Kind: connectorKindSharePoint, CredentialsRef: "sp_bundle", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: connectorKindSharePoint, CredentialsRef: "sp_bundle", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "clio", Kind: connectorKindClio, Endpoint: "http://c", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "broken", Kind: connectorKindSharePoint, CredentialsRef: "bad_bundle", Enabled: true, CreatedAt: 4})

	clients, _, err := srv.buildSharePointClients()
	if err != nil {
		t.Fatalf("buildSharePointClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1 (only the enabled valid record; the broken bundle is skipped)", len(clients))
	}
	if _, ok := clients["contoso"].(*sharepoint.GraphClient); !ok {
		t.Errorf("contoso worker = %T, want *sharepoint.GraphClient", clients["contoso"])
	}
	if _, ok := clients["broken"]; ok {
		t.Error("a malformed credential bundle should be skipped, not built")
	}
}

// TestBuildSharePointClientsLoadError covers buildSharePointClients' store-read failure.
func TestBuildSharePointClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildSharePointClients(); err == nil {
		t.Error("buildSharePointClients with a broken store: want error")
	}
}

// TestSharePointConnectorLifecycle drives a SharePoint worker through create, list,
// and delete, exercising the create branch and the sharepoint arm of the registry
// rebuild end to end (ADR-0141).
func TestSharePointConnectorLifecycle(t *testing.T) {
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
		`{"name":"contoso","kind":"sharepoint","credentialsRef":"sp_bundle"}`)
	if code != http.StatusOK {
		t.Fatalf("create sharepoint worker: %d %s", code, b)
	}
	var c connector
	_ = json.Unmarshal(b, &c)

	_, lb := do(http.MethodGet, "/api/v1/connectors", "")
	if !strings.Contains(string(lb), `"kind":"sharepoint"`) {
		t.Fatalf("worker list = %s, want the sharepoint worker", lb)
	}

	if code, _ := do(http.MethodDelete, "/api/v1/connectors/"+c.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete sharepoint worker: want 204")
	}
}
