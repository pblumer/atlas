package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The enforcement point end to end, on the HTTP surface
// (ADR-0475 §5, §14): a SharePoint delete as the
// person who started the process, through their own delegated connection, refused
// until a manager approves it and then executed with a brokered credential.

// approvalLoopModel deletes a document; an approval the policy asks for routes to a
// user task and back.
const approvalLoopModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-docs">
  <bpmn:error id="needApproval" errorCode="ATLAS_APPROVAL_REQUIRED"/>
  <bpmn:process id="document-cleanup" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="del">
      <bpmn:extensionElements>
        <atlas:sharePointConnector connector="sp-prod" operation="delete-file" site="contoso.sharepoint.com:/sites/hr" list="Documents" path="=docPath"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="asked" attachedToRef="del"><bpmn:errorEventDefinition errorRef="needApproval"/></bpmn:boundaryEvent>
    <bpmn:userTask id="approve"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="del"/>
    <bpmn:sequenceFlow id="f2" sourceRef="del" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="asked" targetRef="approve"/>
    <bpmn:sequenceFlow id="f4" sourceRef="approve" targetRef="del"/>
  </bpmn:process>
</bpmn:definitions>`

const managerApprovalPolicy = `{"id":"hr-documents","target":{"workerTypes":["sharepoint"],"operations":["delete-file"]},
  "ruleCombining":"first-applicable",
  "rules":[{"id":"members-with-manager","effect":"Permit",
            "when":{"subjectGroupsAny":["grp_documents"],"resourcePrefixes":["sharepoint:site=contoso.sharepoint.com:/sites/hr"]},
            "obligations":[{"id":"manager","type":"approval","approverGroup":"grp_managers","approvals":1,"validForSeconds":3600,"excludeRequester":true}]},
           {"id":"otherwise","effect":"Deny","reasonCode":"DELETE_NOT_ALLOWED"}]}`

type pepRig struct {
	t        *testing.T
	srv      *Server
	defKey   uint64
	refresh  int
	conn     identity.Connection
	worker   connector
	alice    *httpapi.Principal
	bob      *httpapi.Principal
	admin    *httpapi.Principal
	internal *httpapi.Principal
}

func newPEPRig(t *testing.T) *pepRig {
	t.Helper()
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(offloadableKindNames()))
	r := &pepRig{t: t, srv: srv,
		alice:    &httpapi.Principal{UserID: "usr_alice", Username: "alice", Roles: []string{RoleUser}, Session: true},
		bob:      &httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleUser}, Session: true},
		admin:    &httpapi.Principal{UserID: "usr_admin", Username: "admin", Roles: []string{RoleAdmin}, Session: true},
		internal: &httpapi.Principal{UserID: servicePrincipalName, Username: servicePrincipalName, Roles: legacyRoles(), Scope: apiScopeWorker},
	}
	// Auth is off in this server, so nothing minted a broker key: the rig plays the
	// supervisor and hands the SharePoint Worker Instance one (authorize, below).
	srv.brokerKey = "broker-key-for-the-sharepoint-worker"
	// The identity service's refresher is the provider; here it is a fake one.
	stores, err := identity.OpenStores(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.identity = identity.New(srv.runLoop, stores, srv.vault,
		func(_ context.Context, c identity.Connection, rt string) (identity.Tokens, error) {
			r.refresh++
			return identity.Tokens{AccessToken: "graph-token-" + strconv.Itoa(r.refresh), RefreshToken: rt + "+", ExpiresIn: 3600}, nil
		}, srv.configuredWorkerExists, time.Now)
	srv.do(func() {
		for _, u := range []User{
			{ID: "usr_alice", Username: "alice", Roles: []string{RoleUser}, Source: SourceLocal},
			{ID: "usr_bob", Username: "bob", Roles: []string{RoleUser}, Source: SourceLocal},
		} {
			if err := srv.users.Save(u); err != nil {
				t.Fatal(err)
			}
		}
		for _, g := range []group{
			{ID: "grp_documents", Name: "documents", Members: []string{"usr_alice"}},
			{ID: "grp_managers", Name: "managers", Members: []string{"usr_bob"}},
		} {
			if err := srv.groups.Save(g); err != nil {
				t.Fatal(err)
			}
		}
	})
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"sp-prod","kind":"sharepoint","credentialsRef":"sp-bundle"}`, "application/json")
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create worker: %d %s", code, raw)
	}
	_ = json.Unmarshal(raw, &r.worker)
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", approvalLoopModel, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(raw, &dep)
	r.defKey = dep.Key
	if _, err := srv.policies.SaveDraft("hr-documents", []byte(managerApprovalPolicy), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.policies.Publish("hr-documents", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	return r
}

// as calls a handler directly, with p as the request's principal: auth is off in
// this server, so the principal is put where the boundary would have put it.
func (r *pepRig) as(p *httpapi.Principal, h http.HandlerFunc, method, target, body string, vals map[string]string) (int, []byte) {
	r.t.Helper()
	return r.asWith(p, nil, h, method, target, body, vals)
}

// asWith is as with request headers.
func (r *pepRig) asWith(p *httpapi.Principal, header http.Header, h http.HandlerFunc, method, target, body string, vals map[string]string) (int, []byte) {
	r.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	for k, v := range vals {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (r *pepRig) startAsAlice() uint64 {
	r.t.Helper()
	code, raw := r.as(r.alice, r.srv.handleCreateInstance, http.MethodPost, "/",
		`{"variables":{"docPath":"/Drafts/old.docx"}}`, map[string]string{"key": strconv.FormatUint(r.defKey, 10)})
	if code != http.StatusOK && code != http.StatusCreated {
		r.t.Fatalf("start: %d %s", code, raw)
	}
	var out struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.InstanceKey
}

func (r *pepRig) lease() (uint64, uint64) {
	r.t.Helper()
	code, raw := serveInternal(r.t, r.srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"sharepoint"}`, compiler.SharePointOpsJobType), "application/json")
	if code != http.StatusOK {
		r.t.Fatalf("lease: %d %s", code, raw)
	}
	var out struct {
		Jobs []struct {
			JobKey     uint64 `json:"jobKey"`
			LeaseToken uint64 `json:"leaseToken"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Jobs) != 1 {
		r.t.Fatalf("leased %d jobs: %s", len(out.Jobs), raw)
	}
	return out.Jobs[0].JobKey, out.Jobs[0].LeaseToken
}

// authorize asks as p. The supervised SharePoint Worker Instance (r.internal) presents
// the broker key its supervisor handed it; authorizeWithKey asks with any other.
func (r *pepRig) authorize(p *httpapi.Principal, job, lease uint64) (int, authorizeAnswer) {
	r.t.Helper()
	key := ""
	if p == r.internal {
		key = r.srv.brokerKey
	}
	return r.authorizeWithKey(p, key, job, lease)
}

func (r *pepRig) authorizeWithKey(p *httpapi.Principal, brokerKey string, job, lease uint64) (int, authorizeAnswer) {
	r.t.Helper()
	var header http.Header
	if brokerKey != "" {
		header = http.Header{brokerKeyHeader: []string{brokerKey}}
	}
	code, raw := r.asWith(p, header, r.srv.handleAuthorizeJob, http.MethodPost, "/",
		fmt.Sprintf(`{"worker":"sharepoint","leaseToken":%d}`, lease), map[string]string{"key": strconv.FormatUint(job, 10)})
	var a authorizeAnswer
	_ = json.Unmarshal(raw, &a)
	if code != http.StatusOK && code != http.StatusForbidden && code != http.StatusConflict {
		r.t.Fatalf("authorize: %d %s", code, raw)
	}
	return code, a
}

func (r *pepRig) evidence(piKey uint64) []authorizationView {
	r.t.Helper()
	_, raw := r.as(r.admin, r.srv.handleInstanceAuthorizations, http.MethodGet, "/", "",
		map[string]string{"key": strconv.FormatUint(piKey, 10)})
	var out []authorizationView
	_ = json.Unmarshal(raw, &out)
	return out
}

func (r *pepRig) govern(mode, enforcement string, extra string) {
	r.t.Helper()
	code, raw := r.as(r.admin, r.srv.handleSetWorkerIdentity, http.MethodPut, "/",
		`{"mode":"`+mode+`","enforcement":"`+enforcement+`"`+extra+`}`, map[string]string{"id": r.worker.ID})
	if code != http.StatusOK {
		r.t.Fatalf("govern: %d %s", code, raw)
	}
}

func (r *pepRig) linkAlice() {
	r.t.Helper()
	c, err := r.srv.identity.Link(identity.LinkRequest{UserID: "usr_alice", Provider: identity.ProviderMicrosoft,
		Issuer: "https://login.microsoftonline.com/tid/v2.0", TenantID: "tid", Subject: "oid-alice", RefreshToken: "rt"})
	if err != nil {
		r.t.Fatal(err)
	}
	if c, err = r.srv.identity.SetAllowedWorkers(c.ID, "usr_alice", []string{"sp-prod"}); err != nil {
		r.t.Fatal(err)
	}
	r.conn = c
}

func (r *pepRig) userTask() uint64 {
	r.t.Helper()
	var key uint64
	r.srv.do(func() {
		_ = r.srv.store.ActivatableJobs(compiler.UserTaskJobTypeIndex, func(k uint64) error { key = k; return nil })
	})
	if key == 0 {
		r.t.Fatal("no user task is waiting")
	}
	return key
}

func TestALegacyWorkerIsNeitherDecidedNorRecorded(t *testing.T) {
	r := newPEPRig(t)
	pi := r.startAsAlice()
	job, lease := r.lease()
	code, a := r.authorize(r.internal, job, lease)
	if code != http.StatusOK || a.Enforcement != "off" || !a.Permitted || a.DecisionKey != 0 {
		t.Fatalf("legacy: %d %+v", code, a)
	}
	if ev := r.evidence(pi); len(ev) != 0 {
		t.Fatalf("a legacy Worker left evidence: %+v", ev)
	}
}

func TestADelegatedActionWithoutAConnectionIsRefusedAndRecorded(t *testing.T) {
	r := newPEPRig(t)
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()
	job, lease := r.lease()
	code, a := r.authorize(r.internal, job, lease)
	if code != http.StatusOK || a.Permitted || a.ReasonCode != "NO_CONNECTION" || a.Credential != nil {
		t.Fatalf("no connection: %d %+v", code, a)
	}
	ev := r.evidence(pi)
	if len(ev) != 1 || ev[0].ReasonCode != "NO_CONNECTION" || ev[0].InitiatedBy != "usr_alice" ||
		ev[0].ExecutedFor != "usr_alice" || !ev[0].Enforced || ev[0].Worker != "sp-prod" || ev[0].WorkerInstance != "sharepoint" {
		t.Fatalf("evidence = %+v", ev)
	}
	// The refusal parked the job on an incident of its own reason.
	r.srv.do(func() {
		jv, _, _ := r.srv.store.GetJob(job)
		inc, _ := r.srv.store.GetIncident(jv.ElementInstanceKey)
		if inc == nil || inc.Reason != model.IncidentAuthorizationDenied || !strings.Contains(inc.Message, "NO_CONNECTION") {
			t.Errorf("incident = %+v", inc)
		}
	})
}

func TestAnExternalWorkerInstanceIsNeverHandedADelegatedCredential(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()
	job, lease := r.lease()
	token := &httpapi.Principal{UserID: "system:token:abc", Roles: legacyRoles(), Scope: apiScopeWorker}
	if code, _ := r.authorize(token, job, lease); code != http.StatusForbidden {
		t.Fatalf("an API-token worker: %d", code)
	}
	if ev := r.evidence(pi); len(ev) != 0 {
		t.Fatalf("a refused brokering left a decision: %+v", ev)
	}
}

// The first complete scenario of §14, with its negative turns.
func TestADelegatedDeleteNeedsAManagersApprovalAndThenRunsAsTheRequester(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()

	// 1. Refused for want of approval: an approval is asked for, the model routes to
	//    its user task, and nothing is released.
	job, lease := r.lease()
	code, a := r.authorize(r.internal, job, lease)
	if code != http.StatusOK || a.Permitted || a.ReasonCode != "APPROVAL_REQUIRED" || a.ApprovalKey == 0 || a.Credential != nil {
		t.Fatalf("first attempt: %d %+v", code, a)
	}
	approval := strconv.FormatUint(a.ApprovalKey, 10)
	instance := strconv.FormatUint(pi, 10)
	decide := func(p *httpapi.Principal, outcome string) int {
		c, _ := r.as(p, r.srv.handleDecideActionApproval, http.MethodPost, "/", `{"outcome":"`+outcome+`","reason":"checked"}`,
			map[string]string{"key": instance, "approval": approval})
		return c
	}
	// 2. Alice cannot approve her own deletion; the internal worker credential cannot
	//    approve anything.
	if c := decide(r.alice, "granted"); c != http.StatusForbidden {
		t.Fatalf("self-approval: %d", c)
	}
	// An application Bob granted access to his account resolves to his id but holds
	// no session of his: an approval is his answer, not an application's.
	if c := decide(&httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleUser}}, "granted"); c != http.StatusForbidden {
		t.Fatalf("an application approved for bob: %d", c)
	}
	if c := decide(r.internal, "granted"); c != http.StatusForbidden {
		t.Fatalf("machine approval: %d", c)
	}
	// 3. Bob, a manager, approves; the user task completes and the loop returns.
	if c := decide(r.bob, "granted"); c != http.StatusOK {
		t.Fatalf("manager approval: %d", c)
	}
	if c := decide(r.bob, "granted"); c != http.StatusConflict {
		t.Fatalf("a second answer: %d", c)
	}
	code, raw := serveInternal(t, r.srv, http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(r.userTask(), 10)+"/complete", `{}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete the approval task: %d %s", code, raw)
	}

	// 4. A worker that completes without asking is refused: the process does not move
	//    on an unauthorized action.
	job, lease = r.lease()
	code, raw = serveInternal(t, r.srv, http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(job, 10)+"/complete",
		fmt.Sprintf(`{"worker":"sharepoint","leaseToken":%d}`, lease), "application/json")
	if code != http.StatusConflict {
		t.Fatalf("completion without a permit: %d %s", code, raw)
	}

	// 5. Asked again, it is permitted, as alice, with a brokered credential and the
	//    parameters it covers.
	code, a = r.authorize(r.internal, job, lease)
	if code != http.StatusOK || !a.Permitted || a.Credential == nil || a.Credential.AccessToken != "graph-token-1" ||
		a.Job["path"] != "/Drafts/old.docx" || a.PolicyID != "hr-documents" || a.PolicyVersion != 1 {
		t.Fatalf("approved attempt: %d %+v", code, a)
	}
	code, raw = serveInternal(t, r.srv, http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(job, 10)+"/complete",
		fmt.Sprintf(`{"worker":"sharepoint","leaseToken":%d,"variables":{"deleted":true}}`, lease), "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete after the permit: %d %s", code, raw)
	}

	// 6. The evidence answers the questions of §22.
	ev := r.evidence(pi)
	if len(ev) != 2 {
		t.Fatalf("decisions = %d", len(ev))
	}
	last := ev[1]
	if last.Effect != "Permit" || last.InitiatedBy != "usr_alice" || last.ExecutedFor != "usr_alice" ||
		last.ExecutedAs != "microsoft:tid:oid-alice" || last.ConnectionID != r.conn.ID || last.PolicyID != "hr-documents" ||
		last.Resource != "sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Documents;path=/Drafts/old.docx" ||
		!strings.Contains(string(last.Detail), "1") {
		t.Fatalf("permit evidence = %+v", last)
	}
	if ev[0].ActionDigest != last.ActionDigest {
		t.Fatal("the approved action and the executed one have different digests")
	}
	_, raw = r.as(r.bob, r.srv.handleInstanceActionApprovals, http.MethodGet, "/", "", map[string]string{"key": instance})
	if !strings.Contains(string(raw), `"principal":"usr_bob"`) || !strings.Contains(string(raw), `"outcome":"granted"`) {
		t.Fatalf("approvals as bob sees them: %s", raw)
	}
}

func TestARevokedConnectionStopsTheNextAction(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	r.startAsAlice()
	if _, err := r.srv.identity.Revoke(r.conn.ID, "usr_alice", false, "left"); err != nil {
		t.Fatal(err)
	}
	job, lease := r.lease()
	// Revoked connections are no candidates: the per-job choice finds none.
	if _, a := r.authorize(r.internal, job, lease); a.Permitted || a.ReasonCode != "NO_CONNECTION" {
		t.Fatalf("after revocation: %+v", a)
	}
}

func TestAShadowWorkerRecordsAndProceeds(t *testing.T) {
	r := newPEPRig(t)
	r.govern("service", "shadow", "")
	pi := r.startAsAlice()
	job, lease := r.lease()
	// The policy would deny — alice has no approval — but shadow never blocks.
	_, a := r.authorize(r.internal, job, lease)
	if a.Permitted || a.Enforced || a.Job == nil || a.Credential != nil {
		t.Fatalf("shadow: %+v", a)
	}
	code, raw := serveInternal(t, r.srv, http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(job, 10)+"/complete",
		fmt.Sprintf(`{"worker":"sharepoint","leaseToken":%d}`, lease), "application/json")
	if code != http.StatusOK {
		t.Fatalf("a shadow Worker's completion was refused: %d %s", code, raw)
	}
	if ev := r.evidence(pi); len(ev) != 1 || ev[0].Enforced {
		t.Fatalf("evidence = %+v", ev)
	}
}

func TestIdentityPoliciesAreValidatedWhereTheyAreSet(t *testing.T) {
	r := newPEPRig(t)
	for name, body := range map[string]string{
		"delegated in shadow":   `{"mode":"delegated-per-job","enforcement":"shadow","actAs":"initiator"}`,
		"unknown mode":          `{"mode":"root","enforcement":"enforce"}`,
		"no enforcement":        `{"mode":"service"}`,
		"service with a conn":   `{"mode":"service","enforcement":"enforce","connectionId":"con_x"}`,
		"per-job without actAs": `{"mode":"delegated-per-job","enforcement":"enforce"}`,
		"fixed without consent": `{"mode":"delegated-fixed","enforcement":"enforce","connectionId":"con_missing"}`,
		"broken json":           `{`,
	} {
		if code, raw := r.as(r.admin, r.srv.handleSetWorkerIdentity, http.MethodPut, "/", body, map[string]string{"id": r.worker.ID}); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	if code, _ := r.as(r.admin, r.srv.handleSetWorkerIdentity, http.MethodPut, "/", `{"mode":"service","enforcement":"enforce"}`, map[string]string{"id": "nope"}); code != http.StatusNotFound {
		t.Errorf("unknown worker: %d", code)
	}
	r.govern("service", "enforce", "")
	_, raw := r.as(r.admin, r.srv.handleSecurityReadiness, http.MethodGet, "/", "", nil)
	if !strings.Contains(string(raw), `"governed":true`) || !strings.Contains(string(raw), "cooperatively") {
		t.Fatalf("readiness: %s", raw)
	}
	if code, _ := r.as(r.admin, r.srv.handleClearWorkerIdentity, http.MethodDelete, "/", "", map[string]string{"id": r.worker.ID}); code != http.StatusNoContent {
		t.Fatalf("clear: %d", code)
	}
	if _, ok := r.srv.governedWorkerNamed("sp-prod"); ok {
		t.Fatal("a cleared Worker is still governed")
	}
}
