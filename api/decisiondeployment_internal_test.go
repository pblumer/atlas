package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The acceptance suite for ADR-0319: a DMN
// decision is a durable, versioned runtime artifact of its own, an application
// made of nothing but decisions publishes, and a process's `latest` reference is
// frozen when the process is deployed rather than chosen when a token arrives.
//
// Everything here drives the public HTTP surface and, where the point is recovery,
// tears the whole stack down and boots a new one over the same data directory —
// which is the only way to prove that what came back was persisted rather than
// remembered.

// eligibilityDMN is the decision the issue's scenario is written around: an amount
// decides an outcome. The verdict for the qualifying branch is templated so a
// second version of the *same* decision id can be published and told apart by what
// it answers.
func eligibilityDMN(verdict string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_eligibility" name="Eligibility" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"/>
  <decision id="eligibility" name="eligibility">
    <informationRequirement><requiredInput href="#id_amount"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1" label="amount"><inputExpression id="ie1" typeRef="number"><text>amount</text></inputExpression></input>
      <output id="out1" label="eligibility" name="eligibility" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 100</text></inputEntry><outputEntry id="o1"><text>"` + verdict + `"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e2"><text>&lt; 100</text></inputEntry><outputEntry id="o2"><text>"reject"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`
}

// discountDMN is a second, unrelated decision, for the case of an application that
// publishes more than one.
const discountDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_discount" name="Discount" namespace="http://atlas/dmn">
  <decision id="discount" name="discount">
    <literalExpression><text>0.1</text></literalExpression>
  </decision>
</definitions>`

// eligibilityProcess is a business rule task calling the "eligibility" decision
// with amount 250, parked afterwards at a user task so the result variable can be
// read off the live instance.
func eligibilityProcess(processID, binding string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <calledDecision decisionId="eligibility" resultVariable="verdict" bindingType="` + binding + `"/>
        <decisionInput name="amount" value="250"/>
      </extensionElements>
    </businessRuleTask>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`
}

// decisionStack is one boot of the whole stack over a fixed data directory, so a
// test can shut it down and boot another over the same directory — a restart.
type decisionStack struct {
	srv   *Server
	x     deployTestHarness
	store *state.Store
	log   *wal.Log
}

func bootDecisionStack(t *testing.T, dir string) *decisionStack {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return &decisionStack{srv: srv, x: deployTestHarness{t, srv.Handler()}, store: store, log: log}
}

func (s *decisionStack) shutdown() {
	s.srv.Close()
	_ = s.store.Close()
	_ = s.log.Close()
}

// publishDecisionApp uploads a DMN model, references it from a fresh application
// and publishes that application — the whole "author a decision and ship it" path
// with no BPMN anywhere in it. It returns the application id and the publish
// report.
func publishDecisionApp(t *testing.T, x deployTestHarness, appName, handle, dmnXML string) (string, publishResp) {
	t.Helper()
	appID := x.mkProject(appName)
	uploadModel(t, x, handle, dmnXML)
	x.addRef(appID, appName, handle)
	return appID, publishApp(t, x, appID)
}

// uploadModel stores a DMN model under an exact handle, which is also how the
// embedded editor saves an edit to a model a reference already points at.
func uploadModel(t *testing.T, x deployTestHarness, handle, dmnXML string) {
	t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/dmn-models?name="+handle+"&handle="+handle, dmnXML)
	if code != http.StatusOK {
		t.Fatalf("upload model %s: %d %s", handle, code, b)
	}
}

func publishApp(t *testing.T, x deployTestHarness, appID string) publishResp {
	t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/applications/"+appID+"/publish", "")
	if code != http.StatusOK {
		t.Fatalf("publish %s: %d %s", appID, code, b)
	}
	var rep publishResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, b)
	}
	if !rep.Deployed {
		t.Fatalf("publish reported not deployed: %s", b)
	}
	return rep
}

