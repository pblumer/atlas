package worker_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/remedy"
	remedymock "github.com/pblumer/atlas/connector/remedy/mock"
	"github.com/pblumer/atlas/worker"
)

// TestRemedyWorkerHoldsItsOwnCredential is ADR-0168's decision seen from the worker's
// side: the AR System base URL and the service account come from *this process's*
// environment, and a leased job contributes only a worker name. A worker can
// therefore file tickets in a Helix instance the engine has no configuration for —
// and, more to the point for an ITSM system, one the engine cannot reach at all.
func TestRemedyWorkerHoldsItsOwnCredential(t *testing.T) {
	env := fakeEnv(map[string]string{
		"ATLAS_REMEDY_CONNECTORS":     "helix",
		"ATLAS_REMEDY_HELIX_ENDPOINT": "https://helix.example.com:8008",
		"ATLAS_REMEDY_HELIX_USERNAME": "atlas-svc",
		"ATLAS_REMEDY_HELIX_PASSWORD": "hunter2",
	})
	built, err := worker.BuiltinConnectors(env, "remedy")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.RemedyJobType]; !ok {
		t.Fatalf("no handler for %s; got %v", compiler.RemedyJobType, built.Handlers)
	}
	// The names are reported to the engine on every poll: the Workers view subtracts
	// them from what deployed models reference, so a name nobody holds is visible.
	if len(built.Names) != 1 || built.Names[0] != "helix" {
		t.Errorf("names = %v, want the configured instance", built.Names)
	}
}

// A worker told to serve Remedy but given no instance must not lease every Remedy job
// and fail it. It does not subscribe at all, so those tasks wait for a worker that can
// actually file them — and it says so, because "remedy is not served here" is the
// answer to why a ticket task is waiting.
func TestARemedyWorkerWithNoConfiguredInstanceSimplyDoesNotServeRemedy(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(nil), "csv", "remedy")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.RemedyJobType]; ok {
		t.Error("the worker subscribed to remedy with no AR System to file against")
	}
	if _, ok := built.Handlers[compiler.CsvImportJobType]; !ok {
		t.Error("the kinds it *can* serve were dropped along with remedy")
	}
	if len(built.Unconfigured) != 1 || built.Unconfigured[0] != "remedy" {
		t.Errorf("unconfigured = %v, want [remedy] so the startup line can say it", built.Unconfigured)
	}
}

// A *named* instance missing a field is a mistake to report at startup, where the
// operator is still watching — not one to discover a retry budget later, per job.
func TestARemedyInstanceMissingAFieldIsRefusedAtStartup(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_REMEDY_CONNECTORS":     "helix",
		"ATLAS_REMEDY_HELIX_ENDPOINT": "https://helix.example.com:8008",
		"ATLAS_REMEDY_HELIX_USERNAME": "atlas-svc",
		// no password
	}), "remedy")
	if err == nil {
		t.Fatal("a remedy instance with no password was accepted")
	}
	// The error quotes the variable to set, so an operator sets exactly the one that
	// was looked for rather than guessing the folding.
	if !strings.Contains(err.Error(), "ATLAS_REMEDY_HELIX_PASSWORD") {
		t.Errorf("error = %v, want it to name the variable to set", err)
	}
}

// The whole path a supervised or external Remedy worker takes: its environment builds
// the client, a leased job carries only the resolved payload, and the entry lands in
// the AR System with the model's form and fields. The mock is the real thing's REST
// API (ADR-0106), so the client under test is the one that talks to Helix.
func TestARemedyWorkerFilesTheEntryFromItsOwnEnvironment(t *testing.T) {
	mock := remedymock.New(remedymock.WithCredentials("atlas-svc", "hunter2"))
	ar := httptest.NewServer(mock.Handler())
	defer ar.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_REMEDY_CONNECTORS":     "helix",
		"ATLAS_REMEDY_HELIX_ENDPOINT": ar.URL,
		"ATLAS_REMEDY_HELIX_USERNAME": "atlas-svc",
		"ATLAS_REMEDY_HELIX_PASSWORD": "hunter2",
	}), "remedy")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	exec, ok := built.Handlers[compiler.RemedyJobType]
	if !ok {
		t.Fatalf("no handler for %s", compiler.RemedyJobType)
	}

	vars, err := exec.Run(context.Background(), worker.Job{
		JobKey: 4711, Type: compiler.RemedyJobType,
		Connector: &worker.ConnectorPayload{Kind: "remedy", Fields: map[string]any{
			"connector": "helix",
			"form":      "HPD:IncidentInterface_Create",
			"values": map[string]any{
				"Description": "Disk full",
				"Impact":      "2-Significant",
			},
			"requestId":      "4711",
			"resultVariable": "incidentNumber",
		}},
	})
	if err != nil {
		t.Fatalf("run the remedy job: %v", err)
	}

	entries := mock.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries created = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Form != "HPD:IncidentInterface_Create" || e.Values["Description"] != "Disk full" || e.Values["Impact"] != "2-Significant" {
		t.Errorf("entry = %+v, want the authored form and fields", e)
	}
	if e.RequestID != "4711" {
		t.Errorf("X-Request-ID = %q, want the job key so an at-least-once replay is recognizable", e.RequestID)
	}
	// The job completes with the created entry's id in the model's result variable,
	// which is how the token carries the ticket number onward.
	if vars["incidentNumber"] != e.ID {
		t.Errorf("completed with %v, want the created entry id %q", vars, e.ID)
	}
}

