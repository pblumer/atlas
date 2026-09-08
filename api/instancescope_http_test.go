package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// reviewBPMN is a one-user-task process. The task carries a form and a candidate
// group, which is what makes it usable for the "who may read this instance" matrix:
// an assignee, a member of the candidate group, and everybody else are three
// different answers.
const reviewBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="review" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="check">
      <extensionElements>
        <zeebe:formDefinition formId="review-form"/>
        <zeebe:assignmentDefinition candidateGroups="Reviewers"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="end"/>
  </process>
</definitions>`

// reviewForm asks for one field, `comment`, nested inside a group so the walk over
// the schema has to recurse. The instance also carries a `secret`, which no form
// asks for and a task holder must therefore never be handed.
const reviewForm = `{"id":"review-form","name":"Review","schema":{"type":"default","components":[` +
	`{"type":"group","components":[{"type":"textfield","key":"comment","label":"Comment"}]}]}}`

// startReview deploys the review process, saves its form, and starts one instance
// carrying a form field and a value nobody's form asks for. It returns the instance
// key.
func startReview(t *testing.T, admin *http.Client, ts *httptest.Server) uint64 {
	t.Helper()
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/forms", reviewForm); code != http.StatusOK {
		t.Fatalf("save form = %d: %s", code, body)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments", reviewBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}
	var dep struct{ Key uint64 }
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	code, body = cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"comment":"","secret":"only-for-the-project"}}`)
	if code != http.StatusOK {
		t.Fatalf("start instance = %d: %s", code, body)
	}
	// The create response names the definition, not the instance, so the key comes
	// from the listing — which the admin may read and nobody else needs to.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances = %d: %s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("instances = %d, want 1", len(rows))
	}
	return rows[0].Key
}