// deployProcess deploys a BPMN model through the single-model route and returns
// its definition key.
func deployProcess(t *testing.T, x deployTestHarness, bpmn string) uint64 {
	t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/deployments", bpmn)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(b, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	return dep.Key
}

// runAndReadVerdict starts an instance of a definition, lets the DMN worker run,
// and reads the "verdict" variable the decision wrote — which version answered,
// observed from the outside.
func runAndReadVerdict(t *testing.T, x deployTestHarness, key uint64, processID string) string {
	t.Helper()
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, b)
	}
	_, ib := x.do(http.MethodGet, "/api/v1/instances", "")
	var insts []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(ib, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, ib)
	}
	var instKey uint64
	for _, in := range insts {
		if in.ProcessID == processID && in.State == "active" && in.Key > instKey {
			instKey = in.Key
		}
	}
	if instKey == 0 {
		t.Fatalf("no active instance of %q in %s", processID, ib)
	}
	_, vb := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instKey), "")
	var vars map[string]any
	if err := json.Unmarshal(vb, &vars); err != nil {
		t.Fatalf("decode variables %s: %v", vb, err)
	}
	verdict, ok := vars["verdict"]
	if !ok {
		t.Fatalf("no \"verdict\" variable on instance %d: %s", instKey, vb)
	}
	return fmt.Sprint(verdict)
}

// listDecisionDeployments reads the deployed-decision listing.
func listDecisionDeployments(t *testing.T, x deployTestHarness, query string) []deployedDecisionResp {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/decision-deployments"+query, "")
	if code != http.StatusOK {
		t.Fatalf("list decision deployments: %d %s", code, b)
	}
	var out []deployedDecisionResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode decision deployments: %v (%s)", err, b)
	}
	return out
}

// TestPublishDecisionOnlyApplication is the headline acceptance criterion: an
// application whose only artifact is a .dmn publishes, and what it publishes is a
// runtime artifact — durable, keyed, versioned, named by the release, and
// evaluable by a process deployed afterwards. Before this the same publish
// succeeded and deployed nothing at all.
func TestPublishDecisionOnlyApplication(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID, rep := publishDecisionApp(t, x, "Order Management", "eligibility", eligibilityDMN("approve"))

	// The publish reports the decision it deployed, and no process.
	if len(rep.Definitions) != 0 {
		t.Fatalf("definitions = %+v, want none (the application holds no BPMN)", rep.Definitions)
	}
	if len(rep.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one", rep.Decisions)
	}
	dec := rep.Decisions[0]
	if dec.DecisionID != "eligibility" || dec.Version != 1 || dec.Key == 0 {
		t.Fatalf("decision = %+v, want eligibility v1 with a runtime key", dec)
	}
	if dec.ApplicationID != appID || dec.ResourceName != "eligibility.dmn" || dec.Checksum == "" || !dec.Current {
		t.Fatalf("decision = %+v, want it filed under the application, named, checksummed and current", dec)
	}

	// The release manifest names the runtime artifact the publish produced.
	if rep.Release == nil {
		t.Fatal("publish minted no release")
	}
	var member *releaseMember
	for i := range rep.Release.Members {
		if rep.Release.Members[i].Kind == "decision" {
			member = &rep.Release.Members[i]
		}
	}
	if member == nil {
		t.Fatalf("release members = %+v, want a decision member", rep.Release.Members)
	}
	if member.Ref != "eligibility" || member.Key != dec.Key || member.ArtifactVer != 1 || member.Artifact != "eligibility.dmn" {
		t.Fatalf("decision member = %+v, want it to name the deployed decision", *member)
	}

	// It is listed as a runtime definition, and its deployed source is readable.
	rows := listDecisionDeployments(t, x, "?applicationId="+appID)
	if len(rows) != 1 || rows[0].Key != dec.Key {
		t.Fatalf("listing = %+v, want the one decision just published", rows)
	}
	code, xml := x.do(http.MethodGet, fmt.Sprintf("/api/v1/decision-deployments/%d/xml", dec.Key), "")
	if code != http.StatusOK || !strings.Contains(string(xml), `id="eligibility"`) {
		t.Fatalf("deployed DMN XML: %d %s", code, xml)
	}

	// And it evaluates: a process deployed afterwards binds to it and runs.
	key := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, x, key, "orders"); got != "approve" {
		t.Fatalf("verdict = %q, want approve", got)
	}
}

