package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// A person's approval worklist is every open approval they may answer and every one
// asked on their behalf, across instances, with their own standing answer picked out
// (ADR-0475 §9). Somebody who is neither sees nothing, a machine principal has no
// worklist, and an expired request leaves it.
func TestAPersonsWorklistHoldsTheApprovalsTheyMayAnswer(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()
	job, lease := r.lease()
	code, a := r.authorize(r.internal, job, lease)
	if code != http.StatusOK || a.ApprovalKey == 0 {
		t.Fatalf("authorize: %d %+v", code, a)
	}
	carol := &httpapi.Principal{UserID: "usr_carol", Username: "carol", Roles: []string{RoleUser}, Session: true}
	list := func(p *httpapi.Principal) []myApprovalView {
		t.Helper()
		code, raw := r.as(p, r.srv.handleMyActionApprovals, http.MethodGet, "/", "", nil)
		if code != http.StatusOK {
			t.Fatalf("worklist of %s: %d %s", p.UserID, code, raw)
		}
		var page httpapi.Page[myApprovalView]
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if page.Truncated || page.Total != len(page.Items) {
			t.Fatalf("a worklist within its bound: %s", raw)
		}
		return page.Items
	}
	answer := func(outcome string) {
		t.Helper()
		if c, raw := r.as(r.bob, r.srv.handleDecideActionApproval, http.MethodPost, "/", `{"outcome":"`+outcome+`"}`,
			map[string]string{"key": strconv.FormatUint(pi, 10), "approval": strconv.FormatUint(a.ApprovalKey, 10)}); c != http.StatusOK {
			t.Fatalf("%s: %d %s", outcome, c, raw)
		}
	}

	bob := list(r.bob)
	if len(bob) != 1 {
		t.Fatalf("bob's worklist = %+v", bob)
	}
	got := bob[0]
	if got.ApprovalKey != a.ApprovalKey || got.ProcessInstanceKey != pi || got.Role != "approver" ||
		got.MyAnswer != nil || got.Granted != 0 || got.Rejected || !got.Open ||
		got.RequestedBy != "usr_alice" || got.RequestedByName != "alice" ||
		got.ApproverGroup != "grp_managers" || got.ApproverGroupName != "managers" ||
		got.ProcessID == "" || got.Operation == "" || got.Resource == "" || got.ActionDigest == "" {
		t.Fatalf("bob's item = %+v", got)
	}
	if alice := list(r.alice); len(alice) != 1 || alice[0].Role != "requester" || alice[0].ApprovalKey != a.ApprovalKey {
		t.Fatalf("alice's worklist = %+v", alice)
	}
	if c := list(carol); len(c) != 0 {
		t.Fatalf("carol, in no group, sees %+v", c)
	}

	// An answer is the caller's own, counted for everybody; a withdrawal takes it back.
	answer("granted")
	if b := list(r.bob)[0]; b.MyAnswer == nil || b.MyAnswer.Outcome != "granted" || b.Granted != 1 ||
		len(b.Answers) != 1 || b.Answers[0].Name != "bob" {
		t.Fatalf("after granting: %+v", b)
	}
	if al := list(r.alice)[0]; al.MyAnswer != nil || al.Granted != 1 {
		t.Fatalf("alice after bob granted: %+v", al)
	}
	answer("withdrawn")
	if b := list(r.bob)[0]; b.MyAnswer != nil || b.Granted != 0 || len(b.Answers) != 2 {
		t.Fatalf("after withdrawing: %+v", b)
	}
	answer("rejected")
	if b := list(r.bob)[0]; b.MyAnswer == nil || b.MyAnswer.Outcome != "rejected" || !b.Rejected || b.Granted != 0 {
		t.Fatalf("after rejecting: %+v", b)
	}

	// A worklist past its bound says so.
	bound := maxMyApprovals
	maxMyApprovals = 0
	_, raw := r.as(r.bob, r.srv.handleMyActionApprovals, http.MethodGet, "/", "", nil)
	maxMyApprovals = bound
	var page httpapi.Page[myApprovalView]
	if err := json.Unmarshal(raw, &page); err != nil || !page.Truncated || page.TotalExact || len(page.Items) != 0 {
		t.Fatalf("a worklist past its bound: %s", raw)
	}

	// At its expiry the request is closed and leaves every worklist.
	r.srv.do(func() {
		later := time.Now().Add(2 * time.Hour).UnixNano()
		r.srv.now = func() int64 { return later }
	})
	if b := list(r.bob); len(b) != 0 {
		t.Fatalf("an expired request is still listed: %+v", b)
	}

	// Only a person has a worklist: a machine principal is refused, and with sign-in
	// off there is nobody, so nothing to answer.
	if c, _ := r.as(r.internal, r.srv.handleMyActionApprovals, http.MethodGet, "/", "", nil); c != http.StatusForbidden {
		t.Errorf("a machine principal's worklist: %d, want 403", c)
	}
	if c, raw := r.as(nil, r.srv.handleMyActionApprovals, http.MethodGet, "/", "", nil); c != http.StatusOK ||
		string(raw) == "" || !json.Valid(raw) || !strings.Contains(string(raw), `"items":[]`) {
		t.Errorf("the worklist with sign-in off: %d %s", c, raw)
	}
}
