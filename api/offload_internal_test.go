package api

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestOffloadedKindIsLeasableByAWorker is the prerequisite ADR-0168 exposed: a
// Worker Type cannot move to a worker while an in-process handler serves it,
// because the pull refuses such a type — that refusal is what keeps work from being
// done twice. Turning the handler off is therefore the operative act of relocating
// a kind, and it has to be something an operator can do.
func TestOffloadedKindIsLeasableByAWorker(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindMail}))

	// Served in-process by default; refused to a worker, and marked in the view.
	plain := newServerForErrors(t)
	if code, _ := pull(t, plain, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.MailJobType)); code != http.StatusConflict {
		t.Errorf("pull of a served kind: status=%d, want 409", code)
	}

	// Offloaded: no in-process handler, so a worker may lease it. The queue is empty,
	// which is a 200 with no jobs rather than the 409 a served kind gives.
	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.MailJobType))
	if code != http.StatusOK {
		t.Fatalf("pull of an offloaded kind: status=%d, want 200", code)
	}
	if len(got.Jobs) != 0 {
		t.Errorf("pulled %d jobs from an empty queue", len(got.Jobs))
	}
}

// The Workers view must report the change, or an operator has no way to see which
// kinds this server still runs itself.
func TestOffloadedKindIsReportedAsLeasable(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindMail}))
	for _, row := range workers(t, srv).Types {
		if row.Type != compiler.MailJobType {
			continue
		}
		if row.ServedInProcess {
			t.Error("an offloaded kind still reports as served in-process")
		}
		if !row.Leasable {
			t.Error("an offloaded kind is not reported as leasable, so nothing can take it")
		}
		return
	}
	t.Fatal("no row for the mail job type")
}

// An unknown kind name is a startup error rather than a silent no-op: an operator
// who misspells one would otherwise believe a kind was relocated when it was not,
// and the work would keep running in the engine.
func TestOffloadingAnUnknownKindFails(t *testing.T) {
	if _, err := newServerWithOptionsErr(t, WithOffloadedConnectorKinds([]string{"no-such-kind"})); err == nil {
		t.Error("offloading an unknown Worker Type succeeded, want an error naming it")
	}
}

// Every kind can be named, so the flag cannot be a partial list that quietly omits
// one and leaves it running in the engine.
func TestEveryManagedKindCanBeOffloaded(t *testing.T) {
	names := offloadableKindNames()
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(names))
	srv.do(func() {
		for name, types := range offloadableKinds {
			for _, jt := range types {
				if srv.jobRunner.Handles(jt) {
					t.Errorf("%s still has an in-process handler after every kind was offloaded", name)
				}
			}
		}
	})
}

// TestEveryInProcessHandlerIsOffloadable is the guard the SOAP worker needed and
// did not have. TestEveryManagedKindCanBeOffloaded above walks offloadableKinds and
// checks each listed kind goes away, so it says nothing at all about a job type the
// map never mentions: a worker added with a handler but no entry passes it
// silently, and the kind is then one an operator can neither name nor relocate.
//
// This walks the other direction — from the handlers a booted server actually
// registered back to the map — so the omission fails here instead.
func TestEveryInProcessHandlerIsOffloadable(t *testing.T) {
	// Every optional registration path on, so the script and user-provisioning
	// handlers are present too and not silently skipped.
	srv := newServerWithOptions(t,
		WithUserProvisioning(),
		WithScriptWorker(compiler.PwshJobTypeIndex, nil),
		WithScriptWorker(compiler.PythonJobTypeIndex, nil),
		WithScriptWorker(compiler.JsJobTypeIndex, nil),
	)

	named := map[int32]string{}
	for name, types := range offloadableKinds {
		for _, jt := range types {
			named[jt] = name
		}
	}
	// The user-provisioning worker is deliberately absent from the map: it mutates
	// the run-loop-owned user store directly (ADR-0123), so there is nothing for a
	// worker to hold and no endpoint for it to reach. Every other handler must be
	// namable. The exception is recorded once, in engineOnlyJobTypes, because the
	// Modeler's placement badge needs the same fact — a kind with no out-of-process
	// form must not be advised to move to one.
	engineOnly := engineOnlyJobTypes

	reserved := compiler.ReservedJobTypes()
	srv.do(func() {
		for i, name := range reserved {
			jt := int32(i)
			if !srv.jobRunner.Handles(jt) {
				continue
			}
			if _, ok := named[jt]; ok {
				continue
			}
			if why, ok := engineOnly[jt]; ok {
				t.Logf("%s runs in the engine on purpose: %s", name, why)
				continue
			}
			t.Errorf("%s has an in-process handler but no --offload-connectors kind names it, so an operator cannot move it to a worker (ADR-0164). Add it to offloadableKinds.", name)
		}
	})
}