// TestDecisionOnlyApplicationSurvivesRestart is the recovery half: the decision
// comes back from its durable record with the same key and version, the registry
// is rebuilt from the persisted source, and a process deployed before the restart
// still evaluates it — with no DMN re-loaded from outside.
func TestDecisionOnlyApplicationSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := bootDecisionStack(t, dir)
	_, rep := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))
	deployedKey := rep.Decisions[0].Key
	procKey := deployProcess(t, first.x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, first.x, procKey, "orders"); got != "approve" {
		t.Fatalf("before restart: verdict = %q, want approve", got)
	}
	first.shutdown()

	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	rows := listDecisionDeployments(t, second.x, "")
	if len(rows) != 1 || rows[0].Key != deployedKey || rows[0].Version != 1 {
		t.Fatalf("after restart: listing = %+v, want the same key %d at v1", rows, deployedKey)
	}
	if got := runAndReadVerdict(t, second.x, procKey, "orders"); got != "approve" {
		t.Fatalf("after restart: verdict = %q, want approve", got)
	}
}

// TestLatestBindingFreezesAtDeployTime is the version-pinning regression, end to
// end and across a restart. It is the whole of issue #915's "exact version
// binding" scenario:
//
//	publish eligibility v1 → deploy Process A (latest)      → A sees v1
//	publish eligibility v2 → Process A, again               → A still sees v1
//	                         deploy Process B (latest)      → B sees v2
//	restart                                                 → A sees v1, B sees v2
func TestLatestBindingFreezesAtDeployTime(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)

	appID, v1 := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))

	procA := deployProcess(t, first.x, eligibilityProcess("proc-a", "latest"))
	if got := runAndReadVerdict(t, first.x, procA, "proc-a"); got != "approve" {
		t.Fatalf("A on v1: verdict = %q, want approve", got)
	}

	// Edit the decision and publish the application again: a second version of the
	// same decision id, under a new runtime key.
	uploadModel(t, first.x, "eligibility", eligibilityDMN("vip"))
	v2 := publishApp(t, first.x, appID)
	if v2.Decisions[0].Version != 2 || v2.Decisions[0].Key == v1.Decisions[0].Key {
		t.Fatalf("second publish = %+v, want v2 under a new key (v1 was %+v)", v2.Decisions[0], v1.Decisions[0])
	}

	// The already-deployed process does not move. This is the defect the record
	// exists for: before it, publishing v2 silently re-pointed Process A.
	if got := runAndReadVerdict(t, first.x, procA, "proc-a"); got != "approve" {
		t.Fatalf("A after v2 was published: verdict = %q, want approve (still v1)", got)
	}

	// A process deployed now resolves latest to v2, once, and keeps it.
	procB := deployProcess(t, first.x, eligibilityProcess("proc-b", "latest"))
	if got := runAndReadVerdict(t, first.x, procB, "proc-b"); got != "vip" {
		t.Fatalf("B on v2: verdict = %q, want vip", got)
	}

	// The bindings are on disk, not in memory: a restart changes neither.
	first.shutdown()
	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	if got := runAndReadVerdict(t, second.x, procA, "proc-a"); got != "approve" {
		t.Fatalf("A after restart: verdict = %q, want approve (v1)", got)
	}
	if got := runAndReadVerdict(t, second.x, procB, "proc-b"); got != "vip" {
		t.Fatalf("B after restart: verdict = %q, want vip (v2)", got)
	}

	// Both versions stay deployed and addressable; only the newer is current.
	rows := listDecisionDeployments(t, second.x, "?decisionId=eligibility")
	if len(rows) != 2 {
		t.Fatalf("listing = %+v, want both versions", rows)
	}
	if !rows[0].Current || rows[0].Version != 2 || rows[1].Current || rows[1].Version != 1 {
		t.Fatalf("listing = %+v, want v2 current and v1 superseded", rows)
	}
}

