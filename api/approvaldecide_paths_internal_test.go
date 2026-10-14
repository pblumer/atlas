package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// approvalDecidePathsStillOpen reports whether a task is still open, read on the loop.
func approvalDecidePathsStillOpen(t *testing.T, srv *Server, key uint64) bool {
	t.Helper()
	return approvalsPathsTaskKeys(t, srv)[key]
}

// TestApprovalDecideRefusesABodyThatCannotBeRead: a decision whose body broke off in
// transit is not a decision. Acting on what arrived — or on nothing — could approve
// lines nobody chose, so the request is refused and every task stays open.
func TestApprovalDecideRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approvals/decide", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Fatalf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if !approvalDecidePathsStillOpen(t, srv, key) {
		t.Error("a refused decision still completed the approval")
	}
}

// TestApprovalDecideFailsWhenTheOrdersCannotBeRead: which named tasks are approvals
// — and of which order — is read from the orders. With that store unreadable the
// decision cannot know what it would be deciding, so it fails as a whole before
// completing anything, rather than skipping every line as "not an approval".
func TestApprovalDecideFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/approvals/decide",
		fmt.Sprintf(`{"taskKeys":[%d],"approved":true}`, key), "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read approvals") {
		t.Fatalf("unreadable order store: %d (%s), want 500 'read approvals'", code, body)
	}
	if !approvalDecidePathsStillOpen(t, srv, key) {
		t.Error("the approval was completed although the decision failed")
	}
}
