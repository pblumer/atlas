package worker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// TestJiraWorkerHoldsItsOwnCredential is ADR-0168's decision seen from the worker's
// side, and the point of giving Jira a worker at all (ADR-0201/0203): the site URL and
// the Atlassian credential come from *this process's* environment, and a leased job
// contributes only a worker name. A worker can therefore operate as an account the
// engine has never held.
func TestJiraWorkerHoldsItsOwnCredential(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS":     "acme",
		"ATLAS_JIRA_ACME_URL":       "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_EMAIL":     "bot@acme.example",
		"ATLAS_JIRA_ACME_API_TOKEN": "t0ken",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.JiraJobType]; !ok {
		t.Fatalf("no handler for %s; got %v", compiler.JiraJobType, built.Handlers)
	}
	// The names are reported to the engine on every poll: the Workers view subtracts
	// them from what deployed models reference, so a name nobody holds is visible.
	if len(built.Names) != 1 || built.Names[0] != "acme" {
		t.Errorf("names = %v, want the configured instance", built.Names)
	}
}

// A worker told to serve Jira but given no site must not lease every Jira job and fail
// it. It does not subscribe at all, so those tasks wait for a worker that can actually
// perform them — and it says so, because "jira is not served here" is the answer to why
// an issue task is waiting.
func TestAJiraWorkerWithNoConfiguredInstanceSimplyDoesNotServeJira(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(nil), "csv", "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.JiraJobType]; ok {
		t.Error("the worker subscribed to jira with no site to file against")
	}
	if _, ok := built.Handlers[compiler.CsvImportJobType]; !ok {
		t.Error("the kinds it *can* serve were dropped along with jira")
	}
	if len(built.Unconfigured) != 1 || built.Unconfigured[0] != "jira" {
		t.Errorf("unconfigured = %v, want [jira] so the startup line can say it", built.Unconfigured)
	}
}

// A *named* instance missing its credential is a mistake to report at startup, where
// the operator is still watching — not one to discover a retry budget later, per job.
// The message names both shapes, because which one is right depends on the product.
func TestAJiraInstanceMissingItsCredentialIsRefusedAtStartup(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   "https://acme.atlassian.net",
	}), "jira")
	if err == nil {
		t.Fatal("a jira instance with no credential was accepted")
	}
	for _, want := range []string{"ATLAS_JIRA_ACME_EMAIL", "ATLAS_JIRA_ACME_API_TOKEN", "ATLAS_JIRA_ACME_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

// Half a Cloud credential is refused rather than sent as an unauthenticated call whose
// 401 tells an operator nothing about which half is missing.
func TestAJiraInstanceWithHalfACloudCredentialIsRefused(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_EMAIL": "bot@acme.example",
		// no API token
	}), "jira")
	if err == nil || !strings.Contains(err.Error(), "ATLAS_JIRA_ACME_API_TOKEN") {
		t.Fatalf("error = %v, want the missing half named", err)
	}
}

// Both shapes at once is ambiguous about which product this worker thinks it is talking
// to — and the shape decides the authentication scheme, how an assignee is addressed,
// and which search endpoint is used. That is a question to answer at startup.
func TestAJiraInstanceWithBothCredentialShapesIsRefused(t *testing.T) {
	_, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS":     "acme",
		"ATLAS_JIRA_ACME_URL":       "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_EMAIL":     "bot@acme.example",
		"ATLAS_JIRA_ACME_API_TOKEN": "t0ken",
		"ATLAS_JIRA_ACME_TOKEN":     "pat",
	}), "jira")
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("error = %v, want both shapes at once refused", err)
	}
}

