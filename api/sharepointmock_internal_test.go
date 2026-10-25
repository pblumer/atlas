package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/worker"
)

// The SharePoint mockup (ADR-0484) is a provider of the
// SharePoint Worker Type: a record that says so binds to a site in this server's
// memory, in the engine and in a supervised worker alike.

func spRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A mockup takes no credential and no endpoint, and keeps neither if a form sent them;
// switching it to Graph is refused until it has a credential again.
func TestSharePointMockWorkerRecord(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	rec := spRequest(t, h, http.MethodPost, "/api/v1/connectors", `{"name":"intranet","kind":"sharepoint","provider":"mock","credentialsRef":"x","endpoint":"https://graph.example"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create a mockup: %d %s", rec.Code, rec.Body)
	}
	var c connector
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Provider != sharepoint.MockProvider || c.CredentialsRef != "" || c.Endpoint != "" {
		t.Errorf("mockup record = %+v, want provider mock and no credential or endpoint", c)
	}
	if rec := spRequest(t, h, http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":""}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "credentialsRef") {
		t.Errorf("switching to Graph without a credential = %d %s, want 400 naming credentialsRef", rec.Code, rec.Body)
	}
	if rec := spRequest(t, h, http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":"","credentialsRef":"sp_bundle"}`); rec.Code != http.StatusOK {
		t.Errorf("switching to Graph with a credential = %d %s", rec.Code, rec.Body)
	}
	if rec := spRequest(t, h, http.MethodPatch, "/api/v1/connectors/"+c.ID, `{"provider":"mock"}`); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sp_bundle") {
		t.Errorf("switching back to the mockup = %d %s, want 200 and the credential reference dropped", rec.Code, rec.Body)
	}
}