// A model that discards the entry id completes with no variables at all, rather than
// writing an empty one nobody asked for.
func TestARemedyJobWithNoResultVariableCompletesWithNothing(t *testing.T) {
	mock := remedymock.New()
	ar := httptest.NewServer(mock.Handler())
	defer ar.Close()

	reg := remedy.NewRegistry()
	reg.Register("helix", remedy.NewHTTPClient(remedy.Connector{BaseURL: ar.URL, Username: "u", Password: "p"}))

	vars, err := worker.RunRemedyJob(context.Background(), worker.Job{
		Connector: &worker.ConnectorPayload{Kind: "remedy", Fields: map[string]any{
			"connector": "helix", "form": "HPD:Help Desk",
		}},
	}, reg)
	if err != nil {
		t.Fatalf("RunRemedyJob: %v", err)
	}
	if vars != nil {
		t.Errorf("completed with %v, want nothing written back", vars)
	}
	if len(mock.Entries()) != 1 {
		t.Errorf("entries created = %d, want the entry to have been filed anyway", len(mock.Entries()))
	}
}

// A Remedy job that arrives unresolved is a server that still runs the kind itself —
// so the error says that, rather than failing on an empty form.
func TestARemedyJobWithNoResolvedDetailSaysSo(t *testing.T) {
	_, err := worker.RunRemedyJob(context.Background(), worker.Job{}, remedy.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "offloading the remedy kind") {
		t.Errorf("error = %v, want one pointing at the server's offload configuration", err)
	}
}

// TestWorkerRunsAnOffloadedRemedyConnector is the move in one test: a real Atlas with
// the Remedy kind taken off the engine, a real worker leasing over the job protocol,
// and the ticket landing in an AR System only the worker can reach.
//
// It pins both halves of ADR-0168 at once. The engine resolved the task — the form and
// the fields, one of them out of a FEEL expression over a process variable — because
// only it can; the worker held the base URL and the service account, because only it
// should. Nothing about Remedy ran on the run loop, and the entry id still came back
// into the instance as the model's result variable.
func TestWorkerRunsAnOffloadedRemedyConnector(t *testing.T) {
	mock := remedymock.New(remedymock.WithCredentials("atlas-svc", "hunter2"))
	ar := httptest.NewServer(mock.Handler())
	defer ar.Close()

	ts := liveAtlasWith(t, remedyConnectorModel, `{"summary":"Disk full on server 7"}`,
		api.WithOffloadedConnectorKinds([]string{"remedy"}))

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_REMEDY_CONNECTORS":     "helix",
		"ATLAS_REMEDY_HELIX_ENDPOINT": ar.URL,
		"ATLAS_REMEDY_HELIX_USERNAME": "atlas-svc",
		"ATLAS_REMEDY_HELIX_PASSWORD": "hunter2",
	}), "remedy")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := worker.New(worker.Options{Server: ts.URL, ID: "remedy-1", Handlers: built.Handlers})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the Remedy job was not completed", running)
	}
	entries := mock.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries created = %d, want the one the model authored", len(entries))
	}
	e := entries[0]
	if e.Form != "HPD:IncidentInterface_Create" {
		t.Errorf("form = %q, want the incident form", e.Form)
	}
	// The FEEL field was evaluated by the engine, against the instance's variables,
	// before the job ever left it.
	if e.Values["Description"] != "Disk full on server 7" {
		t.Errorf("Description = %v, want the FEEL field resolved from the instance", e.Values["Description"])
	}
	if e.Values["Impact"] != "2-Significant" {
		t.Errorf("Impact = %v, want the literal field", e.Values["Impact"])
	}
	if vars := instanceVariables(t, ts); vars["incidentNumber"] != e.ID {
		t.Errorf("incidentNumber = %v, want the created entry id %q", vars["incidentNumber"], e.ID)
	}
}

const remedyConnectorModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="ticketing" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:remedyConnector connector="helix" form="HPD:IncidentInterface_Create" resultVariable="incidentNumber">
          <atlas:remedyField name="Description" value="=summary"/>
          <atlas:remedyField name="Impact" value="2-Significant"/>
        </atlas:remedyConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
