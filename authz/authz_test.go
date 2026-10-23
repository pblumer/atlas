package authz_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/authz"
)

// t0 is the instant every request is evaluated at, so no test reads a clock.
var t0 = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC).UnixNano()

const hour = int64(time.Hour)

// delegated is a well-formed request for a delegated SharePoint delete by alice,
// through her own active connection that allows the Worker.
func delegated() *authz.Request {
	return &authz.Request{
		Subject: authz.Subject{ID: "usr_alice", Known: true, Roles: []string{"user"}, Groups: []string{"grp_documents"}},
		Actor: authz.Actor{
			IdentityMode: authz.ModeDelegatedPerJob, Worker: "sp-prod", WorkerType: "sharepoint",
			WorkerInstance: "sharepoint", ExecutedAs: "microsoft:tid-1:oid-alice",
			Connection: &authz.Connection{
				ID: "con_1", OwnerID: "usr_alice", Status: authz.ConnectionActive,
				AllowedWorkers: []string{"sp-prod"}, LastInteractiveAuth: t0 - hour,
			},
		},
		Action:   authz.Action{Operation: "delete-file", Digest: "d1"},
		Resource: authz.Resource{Type: "sharepoint", ID: "sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Documents;path=/a.docx"},
		Context:  authz.Context{Time: t0, ProcessID: "document-approval", ProcessInstanceKey: 7, JobKey: 9, InitiatedBy: "usr_alice"},
	}
}

func mustSet(t *testing.T, docs ...string) *authz.Set {
	t.Helper()
	var published []authz.Published
	for i, src := range docs {
		doc, err := authz.ParseDocument([]byte(src))
		if err != nil {
			t.Fatalf("ParseDocument #%d: %v", i, err)
		}
		published = append(published, authz.Published{Document: doc, Version: i + 1})
	}
	set, err := authz.CompileSet(context.Background(), published)
	if err != nil {
		t.Fatalf("CompileSet: %v", err)
	}
	return set
}

const permitMembers = `{
  "id": "sp-members",
  "target": {"workerTypes": ["sharepoint"]},
  "ruleCombining": "first-applicable",
  "rules": [
    {"id": "members", "effect": "Permit", "when": {"subjectGroupsAny": ["grp_documents"]}}
  ]
}`

func TestNothingApplicableIsNotAPermit(t *testing.T) {
	set := mustSet(t)
	d := set.Evaluate(context.Background(), delegated())
	if d.Effect != authz.NotApplicable || d.Permitted() {
		t.Fatalf("an empty set decided %v (%s)", d.Effect, d.ReasonCode)
	}
	if d.ReasonCode != "NO_APPLICABLE_POLICY" {
		t.Errorf("reason = %q", d.ReasonCode)
	}
}

func TestAMissingSetIsIndeterminate(t *testing.T) {
	var set *authz.Set
	d := set.Evaluate(context.Background(), delegated())
	if d.Effect != authz.Indeterminate || d.ReasonCode != "POLICY_SET_UNAVAILABLE" {
		t.Fatalf("nil set decided %v %q", d.Effect, d.ReasonCode)
	}
}

func TestTheZeroDecisionIsNeverAPermit(t *testing.T) {
	var d authz.Decision
	if d.Permitted() || d.Effect != authz.Indeterminate {
		t.Fatalf("zero decision = %v", d.Effect)
	}
}

func TestAMatchingRulePermits(t *testing.T) {
	d := mustSet(t, permitMembers).Evaluate(context.Background(), delegated())
	if !d.Permitted() {
		t.Fatalf("decided %v %q, trace %v", d.Effect, d.ReasonCode, d.Trace)
	}
	if d.PolicyID != "sp-members" || d.PolicyVersion != 1 {
		t.Errorf("deciding policy = %s v%d", d.PolicyID, d.PolicyVersion)
	}
	if len(d.Trace) == 0 {
		t.Error("a decision with no trace cannot be explained")
	}
}

