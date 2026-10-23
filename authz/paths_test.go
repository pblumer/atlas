package authz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/authz"
)

func TestEffectsReadAsTheirNames(t *testing.T) {
	for e, want := range map[authz.Effect]string{
		authz.Permit: "Permit", authz.Deny: "Deny", authz.NotApplicable: "NotApplicable", authz.Indeterminate: "Indeterminate",
	} {
		if e.String() != want {
			t.Errorf("%d.String() = %s", e, e.String())
		}
		if parsed, ok := authz.ParseEffect(want); want != "Indeterminate" && (!ok || parsed != e) {
			t.Errorf("ParseEffect(%s) = %v %v", want, parsed, ok)
		}
	}
	if _, ok := authz.ParseEffect("Indeterminate"); ok {
		t.Error("a rule may answer Indeterminate on purpose")
	}
}

func TestASetListsItsPoliciesInEvaluationOrder(t *testing.T) {
	set := mustSet(t,
		`{"id":"b","priority":1,"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`,
		`{"id":"a","priority":1,"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`,
		`{"id":"c","priority":0,"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`)
	var got []string
	for _, p := range set.Policies() {
		got = append(got, p.ID())
		if p.Version() < 1 {
			t.Errorf("%s has version %d", p.ID(), p.Version())
		}
	}
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("order = %v", got)
	}
	var none *authz.Set
	if none.Policies() != nil {
		t.Error("a nil set listed policies")
	}
}

func TestANilRequestIsIndeterminate(t *testing.T) {
	if d := mustSet(t).Evaluate(context.Background(), nil); d.Effect != authz.Indeterminate {
		t.Fatalf("%v", d.Effect)
	}
}

func TestDenyOverridesWithinAPolicyReportsIndeterminateBeforePermit(t *testing.T) {
	src := `{"id":"p","ruleCombining":"deny-overrides","rules":[
	  {"id":"ok","effect":"Permit"},
	  {"id":"broken","effect":"Permit","condition":"subject.id > 5"}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); d.Effect != authz.Indeterminate {
		t.Fatalf("%v %q", d.Effect, d.ReasonCode)
	}
	src = `{"id":"p","ruleCombining":"deny-overrides","rules":[
	  {"id":"skip","effect":"Deny","when":{"operations":["get-file"]}},
	  {"id":"ok","effect":"Permit","reasonCode":"FINE"}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); !d.Permitted() || d.ReasonCode != "FINE" {
		t.Fatalf("%v %q", d.Effect, d.ReasonCode)
	}
}

func TestResourcePrefixesThatDoNotMatchLeaveTheRuleOut(t *testing.T) {
	src := `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","when":{"resourcePrefixes":["sharepoint:site=other"]}}]}`
	if d := mustSet(t, src).Evaluate(context.Background(), delegated()); d.Effect != authz.NotApplicable {
		t.Fatalf("%v", d.Effect)
	}
}

// A decision table may answer in any shape a model can produce; only an effect it may
// give, with a well-formed reason, decides.
func TestADecisionAnswersOnlyInWordsItMayUse(t *testing.T) {
	model := func(outputs string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d" name="d" namespace="http://atlas/authz">
  <inputData id="id_op" name="operation"/>
  <decision id="access" name="access">
    <informationRequirement><requiredInput href="#id_op"/></informationRequirement>
    <literalExpression><text>` + outputs + `</text></literalExpression>
  </decision>
</definitions>`
	}
	for _, tc := range []struct {
		name, expr string
		effect     authz.Effect
		reason     string
	}{
		{"a bare effect", `"Permit"`, authz.Permit, "PERMIT"},
		{"a bare deny", `"Deny"`, authz.Deny, "POLICY_DENY"},
		{"not applicable", `"NotApplicable"`, authz.NotApplicable, "NO_APPLICABLE_POLICY"},
		{"a context", `{effect: "Deny", reasonCode: "NO"}`, authz.Deny, "NO"},
		{"an unknown word", `"Allow"`, authz.Indeterminate, "POLICY_INDETERMINATE"},
		{"a malformed reason", `{effect: "Deny", reasonCode: "no"}`, authz.Indeterminate, "POLICY_INDETERMINATE"},
		{"a number", `42`, authz.Indeterminate, "POLICY_INDETERMINATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","decision":"access"}],"dmn":` + jsonString(model(tc.expr)) + `}`
			d := mustSet(t, src).Evaluate(context.Background(), delegated())
			if d.Effect != tc.effect || d.ReasonCode != tc.reason {
				t.Fatalf("decided %v %q %+v", d.Effect, d.ReasonCode, d.Trace)
			}
		})
	}
}

