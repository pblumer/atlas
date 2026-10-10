package mcp_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestTryDecisionSendsOnlyWhatWasGiven: trying a model is stateless, so the whole
// request is the body. Without a decisionId the server only describes the model,
// which is why an omitted one must stay omitted rather than travel as "".
func TestTryDecisionSendsOnlyWhatWasGiven(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"decisions":[]}`)

	text, isErr, c := rec.call(t, "atlas_try_decision", map[string]any{})
	wantRefusal(t, "atlas_try_decision", text, isErr, c, "argument: xml")

	_, isErr, c = rec.call(t, "atlas_try_decision", map[string]any{"xml": "<definitions/>"})
	if isErr {
		t.Fatal("atlas_try_decision reported an error")
	}
	wantCall(t, "atlas_try_decision", c, http.MethodPost, "/api/v1/decisions/evaluate", "")
	if body := bodyObject(t, c); len(body) != 1 || body["xml"] != "<definitions/>" {
		t.Fatalf("describe-only body = %v, want the model alone", body)
	}

	_, _, c = rec.call(t, "atlas_try_decision", map[string]any{
		"xml": "<definitions/>", "decisionId": "eligibility", "inputs": map[string]any{"age": 30},
	})
	body := bodyObject(t, c)
	if body["decisionId"] != "eligibility" {
		t.Fatalf("evaluate body = %v, want decisionId eligibility", body)
	}
	if in, _ := body["inputs"].(map[string]any); in["age"] != float64(30) {
		t.Fatalf("evaluate body inputs = %v, want age 30", body["inputs"])
	}
}

// TestDeployDecisionFilesProvenanceAsQuery: the document is the body, so where it is
// filed and which model it was authored as ride in the query — and only when given.
func TestDeployDecisionFilesProvenanceAsQuery(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusCreated, `{"key":5}`)

	text, isErr, c := rec.call(t, "atlas_deploy_decision", map[string]any{"xml": "<definitions/>"})
	if isErr || text != `{"key":5}` {
		t.Fatalf("atlas_deploy_decision = (%q, isErr=%v), want the API body", text, isErr)
	}
	wantCall(t, "atlas_deploy_decision", c, http.MethodPost, "/api/v1/decision-deployments", "")
	if c.ContentType != "application/xml" || string(c.Body) != "<definitions/>" {
		t.Fatalf("deploy request = (%q, %q), want the DMN document verbatim", c.ContentType, c.Body)
	}

	_, _, c = rec.call(t, "atlas_deploy_decision",
		map[string]any{"xml": "<definitions/>", "projectId": "app-1", "modelRef": "pricing"})
	wantCall(t, "atlas_deploy_decision", c, http.MethodPost, "/api/v1/decision-deployments",
		"modelRef=pricing&projectId=app-1")
}

// TestDecisionDeploymentsNarrowsByQuery: both narrowings are optional and combine.
func TestDecisionDeploymentsNarrowsByQuery(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `[]`)
	_, _, c := rec.call(t, "atlas_decision_deployments",
		map[string]any{"applicationId": "app-1", "decisionId": "pricing"})
	wantCall(t, "atlas_decision_deployments", c, http.MethodGet, "/api/v1/decision-deployments",
		"applicationId=app-1&decisionId=pricing")
}

// TestDeployedDecisionModelReadsTheDeploymentsSource: the deployment's own XML, not the
// design-time file behind a handle.
func TestDeployedDecisionModelReadsTheDeploymentsSource(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `"<definitions/>"`)
	_, isErr, c := rec.call(t, "atlas_deployed_decision_model", map[string]any{"key": "77"})
	if isErr {
		t.Fatal("atlas_deployed_decision_model reported an error")
	}
	wantCall(t, "atlas_deployed_decision_model", c, http.MethodGet, "/api/v1/decision-deployments/77/xml", "")
}

// TestSaveFormNamesItWhenAsked: an optional display name travels with the schema.
func TestSaveFormNamesItWhenAsked(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"id":"intake"}`)
	_, isErr, c := rec.call(t, "atlas_save_form", map[string]any{
		"id": "intake", "name": "Intake form", "schema": map[string]any{"type": "default"},
	})
	if isErr {
		t.Fatal("atlas_save_form reported an error")
	}
	if body := bodyObject(t, c); body["name"] != "Intake form" || body["id"] != "intake" {
		t.Fatalf("save form body = %v, want id intake named Intake form", body)
	}
}

// TestDesignTimeDeletesSurfaceARefusal: the idempotent deletes confirm only what the
// server did. A refusal (here, a caller who is not the owner) must come back as the
// server's reason, never as "deleted".
func TestDesignTimeDeletesSurfaceARefusal(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusForbidden, `{"error":"only the owner may delete this"}`)
	for _, tc := range []struct{ tool, arg, path string }{
		{"atlas_delete_project", "id", "/api/v1/projects/p-1"},
		{"atlas_delete_application", "id", "/api/v1/applications/p-1"},
		{"atlas_delete_draft", "id", "/api/v1/drafts/p-1"},
		{"atlas_delete_information_model", "id", "/api/v1/infomodel/models/p-1"},
		{"atlas_delete_value_stream", "key", "/api/v1/value-streams/p-1"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			text, isErr, c := rec.call(t, tc.tool, map[string]any{tc.arg: "p-1"})
			if !isErr || !strings.Contains(text, "only the owner may delete this") {
				t.Fatalf("%s = (%q, isErr=%v), want the server's refusal as a tool error", tc.tool, text, isErr)
			}
			wantCall(t, tc.tool, c, http.MethodDelete, tc.path, "")
		})
	}
}

// TestTaskToolsRefuseUnusableArguments: a page size that is not positive and form data
// that is not an object both stop the tool before the server is asked — the second
// would otherwise complete a human task with nothing the form could have produced.
func TestTaskToolsRefuseUnusableArguments(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)

	text, isErr, c := rec.call(t, "atlas_list_tasks", map[string]any{"limit": 0})
	wantRefusal(t, "atlas_list_tasks", text, isErr, c, "limit")

	text, isErr, c = rec.call(t, "atlas_complete_task", map[string]any{"key": 8, "variables": "approved"})
	wantRefusal(t, "atlas_complete_task", text, isErr, c, "variables")
}