// The whole path a supervised or external Jira worker takes: its environment builds the
// client, a leased job carries only the resolved payload and no credential, and the
// issue lands in Jira with the model's project, type and summary. The result variable
// carries what Jira answered back to the token.
func TestAJiraWorkerCreatesTheIssueFromItsOwnEnvironment(t *testing.T) {
	var (
		gotAuth  string
		gotPath  string
		gotBody  map[string]any
		gotReqID string
	)
	jiraSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotReqID = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("X-Request-ID")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"10001","key":"OPS-42"}`))
	}))
	defer jiraSrv.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS":     "acme",
		"ATLAS_JIRA_ACME_URL":       jiraSrv.URL,
		"ATLAS_JIRA_ACME_EMAIL":     "bot@acme.example",
		"ATLAS_JIRA_ACME_API_TOKEN": "t0ken",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	exec, ok := built.Handlers[compiler.JiraJobType]
	if !ok {
		t.Fatalf("no handler for %s", compiler.JiraJobType)
	}

	vars, err := exec.Run(context.Background(), worker.Job{
		JobKey: 4711, Type: compiler.JiraJobType,
		Connector: &worker.ConnectorPayload{Kind: "jira", Fields: map[string]any{
			"connector": "acme", "operation": "create-issue",
			"project": "OPS", "issueType": "Task", "summary": "Disk full",
			"requestId": "4711", "resultVariable": "ticket",
		}},
	})
	if err != nil {
		t.Fatalf("run the jira job: %v", err)
	}
	if gotPath != "/rest/api/2/issue" {
		t.Errorf("path = %q, want the create endpoint", gotPath)
	}
	// The credential is the worker's own, not anything the job carried.
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("Authorization = %q, want the worker's own Cloud basic credential", gotAuth)
	}
	if gotReqID != "4711" {
		t.Errorf("X-Request-ID = %q, want the job key so an at-least-once replay is recognizable", gotReqID)
	}
	fields, _ := gotBody["fields"].(map[string]any)
	project, _ := fields["project"].(map[string]any)
	if project["key"] != "OPS" || fields["summary"] != "Disk full" {
		t.Errorf("fields = %+v, want the authored project and summary", fields)
	}
	ticket, _ := vars["ticket"].(map[string]any)
	if ticket["key"] != "OPS-42" {
		t.Errorf("completed with %v, want what Jira answered in the result variable", vars)
	}
}

// An operation Jira answers with no content completes with no variables at all. Writing
// a null would make "assigned" indistinguishable from "read something that was empty" —
// the same distinction the in-process handler makes, which is what sharing jira.Run
// between the two is for.
func TestAJiraJobWithNoContentCompletesWithNothing(t *testing.T) {
	jiraSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer jiraSrv.Close()

	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   jiraSrv.URL,
		"ATLAS_JIRA_ACME_TOKEN": "pat",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	vars, err := built.Handlers[compiler.JiraJobType].Run(context.Background(), worker.Job{
		JobKey: 12, Type: compiler.JiraJobType,
		Connector: &worker.ConnectorPayload{Kind: "jira", Fields: map[string]any{
			"connector": "acme", "operation": "assign-issue",
			"issue": "OPS-42", "assignee": "5b10a2", "requestId": "12",
		}},
	})
	if err != nil {
		t.Fatalf("run the jira job: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("completed with %v, want nothing written", vars)
	}
}

// A job that carried no resolved detail says so, naming the likely cause, rather than
// failing on an empty operation the operator has to work backwards from.
func TestAJiraJobWithNoResolvedDetailSaysSo(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_TOKEN": "pat",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	_, err = built.Handlers[compiler.JiraJobType].Run(context.Background(), worker.Job{
		JobKey: 1, Type: compiler.JiraJobType,
	})
	if err == nil || !strings.Contains(err.Error(), "offloading the jira kind") {
		t.Fatalf("error = %v, want it to name the likely cause", err)
	}
}

// A worker name this worker does not hold is refused by name, so an operator reads
// which instance is missing rather than a generic failure (ADR-0158).
func TestAJiraJobForAnUnheldConnectorNamesIt(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_TOKEN": "pat",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	_, err = built.Handlers[compiler.JiraJobType].Run(context.Background(), worker.Job{
		JobKey: 2, Type: compiler.JiraJobType,
		Connector: &worker.ConnectorPayload{Kind: "jira", Fields: map[string]any{
			"connector": "other", "operation": "get-issue", "issue": "OPS-1",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("error = %v, want the unheld worker named", err)
	}
}

// A payload whose shape does not match the resolved job is reported as such. It is the
// one failure the worker can meet that is neither Jira's nor the model's: the engine
// sent something this worker cannot read, and saying so beats acting on a zero value.
func TestAJiraJobWithAnUnreadableDetailSaysSo(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_JIRA_CONNECTORS": "acme",
		"ATLAS_JIRA_ACME_URL":   "https://acme.atlassian.net",
		"ATLAS_JIRA_ACME_TOKEN": "pat",
	}), "jira")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	_, err = built.Handlers[compiler.JiraJobType].Run(context.Background(), worker.Job{
		JobKey: 3, Type: compiler.JiraJobType,
		Connector: &worker.ConnectorPayload{Kind: "jira", Fields: map[string]any{
			"connector": "acme", "operation": "search",
			"maxResults": "not a number", // int32 on the far side
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Fatalf("error = %v, want it to name the unreadable payload", err)
	}
}