// A blank entry is skipped rather than refused. `--offload-connectors csv,,rest` and
// a trailing comma are what an operator's shell history produces; failing startup on
// one would be strictness with no safety in it — unlike a *misspelled* kind, a blank
// cannot be mistaken for a relocation that happened.
func TestOffloadingIgnoresBlankEntries(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{"csv", "", "  ", "rest"}))
	srv.do(func() {
		for _, jt := range []int32{compiler.CsvImportJobTypeIndex, compiler.RestJobTypeIndex} {
			if srv.jobRunner.Handles(jt) {
				t.Errorf("job type %d still has an in-process handler; the blank entries broke the list", jt)
			}
		}
	})
}

// TestEveryDefaultOffloadedKindCanBeServedByItsWorker is the safety property behind
// making the default opt-out. A supervised worker inherits this process's environment
// but not its worker store, so a managed kind — whose endpoint and password live
// in that store — can only be defaulted if the engine hands that configuration to the
// child at spawn. Default a managed kind the engine does not provision and every task
// of it fails on a worker nobody configured, which is the one outcome the opt-out
// default must never produce.
func TestEveryDefaultOffloadedKindCanBeServedByItsWorker(t *testing.T) {
	managed := map[string]bool{}
	for _, k := range managedConnectorKinds {
		managed[k.name] = true
	}
	provisioned := (&Server{}).provisionedConnectorKinds()
	for _, kind := range DefaultOffloadedKinds() {
		if _, ok := offloadableKinds[kind]; !ok {
			t.Errorf("default kind %q is not offloadable at all", kind)
		}
		if _, handed := provisioned[kind]; managed[kind] && !handed {
			t.Errorf("default kind %q is managed but not provisioned: its credentials live in the "+
				"worker store, which a supervised worker cannot read and is not handed", kind)
		}
	}
}

// TestEveryDefaultSupervisedWorkerOnlyKindCanBeServed is the same safety property for
// the worker-only defaults (ADR-0172): a kind Atlas always supervises must be a managed
// kind marked worker-only and provisioned by the engine, or the default would run a
// worker with nothing to serve, forever.
func TestEveryDefaultSupervisedWorkerOnlyKindCanBeServed(t *testing.T) {
	byName := map[string]managedConnectorKind{}
	for _, k := range managedConnectorKinds {
		byName[k.name] = k
	}
	provisioned := (&Server{}).provisionedConnectorKinds()
	for _, kind := range DefaultSupervisedWorkerOnlyKinds() {
		k, ok := byName[kind]
		if !ok {
			t.Errorf("worker-only default %q is not a managed Worker Type", kind)
			continue
		}
		if !k.workerOnly {
			t.Errorf("worker-only default %q is not marked workerOnly", kind)
		}
		if _, handed := provisioned[kind]; !handed {
			t.Errorf("worker-only default %q is supervised but not provisioned: the worker would "+
				"start with nothing to serve and park forever", kind)
		}
	}
}

// Active Directory is offloaded by default, and the reason it can be is that the
// engine hands its bind passwords over (ADR-0182).
//
// AD is not a managed kind, so the property above does not cover it: it holds no
// worker record, and its secret is a per-task *reference* the model authors. That
// reference resolves out of the vault, which a supervised worker cannot read any more
// than it can read the worker store — so defaulting AD without provisioning it
// would move every vault-backed directory task to a worker with nothing to bind with,
// which is the same outcome the property above exists to prevent, reached by a
// different route.
func TestActiveDirectoryIsOffloadedByDefaultAndItsBindSecretsAreHandedOver(t *testing.T) {
	if !slices.Contains(DefaultOffloadedKinds(), "ad") {
		t.Error("ad is not offloaded by default; a directory task runs on the engine's own loop")
	}
	if _, handed := (&Server{}).provisionedConnectorKinds()["ad"]; !handed {
		t.Error("ad is defaulted but its bind secrets are not handed to the supervised worker; " +
			"every vault-backed AD task would fail on a worker nobody configured")
	}
}