// The built-in guards run before any policy and no policy can turn them around.
func TestBuiltInGuardsCannotBeOverridden(t *testing.T) {
	set := mustSet(t, `{"id":"everything","ruleCombining":"first-applicable","rules":[{"id":"all","effect":"Permit"}]}`)
	for _, tc := range []struct {
		name   string
		mutate func(r *authz.Request)
		effect authz.Effect
		reason string
	}{
		{"no subject", func(r *authz.Request) { r.Subject = authz.Subject{} }, authz.Deny, "NO_EXECUTING_SUBJECT"},
		{"unknown subject", func(r *authz.Request) { r.Subject.Known = false }, authz.Deny, "SUBJECT_UNKNOWN"},
		{"disabled subject", func(r *authz.Request) { r.Subject.Disabled = true }, authz.Deny, "SUBJECT_DISABLED"},
		{"no connection", func(r *authz.Request) { r.Actor.Connection = nil }, authz.Deny, "NO_CONNECTION"},
		{"revoked", func(r *authz.Request) { r.Actor.Connection.Status = authz.ConnectionRevoked }, authz.Deny, "CONNECTION_REVOKED"},
		{"reauth", func(r *authz.Request) { r.Actor.Connection.Status = authz.ConnectionReauthRequired }, authz.Deny, "CONNECTION_REAUTH_REQUIRED"},
		{"odd status", func(r *authz.Request) { r.Actor.Connection.Status = "paused" }, authz.Deny, "CONNECTION_INACTIVE"},
		{"someone else's", func(r *authz.Request) { r.Actor.Connection.OwnerID = "usr_bob" }, authz.Deny, "CONNECTION_NOT_OWNED"},
		{"worker not allowed", func(r *authz.Request) { r.Actor.Connection.AllowedWorkers = []string{"sp-test"} }, authz.Deny, "CONNECTION_NOT_ALLOWED_FOR_WORKER"},
		{"no operation", func(r *authz.Request) { r.Action.Operation = "" }, authz.Indeterminate, "MISSING_ATTRIBUTE"},
		{"no digest", func(r *authz.Request) { r.Action.Digest = "" }, authz.Indeterminate, "MISSING_ATTRIBUTE"},
		{"no worker", func(r *authz.Request) { r.Actor.Worker = "" }, authz.Indeterminate, "MISSING_ATTRIBUTE"},
		{"no time", func(r *authz.Request) { r.Context.Time = 0 }, authz.Indeterminate, "MISSING_ATTRIBUTE"},
		{"unknown mode", func(r *authz.Request) { r.Actor.IdentityMode = "root" }, authz.Indeterminate, "UNKNOWN_IDENTITY_MODE"},
		{"too many groups", func(r *authz.Request) { r.Subject.Groups = make([]string, authz.MaxListLen+1) }, authz.Indeterminate, "INPUT_TOO_LARGE"},
		{"disabled initiator under a service identity", func(r *authz.Request) {
			r.Actor.IdentityMode = authz.ModeService
			r.Actor.Connection = nil
			r.Subject.Disabled = true
		}, authz.Deny, "SUBJECT_DISABLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := delegated()
			tc.mutate(r)
			d := set.Evaluate(context.Background(), r)
			if d.Effect != tc.effect || d.ReasonCode != tc.reason {
				t.Fatalf("decided %v %q, want %v %q", d.Effect, d.ReasonCode, tc.effect, tc.reason)
			}
			if d.Permitted() {
				t.Fatal("a guard failure permitted")
			}
		})
	}
}

// A service identity has no connection to check and may have no known initiator; the
// policies decide.
func TestAServiceIdentityNeedsNoConnection(t *testing.T) {
	set := mustSet(t, `{"id":"svc","ruleCombining":"first-applicable","rules":[{"id":"svc","effect":"Permit","when":{"identityModes":["service"]}}]}`)
	r := delegated()
	r.Actor.IdentityMode = authz.ModeService
	r.Actor.Connection = nil
	r.Subject = authz.Subject{}
	if d := set.Evaluate(context.Background(), r); !d.Permitted() {
		t.Fatalf("decided %v %q", d.Effect, d.ReasonCode)
	}
}

