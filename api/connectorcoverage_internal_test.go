package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
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

// Every deploy keeps its predecessor, so the deployment registry is the whole
// history, not the current state of the models. Answering coverage over all of it is
// how a name an author corrected two versions ago stays in the card forever, under
// the process's *current* name — which reads as "the model you are looking at is
// broken" about a model that is fine. The next four tests pin which versions count.

// A superseded version nothing is running on cannot create a job, so it has no
// connector left to serve.
func TestASupersededVersionWithNothingRunningIsNotReported(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	deployNotify(t, srv, "office365")      // v1: the name nothing holds
	deployNotify(t, srv, "internal-relay") // v2: corrected
	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["internal-relay"]}`)

	if got := coverage(t, srv); len(got.Unserved) != 0 {
		t.Errorf("unserved = %+v, want none: only the superseded v1 still names office365", got.Unserved)
	}
}

// A superseded version an instance is still running on is reported, at its own
// version. That instance's token reaches the connector task the old model wrote, so
// the gap is real and the version is the part that makes it findable.
func TestASupersededVersionWithALiveInstanceIsReportedAtItsVersion(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	deployNotify(t, srv, "office365")
	startNotify(t, srv, 1) // parks on the mail task: nothing completes it here
	deployNotify(t, srv, "internal-relay")
	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["internal-relay"]}`)

	got := coverage(t, srv)
	if len(got.Unserved) != 1 {
		t.Fatalf("unserved = %+v, want the name the running v1 asks for", got.Unserved)
	}
	u := got.Unserved[0]
	if u.Name != "office365" {
		t.Errorf("name = %q, want office365", u.Name)
	}
	if len(u.Processes) != 1 || u.Processes[0].Version != 1 {
		t.Errorf("processes = %+v, want v1 — the version that names it, not the current one", u.Processes)
	}
}

// An operator can pin a call activity to an exact version (ADR-0105), which makes
// that version the live target of every call to the process id. It is not
// superseded in any sense that matters, so its connectors still have to be served.
func TestAVersionPinnedAsACallTargetIsReported(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	deployNotify(t, srv, "office365")
	deployNotify(t, srv, "internal-relay")
	if err := srv.callOverrides.Save(callOverride{
		CalledProcessID: "notify", Action: overridePin, TargetVersion: 1,
	}); err != nil {
		t.Fatalf("pin v1: %v", err)
	}
	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["internal-relay"]}`)

	got := coverage(t, srv)
	if len(got.Unserved) != 1 || got.Unserved[0].Name != "office365" {
		t.Fatalf("unserved = %+v, want office365: every call to notify resolves to the pinned v1", got.Unserved)
	}
}

// The current version is reported with its version too, so the card never leaves a
// reader guessing which of a process's versions it means.
func TestTheReportCarriesTheVersionThatNamesTheConnector(t *testing.T) {
	srv := mailPullSrv(t)
	pullMail(t, srv, `{"type":"io.atlas.mail.send","worker":"mailer-1","connectors":["internal-relay"]}`)

	got := coverage(t, srv)
	if len(got.Unserved) != 1 || len(got.Unserved[0].Processes) != 1 {
		t.Fatalf("unserved = %+v, want the one model that references it", got.Unserved)
	}
	if p := got.Unserved[0].Processes[0]; p.Version != 1 || p.ProcessDefKey != 1 {
		t.Errorf("process = %+v, want v1 at key 1", p)
	}
}

// The reachability question reads the override sidecar, so a sidecar it cannot read
// has to fail the request rather than quietly answer over the wrong set of versions —
// an unreadable pin would drop a version that is in fact the live call target.
func TestAnUnreadableOverrideSidecarFailsTheWorkersView(t *testing.T) {
	srv := mailPullSrv(t)
	srv.do(func() {
		srv.callOverrides = brokenStore(newCallOverrideStore(filepath.Join(t.TempDir(), "gone")))
	})

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusInternalServerError {
		t.Fatalf("GET /workers: status=%d body=%s, want 500", code, raw)
	}
}

// A connector authored as a FEEL expression (entra, ADR-0172) names no fixed
// connector to check against what workers hold — the tenant is resolved from the
// instance's variables at call time. It must not be listed as unserved (that would be
// a false "= tenant" alarm), while a static name that nothing serves still is.
func TestADynamicConnectorNameIsNotReportedUnserved(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", entraCoverageModel, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	got := coverage(t, srv)
	// entra is worker-only and no worker holds anything here, so the static name is
	// unserved; the dynamic "=tenant" is skipped.
	names := map[string]bool{}
	for _, u := range got.Unserved {
		names[u.Name] = true
	}
	if !names["contoso"] {
		t.Errorf("static entra connector 'contoso' should be reported unserved; got %+v", got.Unserved)
	}
	for n := range names {
		if strings.HasPrefix(n, "=") {
			t.Errorf("a dynamic (FEEL) connector name %q was reported unserved; it should be skipped", n)
		}
	}
}

// entraCoverageModel references the worker-only Entra kind twice: once with a static
// tenant name and once with a FEEL expression for it.
const entraCoverageModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="jml" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t1">
      <bpmn:extensionElements>
        <atlas:entraConnector connector="contoso" operation="list-users" resultVariable="a"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="t2">
      <bpmn:extensionElements>
        <atlas:entraConnector connector="=tenant" operation="list-users" resultVariable="b"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="t2"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t2" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

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
	deployNotify(t, srv, "office365")
	startNotify(t, srv, 1)
}

// deployNotify deploys one version of the notify model naming the given connector.
func deployNotify(t *testing.T, srv *Server, connector string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		fmt.Sprintf(mailCoverageModel, connector), "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
}

// startNotify starts an instance of one definition *by key*, so a test can put a
// live instance on a version that is no longer the current one.
func startNotify(t *testing.T, srv *Server, defKey uint64) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", defKey),
		`{"variables":{"customer":"ada@example.com"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
}

// pollWorker is a poll that need not come back with work: an idle worker still
// reports the connectors it holds, and that is the half the coverage answer needs.
func pollWorker(t *testing.T, srv *Server, body string) {
	t.Helper()
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", body, "application/json"); code != http.StatusOK {
		t.Fatalf("poll: status=%d body=%s", code, raw)
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

// mailCoverageModel is the notify model, parameterized on the connector name so a
// test can deploy a second version that points somewhere else — which is what a
// corrected connector name looks like on a server that keeps every version.
const mailCoverageModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="notify" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="%s" to="=customer" subject="Order shipped" body="On its way."/>
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
