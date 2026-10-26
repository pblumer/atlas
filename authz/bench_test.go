package authz_test

import (
	"context"
	"testing"

	"github.com/pblumer/atlas/authz"
)

// The decision runs once per governed action, off the engine's run loop. These
// measure what one costs for the shapes a policy set realistically has: structured
// matchers only, a FEEL condition, a DMN table, and an approval obligation.

func benchSet(b *testing.B, docs ...string) *authz.Set {
	b.Helper()
	var published []authz.Published
	for i, src := range docs {
		doc, err := authz.ParseDocument([]byte(src))
		if err != nil {
			b.Fatal(err)
		}
		published = append(published, authz.Published{Document: doc, Version: i + 1})
	}
	set, err := authz.CompileSet(context.Background(), published)
	if err != nil {
		b.Fatal(err)
	}
	return set
}

func runBench(b *testing.B, set *authz.Set, r *authz.Request, want authz.Effect) {
	b.Helper()
	ctx := context.Background()
	if d := set.Evaluate(ctx, r); d.Effect != want {
		b.Fatalf("decided %v %q, want %v", d.Effect, d.ReasonCode, want)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set.Evaluate(ctx, r)
	}
}

func BenchmarkEvaluateMatchersOnly(b *testing.B) {
	set := benchSet(b, permitMembers,
		`{"id":"mail","target":{"workerTypes":["mail"]},"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`,
		`{"id":"jira","target":{"workerTypes":["jira"]},"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`)
	runBench(b, set, delegated(), authz.Permit)
}

func BenchmarkEvaluateFEELCondition(b *testing.B) {
	set := benchSet(b, `{"id":"feel","ruleCombining":"first-applicable","rules":[
	  {"id":"r","effect":"Permit","condition":"context.processId = \"document-approval\" and list contains(subject.groups, \"grp_documents\")"}]}`)
	runBench(b, set, delegated(), authz.Permit)
}

func BenchmarkEvaluateDMNRule(b *testing.B) {
	set := benchSet(b, `{"id":"table","ruleCombining":"first-applicable","rules":[{"id":"t","decision":"access"}],"dmn":`+jsonString(accessTable)+`}`)
	r := delegated()
	r.Subject.Roles = []string{"manager"}
	runBench(b, set, r, authz.Permit)
}

func BenchmarkEvaluateApprovalObligation(b *testing.B) {
	set := benchSet(b, `{"id":"approve","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit",
	  "obligations":[{"id":"manager","type":"approval","approverGroup":"grp_managers","approvals":1,"validForSeconds":86400,"excludeRequester":true}]}]}`)
	r := delegated()
	r.Context.Approvals = []authz.Approval{{ID: "apr_1", Digest: "d1", ApproverID: "usr_mgr",
		ApproverGroups: []string{"grp_managers"}, Outcome: authz.ApprovalGranted, DecidedAt: t0 - hour}}
	runBench(b, set, r, authz.Permit)
}