// BMC Remedy is offloaded by default (ADR-0192), and unlike AD
// above it *is* a managed kind — so the property two tests up already proves a
// supervised worker can serve it. What that property cannot say is that the kind is in
// the default set at all, which is the whole of this decision: an ITSM create is three
// round trips to somebody else's host, and leaving it opt-in is what kept it on the
// engine's loop.
//
// The in-process handler stays, deliberately: --in-process-connectors must still return
// the old arrangement, so a Remedy job type an operator opted out of is served here
// again rather than by nobody.
func TestRemedyIsOffloadedByDefaultAndKeepsItsInProcessFallback(t *testing.T) {
	if !slices.Contains(DefaultOffloadedKinds(), connectorKindRemedy) {
		t.Error("remedy is not offloaded by default; a ticket create runs on the engine's own loop")
	}
	if _, handed := (&Server{}).provisionedConnectorKinds()[connectorKindRemedy]; !handed {
		t.Error("remedy is defaulted but its endpoint and service account are not handed to the " +
			"supervised worker; every Remedy task would park on a worker holding no instance")
	}
	// Opting out puts it back in the engine, which is what makes the default reversible.
	srv := newServerWithOptions(t)
	srv.do(func() {
		if !srv.jobRunner.Handles(compiler.RemedyJobTypeIndex) {
			t.Error("a server that was not told to offload remedy has no in-process handler for it, " +
				"so --in-process-connectors would leave the kind served by nobody")
		}
	})
}

// Jira is offloaded by default (ADR-0218), which is only safe
// because the engine hands the supervised worker the site and the credential it cannot
// read for itself — the same condition mail and Remedy are defaulted on. Defaulting it
// without that would move every Jira task to a worker with no site to file against.
//
// The in-process handler stays, because a default that cannot be turned back is not a
// default: --in-process-connectors jira is the way back.
func TestJiraIsOffloadedByDefaultAndKeepsItsInProcessFallback(t *testing.T) {
	if !slices.Contains(DefaultOffloadedKinds(), connectorKindJira) {
		t.Error("jira is not offloaded by default; its work runs on the engine's own loop, " +
			"and the Workers view shows nobody doing it")
	}
	if _, handed := (&Server{}).provisionedConnectorKinds()[connectorKindJira]; !handed {
		t.Error("jira is defaulted but its site and credential are not handed to the " +
			"supervised worker; every Jira task would park on a worker holding no instance")
	}
	srv := newServerWithOptions(t)
	srv.do(func() {
		if !srv.jobRunner.Handles(compiler.JiraJobTypeIndex) {
			t.Error("a server that was not told to offload jira has no in-process handler for it, " +
				"so --in-process-connectors would leave the kind served by nobody")
		}
	})
}

// TestPullingARemedyJobResolvesTheTaskAndCarriesNoCredential is the engine's half of
// ADR-0106's move onto a worker, checked where the engine hands the work over.
//
// What a leased Remedy job carries is the *resolved* task: the worker's name, the
// form, the field values with their FEEL already evaluated against the instance, and
// the job key as the X-Request-ID an at-least-once replay repeats. What it does not
// carry — and has nowhere to put — is the AR System's base URL or its service account.
// That is the whole of ADR-0168's split, and it is only true on this side of it: the
// worker's own tests prove it can act on this payload, not that this is what it gets.
func TestPullingARemedyJobResolvesTheTaskAndCarriesNoCredential(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindRemedy}))
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", remedyPullBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"summary":"Disk full on server 7"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}

	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.RemedyJobType))
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want the one Remedy task", len(got.Jobs))
	}
	j := got.Jobs[0]
	if j.Connector == nil {
		t.Fatal("the leased job carries no resolved worker detail; the worker would have nothing to file")
	}
	if j.Connector.Kind != connectorKindRemedy {
		t.Errorf("kind = %q, want %q", j.Connector.Kind, connectorKindRemedy)
	}
	f := j.Connector.Fields
	if f["connector"] != "helix" {
		t.Errorf("worker = %v, want the name the model authored", f["connector"])
	}
	if f["form"] != "HPD:IncidentInterface_Create" {
		t.Errorf("form = %v, want the authored form", f["form"])
	}
	if f["resultVariable"] != "incidentNumber" {
		t.Errorf("resultVariable = %v, want the model's result variable", f["resultVariable"])
	}
	if f["requestId"] != strconv.FormatUint(j.JobKey, 10) {
		t.Errorf("requestId = %v, want the job key %d, which is what makes a replay recognizable", f["requestId"], j.JobKey)
	}
	values, ok := f["values"].(map[string]any)
	if !ok {
		t.Fatalf("values = %#v, want the entry's field values", f["values"])
	}
	// The FEEL field was evaluated by the engine, against this instance's variables,
	// before the job was handed out — a worker has neither the compiled process nor
	// the scope chain to do it itself.
	if values["Description"] != "Disk full on server 7" {
		t.Errorf("Description = %v, want the FEEL field resolved from the instance", values["Description"])
	}
	if values["Impact"] != "2-Significant" {
		t.Errorf("Impact = %v, want the literal field", values["Impact"])
	}
}