// TestDeploymentBindingStillPinsToItsOwnSnapshot guards the binding this record
// did not change: `deployment` evaluates the model bundled with the process, and
// publishing a newer decision deployment does not touch it.
func TestDeploymentBindingStillPinsToItsOwnSnapshot(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID, _ := publishDecisionApp(t, x, "Order Management", "eligibility", eligibilityDMN("approve"))
	// The reference makes the model resolvable to the single-model deploy, which
	// bundles it into the process's own deployment.
	pinned := deployProcess(t, x, eligibilityProcess("pinned", "deployment"))

	uploadModel(t, x, "eligibility", eligibilityDMN("vip"))
	publishApp(t, x, appID)

	if got := runAndReadVerdict(t, x, pinned, "pinned"); got != "approve" {
		t.Fatalf("deployment-bound verdict = %q, want approve (its own snapshot)", got)
	}
}

// TestLatestFallsBackToTheBundledSnapshot covers the resolution rule's second
// case: a process whose decision was never published on its own pins to the model
// bundled with it, so `latest` and `deployment` name the same model and a deploy
// that worked before this record still works.
func TestLatestFallsBackToTheBundledSnapshot(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	uploadModel(t, x, "eligibility", eligibilityDMN("approve"))
	x.addRef("", "Eligibility", "eligibility") // ungrouped: never published as a decision

	if rows := listDecisionDeployments(t, x, ""); len(rows) != 0 {
		t.Fatalf("decision deployments = %+v, want none", rows)
	}
	key := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, x, key, "orders"); got != "approve" {
		t.Fatalf("verdict = %q, want approve (the bundled snapshot)", got)
	}
}

// TestPublishRefusesAnInvalidDecisionWholesale is the "never partly visible" rule.
// An application carrying one good and one broken model publishes nothing: no
// decision deployment, no release, and the good model is not evaluable either.
func TestPublishRefusesAnInvalidDecisionWholesale(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID := x.mkProject("Order Management")
	uploadModel(t, x, "eligibility", eligibilityDMN("approve"))
	x.addRef(appID, "Eligibility", "eligibility")
	// A reference to a model that does not resolve at all.
	x.addRef(appID, "Ghost", "ghost")

	code, b := x.do(http.MethodPost, "/api/v1/applications/"+appID+"/publish", "")
	if code != http.StatusConflict {
		t.Fatalf("publish = %d %s, want 409", code, b)
	}
	if rows := listDecisionDeployments(t, x, ""); len(rows) != 0 {
		t.Fatalf("decision deployments = %+v, want none — a refused publish deploys nothing", rows)
	}
	code, rb := x.do(http.MethodGet, "/api/v1/applications/"+appID+"/releases", "")
	if code != http.StatusOK || strings.TrimSpace(string(rb)) != "[]" {
		t.Fatalf("releases = %d %s, want none", code, rb)
	}
}

// TestUploadRefusesUncompilableDMN keeps the earliest gate honest: a model temis
// cannot compile never reaches the model folder, so it can never be referenced,
// published, or deployed. Malformed XML and decision logic that does not compile
// are both refused here, before any of the versioning machinery is involved.
func TestUploadRefusesUncompilableDMN(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	for _, tc := range []struct{ name, xml string }{
		{"malformed xml", "<definitions"},
		{"decision logic that does not compile", brokenDMNModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, b := x.do(http.MethodPost, "/api/v1/dmn-models?name=bad", tc.xml); code != http.StatusBadRequest {
				t.Fatalf("upload = %d %s, want 400", code, b)
			}
		})
	}
}

