package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// clioWriteBPMN is a Start → clio write-events service task → End process. The task
// bears an <atlas:clioConnector> extension naming the server-registered connector
// "events", so it appends to whatever clio instance the operator configures under
// that name (ADR-0036).
const clioWriteBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:atlas="http://atlas/schema/1.0">
  <process id="clioflow" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="emit">
      <extensionElements>
        <atlas:clioConnector connector="events" subject="orders/new" eventType="OrderPlaced"/>
      </extensionElements>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="emit"/>
    <sequenceFlow id="f2" sourceRef="emit" targetRef="e"/>
  </process>
</definitions>`

// TestServerExecutesClioConnector is the end-to-end proof of the clio wiring: a
// deployed clio write task parks (raising an incident) until an operator configures
// the "events" connector in the Console, then a new instance appends to the
// configured clio instance (a fake here) and runs to completion — the clio connector
// worker is driven on the run loop exactly like the temis and DMN workers, and its
// endpoint token is resolved from the connector store.
func TestServerExecutesClioConnector(t *testing.T) {
	var calls int32
	var gotSubject, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Events []struct {
				Source  string `json:"source"`
				Subject string `json:"subject"`
			} `json:"events"`
		}
		_ = json.Unmarshal(b, &req)
		if len(req.Events) > 0 {
			gotSubject = req.Events[0].Subject
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	// The connector's token is a reference resolved from the environment (ADR-0041).
	t.Setenv("ATLAS_CONNECTOR_EVENTS_CRED_TOKEN", "s3cr3t")

	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Clio")
	x.saveDraft(pid, clioWriteBPMN)
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var rep projectDeployResp
	_ = json.Unmarshal(b, &rep)
	key := rep.Definitions[0].Key

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

	// No connector yet → the write task can't run, so a new instance raises an incident.
	runInstance()
	if incidents() == 0 {
		t.Fatal("before configuring the connector: want an incident (clio write can't run)")
	}

	// Configure the "events" connector in the Console → a new instance now appends.
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"`+ts.URL+`","credentialsRef":"events_cred"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	before := incidents()
	callsBefore := atomic.LoadInt32(&calls)
	runInstance()
	if atomic.LoadInt32(&calls) == callsBefore {
		t.Fatal("after configuring the connector: clio was never called")
	}
	if gotSubject != "/orders/new" {
		t.Errorf("event subject = %q, want /orders/new (clio subjects are absolute)", gotSubject)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the token resolved from the connector ref", gotAuth)
	}
	if incidents() != before {
		t.Fatalf("after configuring the connector: incidents changed %d→%d, want the write to execute", before, incidents())
	}
}

// TestBuildClioClients covers the managed-connector → client build: only enabled
// records of kind "clio" with a non-empty endpoint become clients, and the token is
// resolved from the credential reference.
func TestBuildClioClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// Enabled clio record → a client; a disabled one, a temis one, and an
	// endpoint-less one are all skipped.
	_ = srv.connectors.Save(connector{ID: "1", Name: "on", Kind: "clio", Endpoint: "http://a", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: "clio", Endpoint: "http://b", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "temis", Kind: "temis", Endpoint: "http://c", Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "noendpoint", Kind: "clio", Endpoint: "", Enabled: true, CreatedAt: 4})

	clients, err := srv.buildClioClients()
	if err != nil {
		t.Fatalf("buildClioClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1 (only the enabled clio record)", len(clients))
	}
	if _, ok := clients["on"]; !ok {
		t.Errorf("clients = %v, want the 'on' connector", clients)
	}
}

// TestBuildClioClientsLoadError covers buildClioClients' store-read failure.
func TestBuildClioClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, err := srv.buildClioClients(); err == nil {
		t.Error("buildClioClients with a broken store: want error")
	}
}

// TestClioConnectorListNoSecret proves a configured clio connector lists without its
// secret material (only the reference), like every managed connector.
func TestClioConnectorListNoSecret(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	post := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(`{"name":"ev","kind":"clio","endpoint":"http://x","credentialsRef":"ev_cred"}`))
	post.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("create clio connector: %d %s", rec.Code, rec.Body)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v1/connectors", nil)
	lrec := httptest.NewRecorder()
	h.ServeHTTP(lrec, list)
	body := lrec.Body.String()
	if !strings.Contains(body, `"kind":"clio"`) || strings.Contains(body, "token") {
		t.Fatalf("connector list = %s, want the clio record and no secret", body)
	}
}
