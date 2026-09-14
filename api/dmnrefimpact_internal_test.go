package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The acceptance suite for ADR-draft-deleting-a-dmn-reference-says-what-it-breaks:
// before a DMN reference is deleted, the server can say exactly what would stop
// deploying afterwards — and, just as importantly, say nothing when nothing would.
//
// It drives the same fixtures the deploy preflight is measured with, on purpose:
// the claim under test is that the warning and the refusal apply one rule, so the
// two have to be measured against the same models.

// refImpact reads one reference's deletion impact.
func refImpact(t *testing.T, x deployTestHarness, refID string) refImpactResp {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/dmnrefs/"+refID+"/impact", "")
	if code != http.StatusOK {
		t.Fatalf("impact: %d %s", code, b)
	}
	var out refImpactResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode impact: %v (%s)", err, b)
	}
	return out
}

// theRefFor finds the reference record pointing at a model handle.
func theRefFor(t *testing.T, x deployTestHarness, modelRef string) string {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/dmnrefs", "")
	if code != http.StatusOK {
		t.Fatalf("list refs: %d %s", code, b)
	}
	var rows []dmnRefResp
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode refs: %v", err)
	}
	for _, r := range rows {
		if r.ModelRef == modelRef {
			return r.ID
		}
	}
	t.Fatalf("no reference points at %q (rows = %+v)", modelRef, rows)
	return ""
}

// TestDeletingTheLastReferenceNamesTheProcessThatWouldStopDeploying is the
// headline: a deployed process whose business rule task binds `deployment` needs
// this reference's model bundled with it, so deleting the reference makes that
// process undeployable from then on. The warning says which process, and how it
// binds.
func TestDeletingTheLastReferenceNamesTheProcessThatWouldStopDeploying(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	deployProcess(t, x, eligibilityProcess("pinned", "deployment"))

	imp := refImpact(t, x, theRefFor(t, x, "eligibility"))
	if !imp.Resolved || len(imp.Decisions) != 1 || imp.Decisions[0] != "eligibility" {
		t.Fatalf("impact = %+v, want it to report the one decision the model provides", imp)
	}
	if len(imp.Exclusive) != 1 || imp.Exclusive[0] != "eligibility" {
		t.Fatalf("exclusive = %v, want eligibility: no other reference provides it", imp.Exclusive)
	}
	if len(imp.Blocked) != 1 {
		t.Fatalf("blocked = %+v, want the one deployed process", imp.Blocked)
	}
	got := imp.Blocked[0]
	if got.Kind != "deployed" || got.ProcessID != "pinned" || got.DecisionID != "eligibility" || got.Binding != "deployment" {
		t.Fatalf("blocked[0] = %+v, want the deployed 'pinned' process, deployment-bound on eligibility", got)
	}
	if got.Key == 0 || got.Version == 0 {
		t.Errorf("blocked[0] = %+v, want the key and version that identify which deployment", got)
	}
}

// TestADeployedDecisionKeepsALatestBoundProcessOutOfTheWarning: the warning reads
// the preflight's rule forwards, so it must not warn about what the preflight
// would not refuse. A latest-bound task on a decision that is deployed pins that
// deployment and never reads the bundle (ADR-0327) — deleting the reference costs
// it nothing, and saying otherwise would be noise.
func TestADeployedDecisionKeepsALatestBoundProcessOutOfTheWarning(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	deployProcess(t, x, eligibilityProcess("orders", "latest"))
	refID := theRefFor(t, x, "eligibility")

	// Nothing deployed for the decision yet: the latest-bound task would fall back
	// to a bundle that would no longer exist, so it is at risk.
	if imp := refImpact(t, x, refID); len(imp.Blocked) != 1 || imp.Blocked[0].Binding != "latest" {
		t.Fatalf("blocked = %+v, want the latest-bound process while nothing is deployed for it", imp.Blocked)
	}

	// Deploy the decision in its own right, and the same process is safe.
	deployOneDecision(t, x, "?modelRef=eligibility", eligibilityDMN("approve"))
	imp := refImpact(t, x, refID)
	if len(imp.Blocked) != 0 {
		t.Fatalf("blocked = %+v, want none: the deployment covers the latest-bound task", imp.Blocked)
	}
	// It is still the last *model* for that decision, which is a different fact and
	// is still reported.
	if len(imp.Exclusive) != 1 || imp.Exclusive[0] != "eligibility" {
		t.Errorf("exclusive = %v, want eligibility: a deployment is not a model", imp.Exclusive)
	}
}