// TestPublishRefusesADecisionThatStoppedCompiling is the other half of "never
// partly visible": a reference whose model became invalid after it was stored — a
// model folder is a folder, and a file in it can be edited by something other than
// Atlas — refuses the publish, and no decision is deployed.
func TestPublishRefusesADecisionThatStoppedCompiling(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID := x.mkProject("Order Management")
	uploadModel(t, x, "eligibility", eligibilityDMN("approve"))
	x.addRef(appID, "Eligibility", "eligibility")
	// Break the stored model behind Atlas's back.
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "eligibility.dmn"), []byte(brokenDMNModel), 0o644); err != nil {
		t.Fatalf("break model: %v", err)
	}

	code, b := x.do(http.MethodPost, "/api/v1/applications/"+appID+"/publish", "")
	if code != http.StatusConflict {
		t.Fatalf("publish = %d %s, want 409", code, b)
	}
	if rows := listDecisionDeployments(t, x, ""); len(rows) != 0 {
		t.Fatalf("decision deployments = %+v, want none", rows)
	}
}

// TestPublishDeploysEveryDecisionOfAnApplication covers an application publishing
// more than one model, including two that provide the same decision id: every
// model becomes its own deployment, the decision ids are versioned independently,
// and the last one registered is what a process deployed afterwards pins to — a
// stated, deterministic outcome rather than an accident of map order.
func TestPublishDeploysEveryDecisionOfAnApplication(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID := x.mkProject("Order Management")
	uploadModel(t, x, "eligibility", eligibilityDMN("approve"))
	uploadModel(t, x, "discount", discountDMN)
	x.addRef(appID, "Eligibility", "eligibility")
	x.addRef(appID, "Discount", "discount")

	rep := publishApp(t, x, appID)
	if len(rep.Decisions) != 2 {
		t.Fatalf("decisions = %+v, want two", rep.Decisions)
	}
	byID := map[string]deployedDecisionResp{}
	for _, d := range rep.Decisions {
		byID[d.DecisionID] = d
	}
	if byID["eligibility"].Key == byID["discount"].Key {
		t.Fatalf("two models shared a deployment key: %+v", rep.Decisions)
	}
	for _, id := range []string{"eligibility", "discount"} {
		if byID[id].Version != 1 {
			t.Fatalf("%s = %+v, want v1", id, byID[id])
		}
	}
	// The release names both.
	kinds := map[string]int{}
	for _, m := range rep.Release.Members {
		kinds[m.Kind]++
	}
	if kinds["decision"] != 2 {
		t.Fatalf("release members = %+v, want two decision members", rep.Release.Members)
	}
}

// TestPublishIsAtomicWhenTheDecisionStoreFails is the durability boundary (I2): if
// the decision record cannot be written, the publish fails, nothing is registered
// with the DMN registry, and no release claims a runtime artifact that does not
// exist. Persisting before registering is what makes the failure look like this.
func TestPublishIsAtomicWhenTheDecisionStoreFails(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	appID := x.mkProject("Order Management")
	uploadModel(t, x, "eligibility", eligibilityDMN("approve"))
	x.addRef(appID, "Eligibility", "eligibility")

	srv.decisionDeploys = brokenStore(newDecisionStore(filepath.Join(t.TempDir(), "gone")))

	code, b := x.do(http.MethodPost, "/api/v1/applications/"+appID+"/publish", "")
	if code != http.StatusInternalServerError {
		t.Fatalf("publish = %d %s, want 500", code, b)
	}
	// Nothing became evaluable: a process deployed now finds no decision deployment
	// and falls back to its own bundled snapshot, which is the pre-publish world.
	var pinned uint64
	srv.do(func() {
		var ok bool
		pinned, ok = srv.dmnRegistry.LatestDecisionKey("eligibility")
		if ok {
			t.Errorf("the registry holds decision deployment %d after a failed publish", pinned)
		}
	})
	code, rb := x.do(http.MethodGet, "/api/v1/applications/"+appID+"/releases", "")
	if code != http.StatusOK || strings.TrimSpace(string(rb)) != "[]" {
		t.Fatalf("releases = %d %s, want none", code, rb)
	}
}

