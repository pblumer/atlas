package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/authz"
)

// The worked example of ADR-0475 lives under
// docs/architecture/identity-aware-execution as a model and two policies. Documentation
// that does not compile teaches the wrong thing, so the model is deployed here and the
// policies are compiled and asked the questions their descriptions answer.

const identitySampleDir = "../docs/architecture/identity-aware-execution"

func TestTheIdentityAwareSampleModelDeploys(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(identitySampleDir, "dokument-loeschen.bpmn"))
	if err != nil {
		t.Fatal(err)
	}
	srv := newServerWithOptions(t)
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", string(src), "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", code, raw)
	}
}

func TestTheIdentityAwareSamplePoliciesDecideWhatTheySay(t *testing.T) {
	var published []authz.Published
	for i, name := range []string{"hr-documents.policy.json", "sharepoint-roles.policy.json"} {
		raw, err := os.ReadFile(filepath.Join(identitySampleDir, name))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := authz.ParseDocument(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, warnings, err := authz.CompilePolicy(context.Background(), doc, 1); err != nil || len(warnings) != 0 {
			t.Fatalf("%s: %v %v", name, err, warnings)
		}
		published = append(published, authz.Published{Document: doc, Version: i + 1})
	}
	set, err := authz.CompileSet(context.Background(), published)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC).UnixNano()
	req := func(op string, roles ...string) *authz.Request {
		return &authz.Request{
			Subject: authz.Subject{ID: "usr_alice", Known: true, Roles: roles, Groups: []string{"grp_documents"}},
			Actor: authz.Actor{IdentityMode: authz.ModeDelegatedPerJob, Worker: "sp-prod", WorkerType: "sharepoint",
				Connection: &authz.Connection{ID: "con_1", OwnerID: "usr_alice", Status: authz.ConnectionActive,
					AllowedWorkers: []string{"sp-prod"}, LastInteractiveAuth: now - int64(time.Hour)}},
			Action:   authz.Action{Operation: op, Digest: "d"},
			Resource: authz.Resource{Type: "sharepoint", ID: "sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Documents;path=/a.docx"},
			Context:  authz.Context{Time: now, ProcessID: "dokument-loeschen", InitiatedBy: "usr_alice"},
		}
	}
	for _, tc := range []struct {
		name   string
		r      *authz.Request
		effect authz.Effect
		reason string
	}{
		// The role table denies a member's delete outright, and a Deny wins over the
		// HR policy's permit-with-approval.
		{"a member deletes", req("delete-file"), authz.Deny, "DELETE_NOT_ALLOWED"},
		// A manager's delete passes the table and still needs another manager's approval.
		{"a manager deletes", req("delete-file", "manager"), authz.Deny, "APPROVAL_REQUIRED"},
		{"anybody reads", req("get-file"), authz.Permit, "READ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := set.Evaluate(context.Background(), tc.r)
			if d.Effect != tc.effect || d.ReasonCode != tc.reason {
				t.Fatalf("decided %v %q, trace %+v", d.Effect, d.ReasonCode, d.Trace)
			}
		})
	}
}