// remedyPullBPMN is a Remedy task with one literal field and one FEEL field,
// so a resolved payload shows both halves of what the engine evaluates.
const remedyPullBPMN = `<?xml version="1.0" encoding="UTF-8"?>
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

// TestPullingAJiraJobResolvesTheTaskAndCarriesNoCredential is the engine's half of
// giving Jira a worker at all (ADR-0201/0203), checked where the engine hands the work
// over.
//
// What a leased Jira job carries is the *resolved* task: the worker's name, the
// operation, every authored value with its FEEL already evaluated against the instance,
// and the job key as the X-Request-ID an at-least-once replay repeats. What it does not
// carry — and has nowhere to put — is the site URL, an email or an API token. Before
// this the kind had no worker at all: `offloadableKinds` advertised it as movable while
// `worker/connectors.go` had no case for it, so --offload-connectors jira stripped the
// in-process handler and left the job type served by nobody.
func TestPullingAJiraJobResolvesTheTaskAndCarriesNoCredential(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindJira}))
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", jiraPullBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"betreff":"Zugang für Rechnungswesen"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}

	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.JiraJobType))
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want the one Jira task", len(got.Jobs))
	}
	j := got.Jobs[0]
	if j.Connector == nil {
		t.Fatal("the leased job carries no resolved worker detail; the worker would have nothing to perform")
	}
	if j.Connector.Kind != connectorKindJira {
		t.Errorf("kind = %q, want %q", j.Connector.Kind, connectorKindJira)
	}
	f := j.Connector.Fields
	for _, tc := range []struct{ key, want string }{
		{"connector", "acme"},
		{"operation", "create-issue"},
		{"project", "OPS"},
		{"issueType", "Task"},
		// The FEEL summary was evaluated by the engine, against this instance's
		// variables, before the job was handed out — a worker has neither the compiled
		// process nor the scope chain to do it itself.
		{"summary", "Zugang für Rechnungswesen"},
		{"resultVariable", "ticket"},
	} {
		if f[tc.key] != tc.want {
			t.Errorf("%s = %v, want %q", tc.key, f[tc.key], tc.want)
		}
	}
	if f["requestId"] != strconv.FormatUint(j.JobKey, 10) {
		t.Errorf("requestId = %v, want the job key %d, which is what makes a replay recognizable", f["requestId"], j.JobKey)
	}
	// There is nowhere in the payload for a credential, and nothing that resembles one.
	for _, forbidden := range []string{"url", "endpoint", "email", "apiToken", "token", "password"} {
		if _, present := f[forbidden]; present {
			t.Errorf("the resolved payload carries %q; a worker's credential must come from its own environment", forbidden)
		}
	}
}

// jiraPullBPMN is a Jira task with literal values and one FEEL value, so a
// resolved payload shows both halves of what the engine evaluates.
const jiraPullBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="ticketing" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:jiraConnector connector="acme" operation="create-issue" project="OPS"
                             issueType="Task" summary="=betreff" resultVariable="ticket"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// Every managed Worker Type is now handed to its supervised worker, and this holds
// that it stays that way.
//
// It used to assert the opposite: that *some* managed kind was still unprovisioned, so
// that the check above could not quietly become a tautology. Google Sheets was the last
// one, and with it the world that test was written for ended — which is the moment to
// invert it rather than delete it, because the property worth guarding changed
// direction rather than disappearing.
//
// What it catches now is the mistake that made this inversion necessary. A managed kind
// added without a provisionedConnectorKinds entry cannot be defaulted onto a worker: it
// keeps an in-engine handler, the Modeler's placement badge says so, and ADR-0164's
// "a connector task belongs on a worker" quietly acquires an exception nobody decided
// on. That is not visible in a diff — it is visible only as a badge in a properties
// panel, which is where it was found.
func TestEveryManagedKindIsProvisioned(t *testing.T) {
	provisioned := (&Server{}).provisionedConnectorKinds()
	for _, k := range managedConnectorKinds {
		if _, handed := provisioned[k.name]; !handed {
			t.Errorf("managed kind %q is not handed to a supervised worker: add a %sWorkerEnv "+
				"to provisionedConnectorKinds, or it can never leave the engine's run loop", k.name, k.name)
		}
	}
}