// TestLegacyDeploymentKeepsRuntimeLatest is the compatibility guarantee. A
// deployment record written before deploy-time pinning carries no binding policy,
// and must keep resolving latest at task activation (ADR-0063) — including picking
// up a decision deployment published afterwards. Nothing on disk may change
// meaning under an upgrade.
func TestLegacyDeploymentKeepsRuntimeLatest(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)

	// Deploy a latest-bound process with its model bundled, the way a pre-pinning
	// installation had one.
	uploadModel(t, first.x, "eligibility", eligibilityDMN("approve"))
	first.x.addRef("", "Eligibility", "eligibility")
	procKey := deployProcess(t, first.x, eligibilityProcess("orders", "latest"))
	first.shutdown()

	// Age the record: strip the policy marker and the resolved bindings, which is
	// exactly what a record written before this looks like.
	agePinnedDeployment(t, dir, procKey)

	second := bootDecisionStack(t, dir)
	// Publishing a newer version of the decision moves the pointer the legacy
	// definition reads at activation — the ADR-0063 behavior it was deployed under.
	_, rep := publishDecisionApp(t, second.x, "Order Management", "eligibility2", eligibilityDMN("vip"))
	if rep.Decisions[0].DecisionID != "eligibility" {
		t.Fatalf("published decision = %+v, want the same decision id", rep.Decisions[0])
	}
	if got := runAndReadVerdict(t, second.x, procKey, "orders"); got != "vip" {
		t.Fatalf("legacy definition: verdict = %q, want vip (runtime latest, as deployed)", got)
	}
	second.shutdown()

	// And a restart does not quietly convert it: the record is still legacy, so it
	// still tracks the newest deployed model.
	third := bootDecisionStack(t, dir)
	defer third.shutdown()
	if got := runAndReadVerdict(t, third.x, procKey, "orders"); got != "vip" {
		t.Fatalf("legacy definition after restart: verdict = %q, want vip", got)
	}
}

// agePinnedDeployment rewrites a stored deployment record as a pre-pinning one:
// no bindingPolicy, no decisionBindings. Editing the JSON directly is the point —
// it produces the bytes an older Atlas wrote, rather than a shape this code could
// only have written on purpose.
func agePinnedDeployment(t *testing.T, dir string, key uint64) {
	t.Helper()
	path := filepath.Join(dir, "deployments", fmt.Sprintf("%d.json", key))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deployment record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode deployment record: %v", err)
	}
	if _, ok := rec["bindingPolicy"]; !ok {
		t.Fatalf("deployment %d was written without a binding policy; the record under test is not the new shape", key)
	}
	delete(rec, "bindingPolicy")
	delete(rec, "decisionBindings")
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encode deployment record: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write deployment record: %v", err)
	}
}

// decisionServiceDMN is a DMN 1.5 model whose output decision is reached through a
// decision service: "Approval" encapsulates "Eligibility" and outputs "Routing".
// A business rule task names the output decision, which is what makes the service
// runnable from BPMN without Atlas growing a second DMN surface.
const decisionServiceDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             namespace="http://atlas/dmn/decision-service" name="Approval" id="defs_approval">
  <inputData id="id_amount" name="Applicant Age">
    <variable name="Applicant Age" typeRef="number"/>
  </inputData>
  <decision id="id_elig" name="Eligibility">
    <variable name="Eligibility" typeRef="string"/>
    <informationRequirement><requiredInput href="#id_amount"/></informationRequirement>
    <literalExpression><text>if Applicant Age &gt;= 18 then "ELIGIBLE" else "INELIGIBLE"</text></literalExpression>
  </decision>
  <decision id="id_route" name="Routing">
    <variable name="Routing" typeRef="string"/>
    <informationRequirement><requiredDecision href="#id_elig"/></informationRequirement>
    <literalExpression><text>if Eligibility = "ELIGIBLE" then "ACCEPT" else "DECLINE"</text></literalExpression>
  </decision>
  <decisionService id="id_approval" name="Approval">
    <outputDecision href="#id_route"/>
    <encapsulatedDecision href="#id_elig"/>
    <inputData href="#id_amount"/>
  </decisionService>