func TestDenyOverridesAcrossPolicies(t *testing.T) {
	deny := `{"id":"no-deletes","priority":50,"target":{"operations":["delete-file"]},"ruleCombining":"first-applicable",
	  "rules":[{"id":"never","effect":"Deny","reasonCode":"DELETE_FORBIDDEN"}]}`
	// Either order of publication decides the same.
	for _, docs := range [][]string{{permitMembers, deny}, {deny, permitMembers}} {
		d := mustSet(t, docs...).Evaluate(context.Background(), delegated())
		if d.Effect != authz.Deny || d.ReasonCode != "DELETE_FORBIDDEN" || d.PolicyID != "no-deletes" {
			t.Fatalf("decided %v %q by %s", d.Effect, d.ReasonCode, d.PolicyID)
		}
	}
}

func TestIndeterminateBeatsPermitButNotDeny(t *testing.T) {
	broken := `{"id":"broken","ruleCombining":"first-applicable",
	  "rules":[{"id":"cmp","effect":"Permit","condition":"subject.id > 5"}]}`
	d := mustSet(t, permitMembers, broken).Evaluate(context.Background(), delegated())
	if d.Effect != authz.Indeterminate || d.Permitted() {
		t.Fatalf("a condition that cannot be decided let a permit through: %v %q", d.Effect, d.ReasonCode)
	}
	deny := `{"id":"deny","ruleCombining":"first-applicable","rules":[{"id":"d","effect":"Deny"}]}`
	d = mustSet(t, permitMembers, broken, deny).Evaluate(context.Background(), delegated())
	if d.Effect != authz.Deny {
		t.Fatalf("a deny lost to an indeterminate: %v", d.Effect)
	}
}

func TestFirstApplicableStopsAtTheFirstRuleThatAnswers(t *testing.T) {
	src := `{"id":"ordered","ruleCombining":"first-applicable","rules":[
	  {"id":"managers","effect":"Permit","when":{"subjectRolesAny":["manager"]}},
	  {"id":"rest","effect":"Deny","reasonCode":"NOT_A_MANAGER"}]}`
	set := mustSet(t, src)
	r := delegated()
	if d := set.Evaluate(context.Background(), r); d.Effect != authz.Deny || d.ReasonCode != "NOT_A_MANAGER" {
		t.Fatalf("non-manager: %v %q", d.Effect, d.ReasonCode)
	}
	r.Subject.Roles = append(r.Subject.Roles, "manager")
	if d := set.Evaluate(context.Background(), r); !d.Permitted() {
		t.Fatalf("manager: %v %q", d.Effect, d.ReasonCode)
	}
}

func TestDenyOverridesWithinAPolicy(t *testing.T) {
	src := `{"id":"both","ruleCombining":"deny-overrides","rules":[
	  {"id":"members","effect":"Permit","when":{"subjectGroupsAny":["grp_documents"]}},
	  {"id":"no-hr","effect":"Deny","reasonCode":"HR_LOCKED","when":{"resourcePrefixes":["sharepoint:site=contoso.sharepoint.com:/sites/hr"]}}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); d.Effect != authz.Deny || d.ReasonCode != "HR_LOCKED" {
		t.Fatalf("%v %q", d.Effect, d.ReasonCode)
	}
}

func TestMatchersAreConjunctive(t *testing.T) {
	src := `{"id":"narrow","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","when":{
	  "subjectGroupsAny":["grp_documents"],"operations":["get-file"],"workers":["sp-prod"]}}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); d.Effect != authz.NotApplicable {
		t.Fatalf("a rule matched although its operation did not: %v", d.Effect)
	}
}