// Two rules over one request build its inputs once; the answer must not depend on
// whether they were already built.
func TestInputsAreSharedAcrossRules(t *testing.T) {
	src := `{"id":"p","ruleCombining":"deny-overrides","rules":[
	  {"id":"a","effect":"Permit","condition":"subject.known"},
	  {"id":"b","effect":"Permit","condition":"actor.worker = \"sp-prod\""},
	  {"id":"c","decision":"access"},
	  {"id":"d","decision":"access"}],"dmn":` + jsonString(accessTable) + `}`
	r := delegated()
	r.Subject.Roles = []string{"manager"}
	if d := mustSet(t, src).Evaluate(context.Background(), r); !d.Permitted() {
		t.Fatalf("%v %q %+v", d.Effect, d.ReasonCode, d.Trace)
	}
}

func TestADocumentIsOneDocument(t *testing.T) {
	if _, err := authz.ParseDocument([]byte(permitMembers + `{}`)); err == nil {
		t.Error("trailing content was accepted")
	}
}

func TestPublicationRefusesMoreMistakes(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"malformed model", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","decision":"x"}],"dmn":"<definitions"}`, "dmn"},
		{"bad rule id", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"R 1","effect":"Permit"}]}`, "rule id"},
		{"not applicable as an effect", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"NotApplicable"}]}`, "effect"},
		{"rule identity mode", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","when":{"identityModes":["god"]}}]}`, "identity mode"},
		{"bad obligation id", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"O","type":"approval"}]}]}`, "obligation id"},
		{"duplicate obligation", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[
		   {"id":"o","type":"fresh-authentication","maxAgeSeconds":60},{"id":"o","type":"fresh-authentication","maxAgeSeconds":60}]}]}`, "twice"},
		{"condition calls nothing known", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"frobnicate(subject)"}]}`, "condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := authz.ParseDocument([]byte(tc.src))
			if err == nil {
				_, _, err = authz.CompilePolicy(context.Background(), doc, 1)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	doc, _ := authz.ParseDocument([]byte(permitMembers))
	if _, _, err := authz.CompilePolicy(context.Background(), doc, 0); err == nil {
		t.Error("version 0 was accepted")
	}
	bad, _ := authz.ParseDocument([]byte(`{"id":"p","ruleCombining":"nope","rules":[{"id":"r","effect":"Permit"}]}`))
	if _, err := authz.CompileSet(context.Background(), []authz.Published{{Document: bad, Version: 1}}); err == nil {
		t.Error("a set compiled a broken policy")
	}
}

func TestADigestOfSomethingWithoutAJSONImageFails(t *testing.T) {
	if _, err := authz.Digest("t", "w", "o", "r", map[string]any{"f": func() {}}); err == nil {
		t.Error("a function was digested")
	}
}

func TestAnUnmetFreshAuthenticationWithoutAConnection(t *testing.T) {
	src := `{"id":"fresh","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit",
	  "obligations":[{"id":"recent","type":"fresh-authentication","maxAgeSeconds":60}]}]}`
	r := delegated()
	r.Actor.IdentityMode = authz.ModeService
	r.Actor.Connection = nil
	if d := mustSet(t, src).Evaluate(context.Background(), r); d.Effect != authz.Deny || d.ReasonCode != "REAUTHENTICATION_REQUIRED" {
		t.Fatalf("%v %q", d.Effect, d.ReasonCode)
	}
}

func TestOversizedApproverGroupsAreRefused(t *testing.T) {
	r := delegated()
	r.Context.Approvals = []authz.Approval{{ApproverGroups: make([]string, authz.MaxListLen+1)}}
	if d := mustSet(t).Evaluate(context.Background(), r); d.ReasonCode != "INPUT_TOO_LARGE" {
		t.Fatalf("%v %q", d.Effect, d.ReasonCode)
	}
}