// instanceVarsAs reads the variables endpoint as one client.
func instanceVarsAs(t *testing.T, c *http.Client, base string, key uint64) (int, map[string]any) {
	t.Helper()
	resp, err := c.Get(fmt.Sprintf("%s/api/v1/instances/%d/variables", base, key))
	if err != nil {
		t.Fatalf("read variables: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestUnrelatedUserCannotReadInstanceVariables is the audit's F11 reproduction. An
// account with the plain `user` role, no project membership and no task on the
// instance has no relationship to it, and being signed in is not one.
func TestUnrelatedUserCannotReadInstanceVariables(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	createUserWithRoles(t, admin, ts.URL, "outsider", `["user"]`)
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	key := startReview(t, admin, ts)

	code, body := instanceVarsAs(t, outsider, ts.URL, key)
	if code != http.StatusNotFound {
		t.Fatalf("an unrelated account read the instance: HTTP %d %v", code, body)
	}
	// And the operator who may see everything still does.
	code, vars := instanceVarsAs(t, admin, ts.URL, key)
	if code != http.StatusOK || vars["secret"] != "only-for-the-project" {
		t.Fatalf("admin read = %d %v, want the whole instance", code, vars)
	}
}

// TestTaskHolderSeesOnlyTheFieldsTheirFormAsksFor: closing the endpoint must not
// close the Tasks app, and opening it for a task worker must not open the whole
// instance. A member of the task's candidate group gets `comment`, which the form
// asks for, and not `secret`, which it does not.
func TestTaskHolderSeesOnlyTheFieldsTheirFormAsksFor(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	reviewerID := createUserWithRoles(t, admin, ts.URL, "reviewer", `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create group = %d: %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &grp); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+reviewerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add group member = %d: %s", code, body)
	}
	// The session snapshots group membership at login (ADR-0180), so sign in after.
	reviewer := signInAs(t, ts.URL, "reviewer", "a-password-that-is-long")

	key := startReview(t, admin, ts)

	code, vars := instanceVarsAs(t, reviewer, ts.URL, key)
	if code != http.StatusOK {
		t.Fatalf("the task's candidate group could not read its own form's fields: HTTP %d %v", code, vars)
	}
	if _, ok := vars["comment"]; !ok {
		t.Errorf("variables = %v, want the form's own field `comment`", vars)
	}
	if _, ok := vars["secret"]; ok {
		t.Errorf("variables = %v, want no `secret`: no form asks for it", vars)
	}
}

// TestProjectMemberReadsTheWholeInstance follows ADR-0071's inheritance one step
// further than it went: a project's membership already reaches its drafts and, since
// F09, its deployments. The instances a deployment runs are the same object, so a
// viewer of the project sees them whole — including the values no form asks for,
// which is exactly what an operator of that business area needs and what a stranger
// must not have.
func TestProjectMemberReadsTheWholeInstance(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["user"]`)
	strangerID := createUserWithRoles(t, admin, ts.URL, "stranger", `["user"]`)
	_ = strangerID

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Claims"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create project = %d: %s", code, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+proj.ID+"/members/"+memberID,
		`{"role":"viewer"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add project member = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments?projectId="+proj.ID, reviewBPMN); code != http.StatusOK {
		t.Fatalf("deploy into project = %d: %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes = %d: %s", code, body)
	}
	var procs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("processes = %d, want 1", len(procs))
	}
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", procs[0].Key),
		`{"variables":{"comment":"","secret":"only-for-the-project"}}`); code != http.StatusOK {
		t.Fatalf("start instance = %d: %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances = %d: %s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode instances: %v (%d rows)", err, len(rows))
	}

	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")
	code, vars := instanceVarsAs(t, member, ts.URL, rows[0].Key)
	if code != http.StatusOK {
		t.Fatalf("a project viewer could not read the instance: HTTP %d %v", code, vars)
	}
	if vars["secret"] != "only-for-the-project" {
		t.Errorf("variables = %v, want the whole instance for a project member", vars)
	}

	stranger := signInAs(t, ts.URL, "stranger", "a-password-that-is-long")
	if code, vars := instanceVarsAs(t, stranger, ts.URL, rows[0].Key); code != http.StatusNotFound {
		t.Fatalf("someone outside the project read it: HTTP %d %v", code, vars)
	}
}

// TestAClaimedTaskIsTheHoldersAlone: a candidate group is who *may* take the work,
// not who may read it once somebody has. After a claim the group loses its window
// and the assignee keeps it — the same rule the inbox already applies to the work
// itself, applied to the data behind it.
func TestAClaimedTaskIsTheHoldersAlone(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	oneID := createUserWithRoles(t, admin, ts.URL, "one", `["user"]`)
	twoID := createUserWithRoles(t, admin, ts.URL, "two", `["user"]`)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create group = %d: %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &grp); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	for _, id := range []string{oneID, twoID} {
		if code, body := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+id, ""); code != http.StatusOK && code != http.StatusNoContent {
			t.Fatalf("add group member = %d: %s", code, body)
		}
	}
	one := signInAs(t, ts.URL, "one", "a-password-that-is-long")
	two := signInAs(t, ts.URL, "two", "a-password-that-is-long")

	key := startReview(t, admin, ts)

	// Unclaimed: both group members may prefill the form.
	for name, c := range map[string]*http.Client{"one": one, "two": two} {
		if code, vars := instanceVarsAs(t, c, ts.URL, key); code != http.StatusOK {
			t.Fatalf("%s could not read the unclaimed task's fields: HTTP %d %v", name, code, vars)
		}
	}

	// One claims it.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks = %d: %s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("decode tasks: %v (%d)", err, len(tasks))
	}
	if code, body := cReq(t, one, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", tasks[0].Key), `{}`); code != http.StatusOK {
		t.Fatalf("claim = %d: %s", code, body)
	}

	if code, vars := instanceVarsAs(t, one, ts.URL, key); code != http.StatusOK {
		t.Fatalf("the holder lost access to their own task: HTTP %d %v", code, vars)
	}
	if code, vars := instanceVarsAs(t, two, ts.URL, key); code != http.StatusNotFound {
		t.Fatalf("a group member still read a task somebody else holds: HTTP %d %v", code, vars)
	}
}

// TestTheTasksAppsOwnScopeKeyIsAuthorizedToo: the Tasks app does not ask for the
// process instance, it asks for the task's *element instance*, so a task inside a
// subprocess prefills from its own fields (ADR-0084). That key has to resolve to the
// instance it belongs to and be authorized the same way, or the fix would close the
// endpoint for exactly the caller it was left open for.
func TestTheTasksAppsOwnScopeKeyIsAuthorizedToo(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	reviewerID := createUserWithRoles(t, admin, ts.URL, "reviewer", `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create group = %d: %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &grp); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+reviewerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add group member = %d: %s", code, body)
	}
	createUserWithRoles(t, admin, ts.URL, "nobody", `["user"]`)
	reviewer := signInAs(t, ts.URL, "reviewer", "a-password-that-is-long")
	nobody := signInAs(t, ts.URL, "nobody", "a-password-that-is-long")

	startReview(t, admin, ts)

	code, body = cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks = %d: %s", code, body)
	}
	var tasks []struct {
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("decode tasks: %v (%d)", err, len(tasks))
	}
	scope := tasks[0].ElementInstanceKey
	if scope == 0 {
		t.Fatal("the task carries no element-instance key, so this test proves nothing")
	}

	code, vars := instanceVarsAs(t, reviewer, ts.URL, scope)
	if code != http.StatusOK {
		t.Fatalf("the task holder could not prefill from their own scope: HTTP %d %v", code, vars)
	}
	if _, ok := vars["secret"]; ok {
		t.Errorf("variables = %v, want no `secret` even through the task's own scope", vars)
	}
	if code, vars := instanceVarsAs(t, nobody, ts.URL, scope); code != http.StatusNotFound {
		t.Fatalf("an unrelated account read the task's scope: HTTP %d %v", code, vars)
	}
}