// The engine and a worker in mockup mode reach one site: what a forwarded operation
// wrote, the engine's own client reads, the Console's view shows, and an operator can
// empty. A Worker that is not a mockup has no site, and a deleted one loses its own.
func TestSharePointMockSiteIsShared(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	t.Setenv("ATLAS_CONNECTOR_SP_BUNDLE_TOKEN", spBundle)
	created := spRequest(t, h, http.MethodPost, "/api/v1/connectors", `{"name":"intranet","kind":"sharepoint","provider":"mock"}`)
	var c connector
	_ = json.Unmarshal(created.Body.Bytes(), &c)
	spRequest(t, h, http.MethodPost, "/api/v1/connectors", `{"name":"contoso","kind":"sharepoint","credentialsRef":"sp_bundle"}`)

	var engineClient sharepoint.Client
	srv.do(func() { engineClient, _ = srv.sharePointRegistry.Client("intranet") })
	if _, ok := engineClient.(*sharepoint.MockSite); !ok {
		t.Fatalf("the engine's client for a mockup Worker = %T, want its site", engineClient)
	}

	ts := httptest.NewServer(h)
	defer ts.Close()
	remote := sharepoint.NewRemoteMock(ts.URL+"/api/v1/sharepoint/mock-sites", "intranet", "")
	site := "contoso.sharepoint.com:/sites/hr"
	ctx := context.Background()
	if _, err := remote.Do(ctx, sharepoint.Request{Site: site, List: "Antraege", Fields: map[string]any{"Title": "A", "Betrag": json.Number("12500")}}); err != nil {
		t.Fatalf("forwarded create-item: %v", err)
	}
	got, err := engineClient.Do(ctx, sharepoint.Request{Operation: "get-item", Site: site, List: "Antraege", ItemID: "1"})
	if err != nil {
		t.Fatalf("the engine does not see what the worker wrote: %v", err)
	}
	if f := got.(map[string]any)["fields"].(map[string]any); f["Betrag"] != json.Number("12500") {
		t.Errorf("Betrag = %#v, want the number as it was sent", f["Betrag"])
	}
	if _, err := remote.Do(ctx, sharepoint.Request{Operation: "get-item", Site: site, List: "Antraege", ItemID: "9"}); err == nil || !strings.Contains(err.Error(), "no item 9") {
		t.Errorf("a refusal forwarded = %v, want the site's own reason", err)
	}
	if _, err := sharepoint.NewRemoteMock(ts.URL+"/api/v1/sharepoint/mock-sites", "contoso", "").Do(ctx, sharepoint.Request{Site: site, List: "A"}); err == nil || !strings.Contains(err.Error(), "holds no mockup") {
		t.Errorf("forwarding for a Graph Worker = %v, want 404: the route never reaches a tenant", err)
	}

	// The Mockups view (ADR-0216): one report per mockup, the envelope the server
	// understands around the site it serves back.
	view := spRequest(t, h, http.MethodGet, "/api/v1/mocks", "")
	var body struct {
		Reports []struct {
			Kind, Source, Target, Summary string
			At                            int64
			Data                          sharepoint.MockSiteSnapshot
		} `json:"reports"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &body); err != nil || len(body.Reports) != 1 {
		t.Fatalf("view = %s", view.Body)
	}
	r := body.Reports[0]
	if r.Kind != "sharepoint" || r.Source != "engine" || r.Target != "intranet" || r.At == 0 || r.Summary != "1 site, 1 list or library, 1 refused operation" {
		t.Errorf("report envelope = %+v", r)
	}
	if w := r.Data; w.Worker != "intranet" || len(w.Sites) != 1 || w.Sites[0].Lists[0].Title != "Antraege" || len(w.Journal) != 3 {
		t.Errorf("view of the mockup = %+v", w)
	}

	if rec := spRequest(t, h, http.MethodDelete, "/api/v1/sharepoint/mock-sites/intranet", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("clear = %d", rec.Code)
	}
	if _, err := engineClient.Do(ctx, sharepoint.Request{Operation: "get-item", Site: site, List: "Antraege", ItemID: "1"}); err == nil {
		t.Error("the engine still reads an item from a site that was emptied")
	}
	if rec := spRequest(t, h, http.MethodDelete, "/api/v1/sharepoint/mock-sites/contoso", ""); rec.Code != http.StatusNotFound {
		t.Errorf("clearing a Graph Worker = %d, want 404", rec.Code)
	}

	if rec := spRequest(t, h, http.MethodDelete, "/api/v1/connectors/"+c.ID, ""); rec.Code/100 != 2 {
		t.Fatalf("delete the Worker = %d %s", rec.Code, rec.Body)
	}
	if _, ok := srv.sharePointMocks.Lookup("intranet"); ok {
		t.Error("a deleted Worker's site is still held")
	}
}

// A supervised worker is told which Workers are mockups and where their sites are, and
// nothing else about them — and builds the forwarding client from exactly that.
func TestSupervisedSharePointMockEnv(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://atlas.example/", nil, nil))
	if err := srv.connectors.Save(connector{ID: "1", Name: "intranet", Kind: connectorKindSharePoint, Provider: sharepoint.MockProvider, Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	env := envOf(t, srv.sharepointWorkerEnv())
	if env["ATLAS_SHAREPOINT_INTRANET_MOCK"] != "1" || env["ATLAS_SHAREPOINT_MOCK_URL"] != "http://atlas.example/api/v1/sharepoint/mock-sites" {
		t.Errorf("env = %v, want the mockup flag and the site's address", env)
	}
	for k := range env {
		if strings.HasSuffix(k, "_CREDENTIALS") || strings.HasSuffix(k, "_ENDPOINT") || strings.HasSuffix(k, "_BROKERED") {
			t.Errorf("a mockup was handed %s", k)
		}
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindSharePoint)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "intranet") {
		t.Errorf("the worker holds %v, want the mockup", built.Names)
	}

	// Without an address to forward to, the worker could only refuse the Worker at
	// startup — and every other instance with it — so it is not handed over.
	alone, _ := newValidateServer(t)
	_ = alone.connectors.Save(connector{ID: "1", Name: "intranet", Kind: connectorKindSharePoint, Provider: sharepoint.MockProvider, Enabled: true, CreatedAt: 1})
	if env := alone.sharepointWorkerEnv(); len(env) != 0 {
		t.Errorf("env without a server address = %v, want nothing", env)
	}
}

// A mockup is never delegated: a person's token released to it would be spent on
// nothing.
func TestSharePointMockIsNeverDelegated(t *testing.T) {
	srv, _ := newValidateServer(t)
	rec := connector{ID: "1", Name: "intranet", Kind: connectorKindSharePoint, Provider: sharepoint.MockProvider, Enabled: true}
	if srv.identity == nil {
		t.Skip("this test server has no identity store")
	}
	msg := srv.validateIdentity(rec, workerIdentity{Mode: "delegated-per-job", Enforcement: enforcementEnforce, ActAs: actAsInitiator})
	if msg != mockDelegatedRefusal {
		t.Errorf("delegating a mockup = %q, want the refusal", msg)
	}
	if msg := srv.validateIdentity(rec, workerIdentity{Mode: "service", Enforcement: enforcementShadow}); msg != "" {
		t.Errorf("governing a mockup with the service identity = %q, want it allowed", msg)
	}
}

// The remaining edges of the server half: a body that is not an operation, a summary
// counted in the plural, and a delegated Graph Worker that is switched to the mockup.
func TestSharePointMockServerEdges(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	spRequest(t, h, http.MethodPost, "/api/v1/connectors", `{"name":"intranet","kind":"sharepoint","provider":"mock"}`)
	if rec := spRequest(t, h, http.MethodPost, "/api/v1/sharepoint/mock-sites/intranet", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("a body that is not an operation = %d, want 400", rec.Code)
	}

	snap := sharepoint.MockSiteSnapshot{Sites: []sharepoint.MockWebSnapshot{{}, {Lists: []sharepoint.MockListSnapshot{{}, {}}}}}
	if got := sharePointMockSummary(snap); got != "2 sites, 2 lists and libraries, 0 refused operations" {
		t.Errorf("summary = %q", got)
	}

	if err := srv.connectors.Save(connector{
		ID: "9", Name: "delegiert", Kind: connectorKindSharePoint, CredentialsRef: "sp_bundle", Enabled: true, CreatedAt: 1,
		Identity: &workerIdentity{Mode: "delegated-per-job", ActAs: actAsInitiator, Enforcement: enforcementEnforce},
	}); err != nil {
		t.Fatal(err)
	}
	if rec := spRequest(t, h, http.MethodPatch, "/api/v1/connectors/9", `{"provider":"mock"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot be delegated") {
		t.Errorf("switching a delegated Worker to the mockup = %d %s, want 400 with the reason", rec.Code, rec.Body)
	}
}
