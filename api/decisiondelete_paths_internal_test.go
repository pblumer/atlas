package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecisionDeleteStopsWhenTheOtherRecordsCannotBeRead: whether a version may go
// depends on every other version of the same decision — a current version with
// history behind it is refused. With one record unreadable that cannot be decided,
// and the delete must not go ahead on a guess.
func TestDecisionDeleteStopsWhenTheOtherRecordsCannotBeRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	rep := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	if err := os.WriteFile(filepath.Join(srv.decisionDeploys.Dir(), "99.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, body := deleteDecisionDeployment(t, x, rep.Key)
	if code != http.StatusInternalServerError || !strings.Contains(body, "read decision deployment") {
		t.Fatalf("delete over an unreadable record: %d (%s), want 500", code, body)
	}
	if _, err := os.Stat(srv.decisionDeploys.fileFor(rep.Key)); err != nil {
		t.Errorf("the deployment's record is gone although the delete failed: %v", err)
	}
}

// TestDecisionDeleteNamesEveryPinnedDefinition: a refusal over several definitions
// that would be left unable to evaluate says so in the plural, and names each,
// because the remedy is to go and deal with every one of them.
func TestDecisionDeleteNamesEveryPinnedDefinition(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	rep := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	deployProcess(t, x, eligibilityProcess("orders", "latest"))
	deployProcess(t, x, eligibilityProcess("billing", "latest"))

	code, body := deleteDecisionDeployment(t, x, rep.Key)
	if code != http.StatusConflict {
		t.Fatalf("delete = %d (%s), want 409", code, body)
	}
	for _, want := range []string{"deployed processes evaluate", "those definitions", "orders", "billing"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal = %s, want it to contain %q", body, want)
		}
	}
}

// TestDecisionDeleteIsNotHeldBackByAnotherDecision: the history guard is per decision
// id. Another deployment of a different decision is not history of this one, and
// must neither block the delete nor be touched by it.
func TestDecisionDeleteIsNotHeldBackByAnotherDecision(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	elig := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	deployOneDecision(t, x, "", discountDMN)

	if code, body := deleteDecisionDeployment(t, x, elig.Key); code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s), want 204", code, body)
	}
	if rows := listDecisionDeployments(t, x, "?decisionId=discount"); len(rows) != 1 {
		t.Errorf("discount deployments = %+v, want the unrelated one untouched", rows)
	}
	if rows := listDecisionDeployments(t, x, "?decisionId=eligibility"); len(rows) != 0 {
		t.Errorf("eligibility deployments = %+v, want it gone", rows)
	}
}