func TestTargetsSelectPolicies(t *testing.T) {
	src := `{"id":"mail-only","target":{"workerTypes":["mail"]},"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); d.Effect != authz.NotApplicable {
		t.Fatalf("a policy for another Worker Type applied: %v", d.Effect)
	}
}

func TestAFEELConditionDecides(t *testing.T) {
	src := `{"id":"feel","ruleCombining":"first-applicable","rules":[
	  {"id":"own-process","effect":"Permit","condition":"context.processId = \"document-approval\" and list contains(subject.groups, \"grp_documents\")"}]}`
	set := mustSet(t, src)
	if d := set.Evaluate(context.Background(), delegated()); !d.Permitted() {
		t.Fatalf("decided %v %q %v", d.Effect, d.ReasonCode, d.Trace)
	}
	r := delegated()
	r.Context.ProcessID = "other"
	if d := set.Evaluate(context.Background(), r); d.Effect != authz.NotApplicable {
		t.Fatalf("a false condition answered %v", d.Effect)
	}
}

func TestADMNRuleDecides(t *testing.T) {
	src := `{"id":"table","ruleCombining":"first-applicable","rules":[{"id":"t","decision":"access"}],"dmn":` + jsonString(accessTable) + `}`
	set := mustSet(t, src)
	r := delegated()
	if d := set.Evaluate(context.Background(), r); d.Effect != authz.Deny || d.ReasonCode != "DELETE_NOT_ALLOWED" {
		t.Fatalf("employee: %v %q", d.Effect, d.ReasonCode)
	}
	r.Subject.Roles = []string{"manager"}
	if d := set.Evaluate(context.Background(), r); !d.Permitted() || d.ReasonCode != "MANAGER_DELETE" {
		t.Fatalf("manager: %v %q", d.Effect, d.ReasonCode)
	}
	r.Action.Operation = "get-file"
	if d := set.Evaluate(context.Background(), r); d.Effect != authz.Indeterminate {
		t.Fatalf("a table with no matching row must not decide: %v %q", d.Effect, d.ReasonCode)
	}
}

func TestAPermitCarriesItsObligationAndAnApprovalSatisfiesIt(t *testing.T) {
	src := `{"id":"approve","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit",
	  "obligations":[{"id":"manager","type":"approval","approverGroup":"grp_managers","approvals":1,"validForSeconds":86400,"excludeRequester":true}]}]}`
	set := mustSet(t, src)
	approval := func(who string, groups ...string) authz.Approval {
		return authz.Approval{ID: "apr_" + who, Digest: "d1", ApproverID: who, ApproverGroups: groups,
			Outcome: authz.ApprovalGranted, DecidedAt: t0 - hour}
	}
	for _, tc := range []struct {
		name      string
		approvals []authz.Approval
		subject   string
		initiator string
		effect    authz.Effect
		reason    string
	}{
		{"none", nil, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"a manager", []authz.Approval{approval("usr_mgr", "grp_managers")}, "usr_alice", "usr_alice", authz.Permit, "PERMIT"},
		{"self approval", []authz.Approval{approval("usr_alice", "grp_managers")}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"the requester under a service identity", []authz.Approval{approval("usr_req", "grp_managers")}, "usr_alice", "usr_req", authz.Deny, "APPROVAL_REQUIRED"},
		{"not in the group (now)", []authz.Approval{approval("usr_mgr", "grp_other")}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"a service principal", []authz.Approval{approval("system:mcp", "grp_managers")}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"another action", []authz.Approval{func() authz.Approval { a := approval("usr_mgr", "grp_managers"); a.Digest = "d2"; return a }()}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"revoked", []authz.Approval{func() authz.Approval { a := approval("usr_mgr", "grp_managers"); a.Revoked = true; return a }()}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"expired", []authz.Approval{func() authz.Approval { a := approval("usr_mgr", "grp_managers"); a.DecidedAt = t0 - 25*hour; return a }()}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_EXPIRED"},
		{"decided in the future", []authz.Approval{func() authz.Approval { a := approval("usr_mgr", "grp_managers"); a.DecidedAt = t0 + hour; return a }()}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REQUIRED"},
		{"rejected", []authz.Approval{func() authz.Approval {
			a := approval("usr_mgr", "grp_managers")
			a.Outcome = authz.ApprovalRejected
			return a
		}()}, "usr_alice", "usr_alice", authz.Deny, "APPROVAL_REJECTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := delegated()
			r.Subject.ID = tc.subject
			r.Actor.Connection.OwnerID = tc.subject
			r.Context.InitiatedBy = tc.initiator
			r.Context.Approvals = tc.approvals
			d := set.Evaluate(context.Background(), r)
			if d.Effect != tc.effect || d.ReasonCode != tc.reason {
				t.Fatalf("decided %v %q, want %v %q", d.Effect, d.ReasonCode, tc.effect, tc.reason)
			}
			if len(d.Obligations) != 1 {
				t.Fatalf("obligations = %+v", d.Obligations)
			}
			ob := d.Obligations[0]
			if ob.Satisfied != (tc.effect == authz.Permit) || ob.ID != "manager" || ob.PolicyID != "approve" {
				t.Errorf("obligation = %+v", ob)
			}
		})
	}
}

func TestDualApprovalNeedsTwoDistinctApprovers(t *testing.T) {
	src := `{"id":"dual","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit",
	  "obligations":[{"id":"two","type":"approval","approverGroup":"grp_admins","approvals":2,"validForSeconds":3600}]}]}`
	set := mustSet(t, src)
	grant := func(who string) authz.Approval {
		return authz.Approval{ID: "apr_" + who, Digest: "d1", ApproverID: who, ApproverGroups: []string{"grp_admins"},
			Outcome: authz.ApprovalGranted, DecidedAt: t0 - int64(time.Minute)}
	}
	r := delegated()
	r.Context.Approvals = []authz.Approval{grant("usr_a"), grant("usr_a")}
	if d := set.Evaluate(context.Background(), r); d.Permitted() {
		t.Fatal("one approver counted twice")
	}
	r.Context.Approvals = []authz.Approval{grant("usr_a"), grant("usr_b")}
	d := set.Evaluate(context.Background(), r)
	if !d.Permitted() {
		t.Fatalf("two approvers: %v %q", d.Effect, d.ReasonCode)
	}
	if got := strings.Join(d.Obligations[0].ApprovalsUsed, ","); got != "apr_usr_a,apr_usr_b" {
		t.Errorf("approvals used = %s", got)
	}
}

func TestFreshAuthentication(t *testing.T) {
	src := `{"id":"fresh","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit",
	  "obligations":[{"id":"recent","type":"fresh-authentication","maxAgeSeconds":7200}]}]}`
	set := mustSet(t, src)
	r := delegated()
	if d := set.Evaluate(context.Background(), r); !d.Permitted() {
		t.Fatalf("an hour-old login: %v %q", d.Effect, d.ReasonCode)
	}
	r.Actor.Connection.LastInteractiveAuth = t0 - 3*hour
	if d := set.Evaluate(context.Background(), r); d.Effect != authz.Deny || d.ReasonCode != "REAUTHENTICATION_REQUIRED" {
		t.Fatalf("a stale login: %v %q", d.Effect, d.ReasonCode)
	}
}

func TestADecisionIsAFunctionOfItsRequest(t *testing.T) {
	src := `{"id":"feel","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"context.time > date and time(\"2026-01-01T00:00:00Z\")"}]}`
	set := mustSet(t, src)
	a := set.Evaluate(context.Background(), delegated())
	b := set.Evaluate(context.Background(), delegated())
	if a.Effect != b.Effect || a.ReasonCode != b.ReasonCode || len(a.Trace) != len(b.Trace) {
		t.Fatalf("two evaluations disagreed: %+v / %+v", a, b)
	}
	if !a.Permitted() {
		t.Fatalf("decided %v %q %v", a.Effect, a.ReasonCode, a.Trace)
	}
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// accessTable reads the flat inputs a policy's DMN rule is given.
const accessTable = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_access" name="Access" namespace="http://atlas/authz">
  <inputData id="id_op" name="operation"/>
  <inputData id="id_roles" name="subjectRoles"/>
  <decision id="access" name="access">
    <informationRequirement><requiredInput href="#id_op"/></informationRequirement>
    <informationRequirement><requiredInput href="#id_roles"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="FIRST">
      <input id="in1"><inputExpression id="ie1" typeRef="string"><text>operation</text></inputExpression></input>
      <input id="in2"><inputExpression id="ie2" typeRef="boolean"><text>list contains(subjectRoles, "manager")</text></inputExpression></input>
      <output id="out1" name="effect" typeRef="string"/>
      <output id="out2" name="reasonCode" typeRef="string"/>
      <rule id="r1"><inputEntry id="e11"><text>"delete-file"</text></inputEntry><inputEntry id="e12"><text>true</text></inputEntry><outputEntry id="o11"><text>"Permit"</text></outputEntry><outputEntry id="o12"><text>"MANAGER_DELETE"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e21"><text>"delete-file"</text></inputEntry><inputEntry id="e22"><text>-</text></inputEntry><outputEntry id="o21"><text>"Deny"</text></outputEntry><outputEntry id="o22"><text>"DELETE_NOT_ALLOWED"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`