// TestASecondReferenceMakesADecisionNonExclusive: the decision does not lose its
// last model when another reference provides it too, so nothing is at risk and
// nothing is claimed.
func TestASecondReferenceMakesADecisionNonExclusive(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	seedReferencedDecision(t, x, "eligibility-copy", eligibilityDMN("approve"))
	deployProcess(t, x, eligibilityProcess("pinned", "deployment"))

	imp := refImpact(t, x, theRefFor(t, x, "eligibility"))
	if len(imp.Decisions) != 1 {
		t.Fatalf("decisions = %v, want the one this model provides", imp.Decisions)
	}
	if len(imp.Exclusive) != 0 {
		t.Fatalf("exclusive = %v, want none: the copy provides it too", imp.Exclusive)
	}
	if len(imp.Blocked) != 0 {
		t.Fatalf("blocked = %+v, want none", imp.Blocked)
	}
}

// TestTheWarningReachesADraftThatIsNotDeployedYet: the artifact an author is most
// likely to deploy next is a draft, and it is the one a deployed-process-only
// warning would miss.
func TestTheWarningReachesADraftThatIsNotDeployedYet(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	appID := x.mkProject("orders app")
	x.saveDraft(appID, eligibilityProcess("draft-proc", "deployment"))

	imp := refImpact(t, x, theRefFor(t, x, "eligibility"))
	if len(imp.Blocked) != 1 {
		t.Fatalf("blocked = %+v, want the draft", imp.Blocked)
	}
	got := imp.Blocked[0]
	if got.Kind != "draft" || got.ProcessID != "draft-proc" || got.Binding != "deployment" {
		t.Fatalf("blocked[0] = %+v, want the deployment-bound draft", got)
	}
	if got.Key != 0 {
		t.Errorf("blocked[0] = %+v, want no deployment key on a draft", got)
	}
}

