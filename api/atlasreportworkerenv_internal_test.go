package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// The Atlas report Worker Type is worker-only, and a supervised worker can serve it
// because the engine hands its servers over: a task names its Worker and nothing more,
// and the endpoint and the token are a Worker record and a vault secret (ADR-0555 §3).

const atlasReportBundle = `{"token":"atl_report_0123456789"}`

func saveAtlasReportWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindAtlasReport,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its clients from, the bare token the
// API-token page seals into the vault included.
func TestSupervisedAtlasReportEnvBuildsTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveAtlasReportWorker(t, srv, "1", "atlas", "https://atlas.example.ch", "atlas-report")
	saveAtlasReportWorker(t, srv, "2", "zentrale", "https://zentrale.example.ch/atlas", "atlas-zentrale")
	t.Setenv("ATLAS_CONNECTOR_ATLAS_REPORT_TOKEN", atlasReportBundle)
	t.Setenv("ATLAS_CONNECTOR_ATLAS_ZENTRALE_TOKEN", "atl_sealed_bare_token")

	env := envOf(t, srv.atlasReportWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindAtlasReport)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "atlas") || !slices.Contains(built.Names, "zentrale") {
		t.Errorf("the worker holds %v, want both servers", built.Names)
	}
	if got := env["ATLAS_ATLASREPORT_ATLAS_URL"]; got != "https://atlas.example.ch" {
		t.Errorf("the handed endpoint = %q", got)
	}
	if got := env["ATLAS_ATLASREPORT_ZENTRALE_CREDENTIALS"]; got != "atl_sealed_bare_token" {
		t.Errorf("the handed credential = %q, want it as the vault holds it", got)
	}
}

// A server whose endpoint or credential does not build is left out rather than handed
// over, and two names that fold to one environment name hand over only the first.
func TestSupervisedAtlasReportEnvSkipsWhatCannotBeUsed(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, secret string }{
		{"never set", "https://atlas.example.ch", ""},
		{"malformed", "https://atlas.example.ch", "{"},
		{"no token", "https://atlas.example.ch", `{"token":""}`},
		{"not an address", "ftp://atlas.example.ch", atlasReportBundle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveAtlasReportWorker(t, srv, "1", "atlas", tc.endpoint, "atlas")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_ATLAS_TOKEN", tc.secret)
			}
			if env := srv.atlasReportWorkerEnv(); env != nil {
				t.Errorf("rendered %v for a server that cannot be used; want nothing", env)
			}
		})
	}

	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveAtlasReportWorker(t, srv, "1", "atlas-eu", "https://atlas.example.ch", "atlas")
	saveAtlasReportWorker(t, srv, "2", "atlas_eu", "https://atlas.example.ch", "atlas")
	t.Setenv("ATLAS_CONNECTOR_ATLAS_TOKEN", atlasReportBundle)
	t.Setenv("ATLAS_ATLASREPORT_CONNECTORS", "host-atlas")
	env := envOf(t, srv.atlasReportWorkerEnv())
	if got := env["ATLAS_ATLASREPORT_CONNECTORS"]; got != "host-atlas,atlas-eu" {
		t.Errorf("ATLAS_ATLASREPORT_CONNECTORS = %q, want the host's server and only the first of two names that fold together", got)
	}
}

func TestAtlasReportIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindAtlasReport) {
		t.Error("atlasreport is not supervised by default: adding a server in the Console would need a deployment change")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindAtlasReport]; !provisioned {
		t.Error("atlasreport is supervised but its credential is not handed over")
	}
}

func TestValidateAtlasReportWorker(t *testing.T) {
	p := createConnectorParams{CredentialsRef: "atlas"}
	if msg := validateAtlasReportConnector(&p); !strings.Contains(msg, "endpoint") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://atlas.example.ch"}
	if msg := validateAtlasReportConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://atlas.example.ch", CredentialsRef: "atlas", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateAtlasReportConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}

const atlasReportBPMN = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-report">
  <bpmn:error id="err" errorCode="ATLASREPORT_REFUSED"/>
  <bpmn:process id="bericht" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:atlasReportConnector connector="atlas" operation="process-statistics" resultVariable="bericht" input="{&quot;process&quot;: &quot;=prozess&quot;, &quot;buckets&quot;: 4, &quot;bucket&quot;: &quot;week&quot;}"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="err"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

// The leased job carries the evaluated input and the codes the process catches, and
// nothing that says which server or as whom.
func TestTheAtlasReportPayloadCarriesTheEvaluatedInput(t *testing.T) {
	srv, _ := jobPullSrvClockedWith(t, atlasReportBPMN, `{"prozess":"bestellung"}`)
	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"report-worker","leaseMs":60000}`, compiler.AtlasReportJobType))
	if code != http.StatusOK || len(got.Jobs) != 1 || got.Jobs[0].Connector == nil {
		t.Fatalf("pull: status=%d jobs=%+v", code, got.Jobs)
	}
	c := got.Jobs[0].Connector
	if c.Kind != "atlasreport" || c.Fields["connector"] != "atlas" || c.Fields["operation"] != "process-statistics" ||
		c.Fields["process"] != "bestellung" || c.Fields["bucket"] != "week" || c.Fields["resultVariable"] != "bericht" {
		t.Errorf("payload = %s %v", c.Kind, c.Fields)
	}
	if n, ok := c.Fields["buckets"].(float64); !ok || n != 4 {
		t.Errorf("buckets = %v", c.Fields["buckets"])
	}
	if codes, _ := c.Fields["errorCodes"].([]any); !slices.Contains(codes, any("ATLASREPORT_REFUSED")) {
		t.Errorf("errorCodes = %v, want the code the boundary catches", c.Fields["errorCodes"])
	}
	for _, secret := range []string{"token", "endpoint", "credentialsRef"} {
		if _, ok := c.Fields[secret]; ok {
			t.Errorf("the payload carries %q; the server's address and token stay on the worker", secret)
		}
	}
}