</definitions>`

// decisionServiceProcess calls the decision service's output decision and parks so
// the result can be read.
const decisionServiceProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="approval" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <calledDecision decisionId="Routing" resultVariable="verdict" bindingType="latest"/>
        <decisionInput name="Applicant Age" value="30"/>
      </extensionElements>
    </businessRuleTask>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

// TestDecisionServiceVerticalSlice walks issue #915's end-to-end acceptance path
// on the DMN 1.5 Decision Service fixture: author the model, publish it from an
// application holding nothing else, restart Atlas, and have a BPMN process deployed
// afterwards evaluate the service's output decision through temis — with nothing
// re-loaded from outside Atlas at any point.
func TestDecisionServiceVerticalSlice(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)

	appID, rep := publishDecisionApp(t, first.x, "Approval", "approval", decisionServiceDMN)
	// The model's evaluable decisions — the encapsulated one and the service's
	// output — are each versioned in their own right.
	ids := map[string]int32{}
	for _, d := range rep.Decisions {
		ids[d.DecisionID] = d.Version
	}
	if ids["Routing"] != 1 || ids["Eligibility"] != 1 {
		t.Fatalf("published decisions = %+v, want Eligibility and Routing at v1", rep.Decisions)
	}
	deployedKey := rep.Decisions[0].Key
	for _, d := range rep.Decisions {
		if d.Key != deployedKey {
			t.Fatalf("one model produced two deployment keys: %+v", rep.Decisions)
		}
	}
	first.shutdown()

	// Restart: the registry is rebuilt from the persisted DMN source alone.
	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	rows := listDecisionDeployments(t, second.x, "?applicationId="+appID)
	if len(rows) != 2 || rows[0].Key != deployedKey || rows[1].Key != deployedKey {
		t.Fatalf("after restart: listing = %+v, want both decisions of deployment %d", rows, deployedKey)
	}

	key := deployProcess(t, second.x, decisionServiceProcess)
	if got := runAndReadVerdict(t, second.x, key, "approval"); got != "ACCEPT" {
		t.Fatalf("decision service verdict = %q, want ACCEPT", got)
	}
}

// TestOperationsListsADecisionNoProcessCalls closes the product gap the new
// artifact opens: an application may publish decisions and no BPMN at all, and the
// Operations decisions overview — which listed only what a deployed process
// references — would then show nothing at all for it.
func TestOperationsListsADecisionNoProcessCalls(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	publishDecisionApp(t, x, "Order Management", "eligibility", eligibilityDMN("approve"))

	code, b := x.do(http.MethodGet, "/api/v1/decisions/deployed", "")
	if code != http.StatusOK {
		t.Fatalf("deployed decisions = %d %s", code, b)
	}
	var rows []deployedDecisionView
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, b)
	}
	if len(rows) != 1 || rows[0].DecisionID != "eligibility" || !rows[0].Local {
		t.Fatalf("rows = %+v, want the published decision listed as local", rows)
	}
	if len(rows[0].Processes) != 0 || rows[0].Evaluations != 0 {
		t.Fatalf("rows = %+v, want no referencing process and no evaluations yet", rows)
	}
}

// TestDecisionDeploymentEndpointErrors covers what the two listing endpoints owe a
// caller when the request or the store is wrong: a key that is not a key, a key
// nothing was deployed under, and a store that cannot be read. Each is a distinct
// answer, and none of them is a nil dereference.
func TestDecisionDeploymentEndpointErrors(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	if code, b := x.do(http.MethodGet, "/api/v1/decision-deployments/not-a-key/xml", ""); code != http.StatusBadRequest {
		t.Fatalf("xml of a non-numeric key = %d %s, want 400", code, b)
	}
	if code, b := x.do(http.MethodGet, "/api/v1/decision-deployments/4242/xml", ""); code != http.StatusNotFound {
		t.Fatalf("xml of an unknown key = %d %s, want 404", code, b)
	}

	// A record on disk that is not a record — a truncated write, a bad restore — is
	// reported rather than read as "no such decision", because the two call for
	// different things from whoever is holding the pager.
	if err := os.WriteFile(filepath.Join(srv.decisionDeploys.Dir(), "77.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}
	if code, b := x.do(http.MethodGet, "/api/v1/decision-deployments/77/xml", ""); code != http.StatusInternalServerError {
		t.Fatalf("xml of a corrupt record = %d %s, want 500", code, b)
	}

	srv.decisionDeploys = brokenStore(newDecisionStore(filepath.Join(t.TempDir(), "gone")))
	if code, b := x.do(http.MethodGet, "/api/v1/decision-deployments", ""); code != http.StatusInternalServerError {
		t.Fatalf("listing over an unreadable store = %d %s, want 500", code, b)
	}
	// A single record is a clean miss rather than a failure when the directory is
	// gone, which is the sidecar store's stated behaviour — so this stays a 404.
	if code, b := x.do(http.MethodGet, "/api/v1/decision-deployments/1/xml", ""); code != http.StatusNotFound {
		t.Fatalf("xml over an unreadable store = %d %s, want 404", code, b)
	}
}

// TestRecoveryRefusesADecisionRecordItCannotCompile is the hard half of the
// ADR-0177 split, for decisions: a stored record whose XML will not parse at all
// stops the server with a message naming the file, because there is no model to
// bring back and silently dropping it would leave processes pinned to a key
// nothing answers for.
func TestRecoveryRefusesADecisionRecordItCannotCompile(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)
	_, rep := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))
	key := rep.Decisions[0].Key
	first.shutdown()

	rewriteDecisionXML(t, dir, key, "<definitions")

	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err == nil {
		srv.Close()
		t.Fatal("the server started over an uncompilable decision record; the decisions pinned to it answer nothing")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d.json", key)) {
		t.Errorf("New: %v, want the message to name the record — acting on it means editing that file", err)
	}
}

// TestRecoveryRestoresADecisionWhoseLogicStoppedCompiling is the soft half: a
// record that still parses but whose decision logic has since acquired a
// diagnostic comes back anyway. Refusing it would undeploy nothing and would only
// keep the server — and every instance behind it — from starting.
func TestRecoveryRestoresADecisionWhoseLogicStoppedCompiling(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)
	_, rep := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))
	key := rep.Decisions[0].Key
	first.shutdown()

	rewriteDecisionXML(t, dir, key, brokenDMNModel)

	second := bootDecisionStack(t, dir)
	defer second.shutdown()
	rows := listDecisionDeployments(t, second.x, "")
	if len(rows) != 1 || rows[0].Key != key {
		t.Fatalf("listing = %+v, want the record restored under key %d", rows, key)
	}
}

// rewriteDecisionXML replaces the DMN source inside a stored decision deployment,
// standing in for a model that stopped compiling between two starts.
func rewriteDecisionXML(t *testing.T, dir string, key uint64, xml string) {
	t.Helper()
	path := filepath.Join(dir, "decisions", fmt.Sprintf("%d.json", key))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read decision record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode decision record: %v", err)
	}
	rec["xml"] = xml
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encode decision record: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write decision record: %v", err)
	}
}