// TestADraftThatNamesNothingIsNotReported: the prefilter that avoids compiling
// every draft must not change the answer. A draft with no business rule task at
// all is silent, and so is one naming a different decision.
func TestADraftThatNamesNothingIsNotReported(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	appID := x.mkProject("orders app")
	x.saveDraft(appID, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="plain" isExecutable="true">
    <startEvent id="s"/><userTask id="wait"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`)
	x.saveDraft(appID, decisionsProcess("other", brTask{"discount", "deployment"}))

	if imp := refImpact(t, x, theRefFor(t, x, "eligibility")); len(imp.Blocked) != 0 {
		t.Fatalf("blocked = %+v, want none: neither draft names eligibility", imp.Blocked)
	}
}

// TestAnUnresolvedReferenceBreaksNothing: a handle that names no model has no
// decisions to lose, so the confirm has nothing to warn about and says so rather
// than failing.
func TestAnUnresolvedReferenceBreaksNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	x.addRef("", "Ghost", "not-a-model")

	imp := refImpact(t, x, theRefFor(t, x, "not-a-model"))
	if imp.Resolved {
		t.Errorf("resolved = true, want false for a handle that names no model")
	}
	if len(imp.Decisions) != 0 || len(imp.Exclusive) != 0 || len(imp.Blocked) != 0 {
		t.Fatalf("impact = %+v, want everything empty", imp)
	}
}

// TestTheImpactOfAReferenceThatDoesNotExist: a deleted (or never created)
// reference is a 404, the way every other per-reference route reports one.
func TestTheImpactOfAReferenceThatDoesNotExist(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, _ := x.do(http.MethodGet, "/api/v1/dmnrefs/nope/impact", ""); code != http.StatusNotFound {
		t.Fatalf("impact of an unknown reference = %d, want 404", code)
	}
}

// TestBlockedDecisionsAppliesThePreflightRule pins the rule itself, per binding
// and per coverage, without a server around it. It is the table in the record.
func TestBlockedDecisionsAppliesThePreflightRule(t *testing.T) {
	exclusive := map[string]bool{"a": true, "b": true}
	covered := map[string]bool{"b": true}

	// Deployment-bound: always blocked, covered or not — it evaluates the model
	// bundled with its own process.
	got := blockedDecisions([]string{"a", "b"}, []string{"a", "b"}, exclusive, covered)
	if len(got) != 2 || got[0].binding != "deployment" || got[1].binding != "deployment" {
		t.Fatalf("deployment-bound = %+v, want both blocked as deployment", got)
	}

	// Latest-bound: blocked only where nothing is deployed.
	got = blockedDecisions([]string{"a", "b"}, nil, exclusive, covered)
	if len(got) != 1 || got[0].decision != "a" || got[0].binding != "latest" {
		t.Fatalf("latest-bound = %+v, want only the uncovered decision", got)
	}

	// A decision another reference also provides is not exclusive, so not at risk.
	if got = blockedDecisions([]string{"c"}, []string{"c"}, exclusive, covered); len(got) != 0 {
		t.Fatalf("non-exclusive = %+v, want none", got)
	}

	// One diagram naming a decision twice reports it once, and the fatal binding
	// wins over the survivable one.
	got = blockedDecisions([]string{"b", "b"}, []string{"b"}, exclusive, covered)
	if len(got) != 1 || got[0].binding != "deployment" {
		t.Fatalf("mixed bindings = %+v, want one entry, deployment", got)
	}
}

// TestADraftInSomebodyElsesSpaceIsCountedNotNamed: sharing scopes (ADR-0071) decide
// what a caller may see, and a deletion warning must not become the way round
// them. A draft the caller cannot view still counts — understating the damage
// because of who is looking would be a warning that lies to exactly the person
// about to act — but nothing about it is disclosed.
func TestADraftInSomebodyElsesSpaceIsCountedNotNamed(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	h := srv.Handler()
	session := func(id string) string {
		t.Helper()
		tok, err := srv.sessions.create(User{ID: id, Username: id, Roles: []string{RoleModeler, RoleUser}}, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return tok
	}
	as := func(tok, method, path, body string) (int, []byte) {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	// Both artifacts are ungrouped, which makes each its creator's personal space.
	owner, stranger := session("usr_owner"), session("usr_stranger")
	if code, b := as(owner, http.MethodPost, "/api/v1/dmn-models?name=eligibility&handle=eligibility", eligibilityDMN("approve")); code != http.StatusOK {
		t.Fatalf("upload model: %d %s", code, b)
	}
	if code, b := as(owner, http.MethodPost, "/api/v1/dmnrefs", `{"name":"Eligibility","modelRef":"eligibility"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	if code, b := as(stranger, http.MethodPost, "/api/v1/drafts", eligibilityProcess("theirs", "deployment")); code != http.StatusOK {
		t.Fatalf("stranger draft: %d %s", code, b)
	}

	code, b := as(owner, http.MethodGet, "/api/v1/dmnrefs", "")
	if code != http.StatusOK {
		t.Fatalf("list refs: %d %s", code, b)
	}
	var rows []dmnRefResp
	if err := json.Unmarshal(b, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("refs = %s (%v), want the one just created", b, err)
	}

	code, b = as(owner, http.MethodGet, "/api/v1/dmnrefs/"+rows[0].ID+"/impact", "")
	if code != http.StatusOK {
		t.Fatalf("impact: %d %s", code, b)
	}
	var imp refImpactResp
	if err := json.Unmarshal(b, &imp); err != nil {
		t.Fatalf("decode impact: %v (%s)", err, b)
	}
	if imp.BlockedHidden != 1 {
		t.Fatalf("blockedHidden = %d, want 1: the stranger's draft counts", imp.BlockedHidden)
	}
	if len(imp.Blocked) != 0 {
		t.Fatalf("blocked = %+v, want nothing named from a space the caller cannot see", imp.Blocked)
	}
	if strings.Contains(string(b), "theirs") {
		t.Errorf("impact = %s, want it not to disclose the hidden draft's process id", b)
	}
}
