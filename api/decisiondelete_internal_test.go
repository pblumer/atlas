package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// twoDecisionDMN is one model providing two decisions, so a delete can be measured
// against the unit it actually removes: a key, which is a model, which holds one
// version of every lineage it declares.
const twoDecisionDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_pair" name="Pair" namespace="http://atlas/dmn">
  <decision id="eligibility" name="eligibility">
    <literalExpression><text>"approve"</text></literalExpression>
  </decision>
  <decision id="discount" name="discount">
    <literalExpression><text>0.1</text></literalExpression>
  </decision>
</definitions>`

// asUser issues one request carrying a session cookie, for the role cases.
func asUser(h http.Handler, method, path, body, tok string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The acceptance suite for ADR-0336: a decision
// deployment can be removed, under the guard ADR-0329 wrote down before the route
// existed.
//
// It replaces api/decisiondelete_guard_test.go, which asserted the route's absence
// and whose failure message said to do exactly this. The two refusals are what
// those tests are about; the successful delete is measured across a restart,
// because a deletion that a reboot undoes is not a deletion.

// deleteDecisionDeployment removes one and returns the status and body.
func deleteDecisionDeployment(t *testing.T, x deployTestHarness, key uint64) (int, string) {
	t.Helper()
	code, b := x.do(http.MethodDelete, fmt.Sprintf("/api/v1/decision-deployments/%d", key), "")
	return code, string(b)
}

// TestDeletingADecisionDeploymentSurvivesARestart is the headline. Removing a
// deployment nothing depends on takes the record, the listing and the registry
// entry with it — and a reboot over the same directory agrees, which is the only
// test that distinguishes a deletion from a forgetting.
func TestDeletingADecisionDeploymentSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)
	rep := deployOneDecision(t, stack.x, "", eligibilityDMN("approve"))
	if rows := listDecisionDeployments(t, stack.x, "?decisionId=eligibility"); len(rows) != 1 {
		t.Fatalf("listing = %+v, want the deployment just made", rows)
	}

	if code, body := deleteDecisionDeployment(t, stack.x, rep.Key); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204: nothing is pinned to it and it has no history", code, body)
	}
	if rows := listDecisionDeployments(t, stack.x, "?decisionId=eligibility"); len(rows) != 0 {
		t.Fatalf("listing = %+v, want it gone", rows)
	}
	// The registry forgot it too, so a process deployed now must bundle a model
	// again — which is the deploy preflight's rule reading the same state (ADR-0327).
	code, b := stack.x.do(http.MethodPost, "/api/v1/deployments", eligibilityProcess("orders", "latest"))
	if code != http.StatusConflict {
		t.Fatalf("deploy after delete = %d %s, want 409: the decision is no longer deployed", code, b)
	}
	stack.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	if rows := listDecisionDeployments(t, rebooted.x, "?decisionId=eligibility"); len(rows) != 0 {
		t.Fatalf("after restart = %+v, want the deletion to have stuck", rows)
	}
	if code, _ := rebooted.x.do(http.MethodGet, fmt.Sprintf("/api/v1/decision-deployments/%d/xml", rep.Key), ""); code != http.StatusNotFound {
		t.Errorf("source after restart = %d, want 404", code)
	}
}

// TestAPinnedDecisionDeploymentIsRefused is ADR-0329's guard, and the second half
// of this test is the half that is easy to get wrong: the process has **no running
// instances**, and is pinned all the same. A process delete may use a live count
// because deleting the definition removes the thing that would be started; here the
// definition survives and keeps its pin.
func TestAPinnedDecisionDeploymentIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	rep := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	defKey := deployProcess(t, x, eligibilityProcess("orders", "latest"))

	code, body := deleteDecisionDeployment(t, x, rep.Key)
	if code != http.StatusConflict {
		t.Fatalf("delete = %d %s, want 409: a deployed definition is pinned to it", code, body)
	}
	for _, want := range []string{"orders", fmt.Sprintf("key %d", defKey), "eligibility"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal = %s, want it to name %q so the operator can go and look", body, want)
		}
	}
	// It is still there, and still evaluates.
	if rows := listDecisionDeployments(t, x, "?decisionId=eligibility"); len(rows) != 1 {
		t.Fatalf("listing = %+v, want a refused delete to have removed nothing", rows)
	}
	if got := runAndReadVerdict(t, x, defKey, "orders"); got != "approve" {
		t.Errorf("verdict = %q, want the pinned decision still answering", got)
	}
}

// TestASupersededVersionIsAsPinnedAsACurrentOne: a definition deployed while v1 was
// newest stays pinned to v1 after v2 ships. Deleting v1 "because v2 exists" is
// exactly the mistake the versioning was built to prevent.
func TestASupersededVersionIsAsPinnedAsACurrentOne(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	first := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	defKey := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	second := deployOneDecision(t, x, "", eligibilityDMN("vip"))

	// The process is pinned to v1 and keeps answering v1, whatever v2 says.
	if got := runAndReadVerdict(t, x, defKey, "orders"); got != "approve" {
		t.Fatalf("verdict = %q, want the pinned version, not the newest", got)
	}
	code, body := deleteDecisionDeployment(t, x, first.Key)
	if code != http.StatusConflict {
		t.Fatalf("delete of the superseded version = %d %s, want 409", code, body)
	}
	// And v2, which nothing is pinned to, is refused for the *other* reason: it is
	// the current version with history behind it.
	code, body = deleteDecisionDeployment(t, x, second.Key)
	if code != http.StatusConflict {
		t.Fatalf("delete of the current version = %d %s, want 409", code, body)
	}
	if strings.Contains(body, "pinned to") {
		t.Errorf("refusal = %s, want the current-version reason, not the pin one", body)
	}
}

// TestTheCurrentVersionWithHistoryBehindItIsRefused is the second guard on its own.
// Removing it would send the next deploy's latest binding back a version with
// nothing recording the change, and lower the version high-water mark — which is
// derived from the surviving records, so the deploy after that would reuse a number
// a release manifest already spends.
func TestTheCurrentVersionWithHistoryBehindItIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	first := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	second := deployOneDecision(t, x, "", eligibilityDMN("vip"))
	if second.Decisions[0].Version != 2 {
		t.Fatalf("second deploy = v%d, want v2", second.Decisions[0].Version)
	}

	code, body := deleteDecisionDeployment(t, x, second.Key)
	if code != http.StatusConflict {
		t.Fatalf("delete = %d %s, want 409", code, body)
	}
	for _, want := range []string{"eligibility v2", "deploy a newer version first"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal = %s, want it to contain %q", body, want)
		}
	}

	// Oldest first is the way through, and it is allowed: v1 is not current.
	if code, body := deleteDecisionDeployment(t, x, first.Key); code != http.StatusNoContent {
		t.Fatalf("delete of the superseded version = %d %s, want 204", code, body)
	}
	// v2 is now the only version and may go, because nothing survives to contradict
	// a decision that starts again at v1.
	if code, body := deleteDecisionDeployment(t, x, second.Key); code != http.StatusNoContent {
		t.Fatalf("delete of the last version = %d %s, want 204", code, body)
	}
	if rows := listDecisionDeployments(t, x, "?decisionId=eligibility"); len(rows) != 0 {
		t.Fatalf("listing = %+v, want the whole lineage gone", rows)
	}
	// With no history left, the counter starts over rather than carrying on from a
	// number nothing records.
	again := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	if again.Decisions[0].Version != 1 {
		t.Errorf("after deleting the lineage, the next deploy = v%d, want v1", again.Decisions[0].Version)
	}
}

// TestDeletingASupersededVersionLeavesTheCounterAlone: the common cleanup — drop
// the old iterations, keep what is live — must not move the version number, before
// or after a restart.
func TestDeletingASupersededVersionLeavesTheCounterAlone(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)
	v1 := deployOneDecision(t, stack.x, "", eligibilityDMN("a"))
	deployOneDecision(t, stack.x, "", eligibilityDMN("b"))
	v3 := deployOneDecision(t, stack.x, "", eligibilityDMN("c"))
	if v3.Decisions[0].Version != 3 {
		t.Fatalf("third deploy = v%d, want v3", v3.Decisions[0].Version)
	}

	if code, body := deleteDecisionDeployment(t, stack.x, v1.Key); code != http.StatusNoContent {
		t.Fatalf("delete v1 = %d %s, want 204", code, body)
	}
	next := deployOneDecision(t, stack.x, "", eligibilityDMN("d"))
	if next.Decisions[0].Version != 4 {
		t.Fatalf("next deploy = v%d, want v4: deleting an old version must not free a number", next.Decisions[0].Version)
	}
	stack.shutdown()

	// And the recovered counter agrees with the live one.
	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	after := deployOneDecision(t, rebooted.x, "", eligibilityDMN("e"))
	if after.Decisions[0].Version != 5 {
		t.Errorf("after restart the next deploy = v%d, want v5", after.Decisions[0].Version)
	}
}

// TestDeletingAModelProvidingSeveralDecisionsTakesOneVersionOfEach: the unit is the
// key, which is one model. A model providing two decisions holds one version of
// each lineage, and both go together because both were written together.
func TestDeletingAModelProvidingSeveralDecisionsTakesOneVersionOfEach(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	rep := deployOneDecision(t, x, "", twoDecisionDMN)
	if len(rep.Decisions) != 2 {
		t.Fatalf("deploy = %+v, want both decisions", rep.Decisions)
	}

	if code, body := deleteDecisionDeployment(t, x, rep.Key); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", code, body)
	}
	for _, id := range []string{"eligibility", "discount"} {
		if rows := listDecisionDeployments(t, x, "?decisionId="+id); len(rows) != 0 {
			t.Errorf("%s listing = %+v, want it gone with its model", id, rows)
		}
	}
}

// TestDeletingADecisionDeploymentTwiceSucceeds: the caller asked for it to be gone,
// and it is. Idempotent like every other delete in this tree.
func TestDeletingADecisionDeploymentTwiceSucceeds(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	rep := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	if code, _ := deleteDecisionDeployment(t, x, rep.Key); code != http.StatusNoContent {
		t.Fatal("first delete")
	}
	if code, body := deleteDecisionDeployment(t, x, rep.Key); code != http.StatusNoContent {
		t.Fatalf("second delete = %d %s, want 204", code, body)
	}
	if code, _ := x.do(http.MethodDelete, "/api/v1/decision-deployments/not-a-key", ""); code != http.StatusBadRequest {
		t.Error("a key that is not a number should be a 400")
	}
}

// TestDeletingADecisionDeploymentNeedsModeler: it removes a runtime artifact, the
// same class of act as deleting a process definition, and carries the same role.
func TestDeletingADecisionDeploymentNeedsModeler(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	h := srv.Handler()
	session := func(id string, roles ...string) string {
		t.Helper()
		tok, err := srv.sessions.create(User{ID: id, Username: id, Roles: roles}, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return tok
	}
	modeler := session("usr_modeler", RoleModeler)
	rec := asUser(h, http.MethodPost, "/api/v1/decision-deployments", eligibilityDMN("approve"), modeler)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy = %d %s", rec.Code, rec.Body)
	}
	var rep deployDecisionResp
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := fmt.Sprintf("/api/v1/decision-deployments/%d", rep.Key)

	operator := session("usr_ops", RoleOperator)
	if got := asUser(h, http.MethodDelete, path, "", operator).Code; got != http.StatusForbidden {
		t.Errorf("operator delete = %d, want 403: reading what is deployed is not removing it", got)
	}
	if got := asUser(h, http.MethodDelete, path, "", modeler).Code; got != http.StatusNoContent {
		t.Errorf("modeler delete = %d, want 204", got)
	}
}

// TestTheVersionListingSaysWhatIsPinnedToEachVersion: the refusal is not the first
// place an operator should learn that a version is held. The listing they choose
// from carries it.
func TestTheVersionListingSaysWhatIsPinnedToEachVersion(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	first := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	defKey := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	second := deployOneDecision(t, x, "", eligibilityDMN("vip"))

	rows := listDecisionDeployments(t, x, "?decisionId=eligibility")
	if len(rows) != 2 {
		t.Fatalf("listing = %+v, want both versions", rows)
	}
	byKey := map[uint64]deployedDecisionResp{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	held := byKey[first.Key]
	if len(held.PinnedBy) != 1 {
		t.Fatalf("v1 pinnedBy = %+v, want the process that pinned it", held.PinnedBy)
	}
	if got := held.PinnedBy[0]; got.Key != defKey || got.ProcessID != "orders" || got.DecisionID != "eligibility" {
		t.Errorf("v1 pinnedBy[0] = %+v, want the orders definition", got)
	}
	// The newer one nothing has deployed against yet is held by nothing, which is
	// how an operator tells a version they can remove from one they cannot.
	if free := byKey[second.Key]; len(free.PinnedBy) != 0 {
		t.Errorf("v2 pinnedBy = %+v, want none: no process has been deployed since", free.PinnedBy)
	}
}