// TestAMachineCredentialIsJudgedByItsIssuersRoles: a token is minted by an
// administrator and carries that administrator's roles (ADR-0194), so the object
// check answers for it exactly as it does for the person — and must not turn the
// machine surface off as a side effect of closing the endpoint. MCP is the same
// case: it forwards the caller's Authorization or Cookie header verbatim to this
// route (ADR-0196), so whatever the caller may read there, they may read here.
func TestAMachineCredentialIsJudgedByItsIssuersRoles(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"ci","scope":"full","expiresInDays":30}`)

	key := startReview(t, admin, ts)

	code, body := bearerReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", key), "", secret)
	if code != http.StatusOK {
		t.Fatalf("an admin-issued token could not read the instance: HTTP %d %s", code, body)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	if vars["secret"] != "only-for-the-project" {
		t.Errorf("variables = %v, want the whole instance for an admin-issued token", vars)
	}
}

// mixedBPMN parks four live elements on one instance at once: a user task with a
// form, a user task without one, a service task, and an intermediate timer. Between
// them they are every kind of thing the allowlist scan walks past, which is the
// point — the scan has to answer for a whole instance, not for the one element the
// happy path puts there.
const mixedBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="mixed" isExecutable="true">
    <startEvent id="start"/>
    <parallelGateway id="fork"/>
    <userTask id="check">
      <extensionElements>
        <zeebe:formDefinition formId="review-form"/>
        <zeebe:assignmentDefinition candidateGroups="Reviewers"/>
      </extensionElements>
    </userTask>
    <userTask id="note">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="Reviewers"/>
      </extensionElements>
    </userTask>
    <serviceTask id="work">
      <extensionElements><zeebe:taskDefinition type="mixed.work"/></extensionElements>
    </serviceTask>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition><timeDuration xsi:type="tFormalExpression">PT30M</timeDuration></timerEventDefinition>
    </intermediateCatchEvent>
    <sequenceFlow id="f0" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="note"/>
    <sequenceFlow id="f3" sourceRef="fork" targetRef="work"/>
    <sequenceFlow id="f4" sourceRef="fork" targetRef="wait"/>
  </process>
</definitions>`

// reviewerOn creates a `user` account in a group named after the models' candidate
// group and returns a signed-in client for it. Membership is snapshotted at login
// (ADR-0180), so the sign-in comes last.
func reviewerOn(t *testing.T, admin *http.Client, ts *httptest.Server, username string) *http.Client {
	t.Helper()
	id := createUserWithRoles(t, admin, ts.URL, username, `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create group = %d: %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &grp); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+id, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add group member = %d: %s", code, body)
	}
	return signInAs(t, ts.URL, username, "a-password-that-is-long")
}

// startMixed deploys mixedBPMN (optionally saving the form first) and returns the
// instance key.
func startMixed(t *testing.T, admin *http.Client, ts *httptest.Server, withForm bool) uint64 {
	t.Helper()
	if withForm {
		if code, body := cReq(t, admin, ts, "POST", "/api/v1/forms", reviewForm); code != http.StatusOK {
			t.Fatalf("save form = %d: %s", code, body)
		}
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments", mixedBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}
	var dep struct{ Key uint64 }
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"comment":"","secret":"only-for-the-project"}}`); code != http.StatusOK {
		t.Fatalf("start instance = %d: %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances = %d: %s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode instances: %v (%d rows)", err, len(rows))
	}
	return rows[0].Key
}

