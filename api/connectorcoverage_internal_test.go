package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The Workers view's connector coverage: which connector names deployed models
// reference, and whether anything can actually serve them.
//
// Moving a credential onto a worker (ADR-0168) created a failure the engine can no
// longer catch on its own. It used to refuse an unconfigured connector at lease
// time, because it held every credential. Once a kind is offloaded it does not, so
// the only way an operator learns that a name is configured *nowhere* is a job that
// fails at a worker an hour later. These tests pin the answer arriving before that.

type coverageResp struct {
	Workers []struct {
		Worker     string   `json:"worker"`
		Connectors []string `json:"connectors"`
	} `json:"workers"`
	Unserved []struct {
		Name      string     `json:"name"`
		JobType   string     `json:"jobType"`
		Processes []typeUser `json:"processes"`
	} `json:"unservedConnectors"`
}

func coverage(t *testing.T, srv *Server) coverageResp {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var out coverageResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode workers: %v (%s)", err, raw)
	}
	return out
}

// A worker says which connector names it holds credentials for, and the view shows
// them. This is the half only the worker knows — the engine cannot read another
// process's environment.
func TestAWorkerReportsTheConnectorNamesItHolds(t *testing.T) {
	srv := mailPullSrv(t)
	pullMail(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["office365","internal-relay"]}`)

	got := coverage(t, srv)
	if len(got.Workers) != 1 {
		t.Fatalf("workers = %d, want 1", len(got.Workers))
	}
	names := got.Workers[0].Connectors
	if len(names) != 2 || names[0] != "internal-relay" || names[1] != "office365" {
		t.Errorf("connectors = %v, want both names in a stable order", names)
	}
}

// The payoff: a connector a model references that no worker holds is named, with the
// processes that use it, so an operator can go and configure it somewhere.
func TestTheViewNamesAConnectorNoWorkerHolds(t *testing.T) {
	srv := mailPullSrv(t)
	// A worker that serves mail, but not the provider this model asks for.
	pullMail(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["internal-relay"]}`)

	got := coverage(t, srv)
	if len(got.Unserved) != 1 {
		t.Fatalf("unserved connectors = %+v, want the one nothing can send through", got.Unserved)
	}
	u := got.Unserved[0]
	if u.Name != "office365" {
		t.Errorf("name = %q, want office365", u.Name)
	}
	if u.JobType != "io.atlas.mail.send" {
		t.Errorf("job type = %q, want the kind it belongs to", u.JobType)
	}
	if len(u.Processes) != 1 || u.Processes[0].ProcessID != "notify" {
		t.Errorf("processes = %+v, want the model that references it", u.Processes)
	}
}

// A connector a worker does hold is not reported as missing — otherwise the list
// would be noise and an operator would stop reading it.
func TestAConnectorAWorkerHoldsIsNotReportedMissing(t *testing.T) {
	srv := mailPullSrv(t)
	pullMail(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["office365"]}`)

	if got := coverage(t, srv); len(got.Unserved) != 0 {
		t.Errorf("unserved = %+v, want none: a worker holds that connector", got.Unserved)
	}
}

// A kind the engine still serves itself is not reported either. The engine holds
// every credential for a kind it has not offloaded, and ADR-0163's own machinery
// already answers for those — reporting them here would say "missing" about a
// connector that works.
func TestAKindTheEngineStillServesIsNotReportedMissing(t *testing.T) {
	// No offload: mail runs in the engine, as it does by default.
	srv, _ := newValidateServer(t)
	deployMailModel(t, srv)

	if got := coverage(t, srv); len(got.Unserved) != 0 {
		t.Errorf("unserved = %+v, want none: the engine serves mail itself here", got.Unserved)
	}
}

// mailPullSrv is a server that has offloaded mail and has the notify model deployed,
// so its one mail connector task is leasable by an external worker.
func mailPullSrv(t *testing.T) *Server {
	t.Helper()
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	deployMailModel(t, srv)
	return srv
}

func deployMailModel(t *testing.T, srv *Server) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", mailCoverageModel, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"customer":"ada@example.com"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
}

func pullMail(t *testing.T, srv *Server, body string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	if len(out.Jobs) == 0 {
		t.Fatal(fmt.Sprintf("the pull returned no mail job; body=%s", raw))
	}
}

const mailCoverageModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="notify" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="office365" to="=customer" subject="Order shipped" body="On its way."/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestIsOffloadableKindTellsInProcessKindsFromWorkerOnlyOnes pins the distinction a
// caller asking for a supervised worker depends on: offloading is the removal of
// in-process handlers, so a kind that has none cannot be named in the offload list
// and must not be pushed into it just because someone wants a worker for it.
func TestIsOffloadableKindTellsInProcessKindsFromWorkerOnlyOnes(t *testing.T) {
	for _, kind := range []string{"ad", "mail", "script", "webscrape"} {
		if !IsOffloadableKind(kind) {
			t.Errorf("IsOffloadableKind(%q) = false, want true: the engine handles it today", kind)
		}
	}
	for _, kind := range []string{"entra", "nonsense"} {
		if IsOffloadableKind(kind) {
			t.Errorf("IsOffloadableKind(%q) = true, want false: there is nothing in process to remove", kind)
		}
	}
}