// TestTheAllowlistWalksPastEverythingThatIsNotTheirTask: the scan meets a service
// task's job, an intermediate timer holding no job at all, and a user task with no
// form, alongside the one task that does have a form. Only the last contributes.
//
// The formless user task is the interesting one and the contract worth stating: it
// is the caller's own task, and it still grants nothing, because there is no
// declared set of fields to grant and inventing one is how an allowlist stops being
// one.
func TestTheAllowlistWalksPastEverythingThatIsNotTheirTask(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	reviewer := reviewerOn(t, admin, ts, "reviewer")

	key := startMixed(t, admin, ts, true)

	code, vars := instanceVarsAs(t, reviewer, ts.URL, key)
	if code != http.StatusOK {
		t.Fatalf("the task holder was refused: HTTP %d %v", code, vars)
	}
	if _, ok := vars["comment"]; !ok {
		t.Errorf("variables = %v, want the field the one form asks for", vars)
	}
	if _, ok := vars["secret"]; ok {
		t.Errorf("variables = %v, want nothing a form did not ask for", vars)
	}
}

// TestAFormThatWasNeverSavedGrantsNothing: the model names a form the installation
// does not have — a deployment that outlived its form, or one restored without it.
// There is nothing to read the field list from, so the task grants nothing rather
// than everything.
func TestAFormThatWasNeverSavedGrantsNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	reviewer := reviewerOn(t, admin, ts, "reviewer")

	key := startMixed(t, admin, ts, false) // the form is never saved

	if code, vars := instanceVarsAs(t, reviewer, ts.URL, key); code != http.StatusNotFound {
		t.Fatalf("a task whose form does not exist granted access: HTTP %d %v", code, vars)
	}
}

// TestADeploymentWhoseProjectIsGoneGrantsNothing: a project can be deleted while
// the deployments filed into it keep running. ADR-0034 calls that state Ungrouped,
// and there is no membership left to inherit — so the instance falls back to the
// task rule like any other, and somebody who was a member of the project it used to
// belong to is a stranger to it again.
//
// The alternative reading, "a deployment with no resolvable project is everybody's",
// is the hole this whole change closes, so it is worth a test that says which way it
// goes rather than a comment.
func TestADeploymentWhoseProjectIsGoneGrantsNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["user"]`)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Doomed"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create project = %d: %s", code, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+proj.ID+"/members/"+memberID,
		`{"role":"viewer"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add project member = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments?projectId="+proj.ID, reviewBPMN); code != http.StatusOK {
		t.Fatalf("deploy into project = %d: %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes = %d: %s", code, body)
	}
	var procs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &procs); err != nil || len(procs) != 1 {
		t.Fatalf("decode processes: %v (%d)", err, len(procs))
	}
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", procs[0].Key),
		`{"variables":{"secret":"only-for-the-project"}}`); code != http.StatusOK {
		t.Fatalf("start instance = %d: %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances = %d: %s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode instances: %v (%d)", err, len(rows))
	}

	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")
	if code, vars := instanceVarsAs(t, member, ts.URL, rows[0].Key); code != http.StatusOK {
		t.Fatalf("the project member could not read it while the project existed: HTTP %d %v", code, vars)
	}

	if code, body := cReq(t, admin, ts, "DELETE", "/api/v1/projects/"+proj.ID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("delete project = %d: %s", code, body)
	}
	// The session still carries the old membership; the project it pointed at does not.
	if code, vars := instanceVarsAs(t, member, ts.URL, rows[0].Key); code != http.StatusNotFound {
		t.Fatalf("a deployment whose project is gone still granted access: HTTP %d %v", code, vars)
	}
}
