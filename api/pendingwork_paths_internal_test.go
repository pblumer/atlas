package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// What is waiting for one person (ADR-0343), from the side the HTTP tests in
// pendingwork_http_test.go do not reach: an approval actually listed, a task that
// is not one left out, the item ceiling, single-user mode, and the answers given
// when a store cannot be read or the request carries nobody.

// pendingWorkPathsTaskBPMN is a process that parks on one user task; the id is
// what a line's approval kind names when the process is what decides it.
func pendingWorkPathsTaskBPMN(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="s"/><userTask id="decide" name="Decide"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </process>
</definitions>`
}

// pendingWorkPathsStart deploys a user-task process (once per id) and starts one
// instance of it with the given variables. It goes through the route table
// directly, as an administrator, so it seeds a server behind the access boundary
// as readily as one without it.
func pendingWorkPathsStart(t *testing.T, s *Server, processID, vars string) {
	t.Helper()
	mux, _ := s.mountRoutes()
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(httpapi.WithPrincipal(r.Context(), admin)))
	})
	var deployed bool
	s.do(func() { deployed = s.latestDeploymentOf(processID) != nil })
	if !deployed {
		code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/deployments",
			strings.NewReader(pendingWorkPathsTaskBPMN(processID)))
		if code != http.StatusOK {
			t.Fatalf("deploy %s: %d %s", processID, code, body)
		}
	}
	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/instances",
		strings.NewReader(`{"processId":"`+processID+`","variables":`+vars+`}`))
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start %s: %d %s", processID, code, body)
	}
}

// pendingWorkPathsOrder files an order whose one line is decided by approval.
func pendingWorkPathsOrder(t *testing.T, s *Server, id, approval string) {
	t.Helper()
	var err error
	s.do(func() {
		err = s.orderStore.Save(order.Order{ID: id, Orderer: "usr_ada", Recipient: "usr_ada",
			Lines: []order.Line{{ItemID: "vpn", Status: order.StatusPending,
				Approval: order.Approval{Kind: approval}}}})
	})
	if err != nil {
		t.Fatalf("save order %s: %v", id, err)
	}
}

// pendingWorkPathsAccount files one account.
func pendingWorkPathsAccount(t *testing.T, s *Server, u User) {
	t.Helper()
	var err error
	s.do(func() { err = s.users.Save(u) })
	if err != nil {
		t.Fatalf("save account %s: %v", u.ID, err)
	}
}

// pendingWorkPathsRead reads the route and decodes a 200.
func pendingWorkPathsRead(t *testing.T, s *Server, query string) pendingWork {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/pending-work"+query, nil)
	if code != http.StatusOK {
		t.Fatalf("pending-work%s: %d %s", query, code, raw)
	}
	var out pendingWork
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}

// pendingWorkPathsServe calls the handler directly with the given principal on
// the request — what the access boundary does after a sign-in — so the
// authenticated arms can be reached without a session.
func pendingWorkPathsServe(s *Server, pr *httpapi.Principal, query string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending-work"+query, nil)
	if pr != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), pr))
	}
	rec := httptest.NewRecorder()
	s.handlePendingWork(rec, req)
	return rec.Code, rec.Body.String()
}

// TestPendingWorkAnApprovalIsListedAndAPlainTaskIsNot. Only a task the order says
// decides one of its lines is an approval; a user task that merely carries an
// order id — a manual provisioning step — must not reach a reminder dressed as
// one. The approval names the order line and links into the inbox.
func TestPendingWorkAnApprovalIsListedAndAPlainTaskIsNot(t *testing.T) {
	srv := newServerForErrors(t)
	pendingWorkPathsAccount(t, srv, User{ID: "usr_ada", Username: "ada", Email: "ada@example.org"})
	pendingWorkPathsOrder(t, srv, "ord_pw", "pw-approval")
	pendingWorkPathsStart(t, srv, "pw-approval", `{"orderId":"ord_pw","itemId":"vpn","recipient":"usr_ada"}`)
	pendingWorkPathsStart(t, srv, "pw-chore", `{"orderId":"ord_pw","itemId":"vpn"}`)

	got := pendingWorkPathsRead(t, srv, "?principal=ada")
	if got.Principal != "usr_ada" {
		t.Errorf("principal = %q, want ada resolved by username to usr_ada", got.Principal)
	}
	if got.Counts.Approvals != 1 || len(got.Items) != 1 {
		t.Fatalf("counts = %+v items = %+v, want the one approval and not the chore", got.Counts, got.Items)
	}
	it := got.Items[0]
	if it.Kind != "approval" || it.Ref != "ord_pw" || it.Sub != "vpn" {
		t.Errorf("item = %+v, want the approval of ord_pw/vpn", it)
	}
	if !strings.Contains(it.What, "An order of vpn for usr_ada") || !strings.Contains(it.Link, "order=ord_pw") {
		t.Errorf("item = %+v, want it worded and linked to the order line", it)
	}
}

// TestPendingWorkTheListIsCutAndSaysSo. A reminder carries a bounded list, and a
// cut list says how much it left out rather than pretending to be everything.
func TestPendingWorkTheListIsCutAndSaysSo(t *testing.T) {
	budgets := limits.Default()
	budgets.PendingWorkItems = 1
	srv := newServerWithOptions(t, WithLimits(budgets))
	pendingWorkPathsAccount(t, srv, User{ID: "usr_ada", Username: "ada", Email: "ada@example.org"})
	for _, id := range []string{"ord_one", "ord_two"} {
		pendingWorkPathsOrder(t, srv, id, "pw-approval")
		pendingWorkPathsStart(t, srv, "pw-approval", `{"orderId":"`+id+`","itemId":"vpn"}`)
	}

	got := pendingWorkPathsRead(t, srv, "?principal=usr_ada")
	if got.Counts.Total != 2 || len(got.Items) != 1 || got.Omitted != 1 {
		t.Errorf("total = %d items = %d omitted = %d, want 2 counted, 1 shown, 1 omitted",
			got.Counts.Total, len(got.Items), got.Omitted)
	}
}

// TestPendingWorkAReviewNamesItsDisputeAndSkipsOtherPeoplesRows. A row addressed
// to somebody else is theirs to be reminded of; one addressed to this person says
// when a reconciliation finding stands against the right it asks about.
func TestPendingWorkAReviewNamesItsDisputeAndSkipsOtherPeoplesRows(t *testing.T) {
	srv := newServerForErrors(t)
	pendingWorkPathsAccount(t, srv, User{ID: "usr_ada", Username: "ada", Email: "ada@example.org"})
	for _, item := range []string{"vpn", "sap"} {
		recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
			Principal: "usr_cy", ItemID: item, Since: 1_000, Origin: model.OriginLegacy})
	}
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
		Principal: "usr_dee", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	now := time.Now().Unix()
	seedDiscrepancy(t, srv, discrepancyRecord{ID: "missing:ad:usr_cy:vpn", Kind: recMissing,
		System: "ad", Principal: "usr_cy", ItemID: "vpn", OpenedAt: now, LastSeenAt: now})
	recertifyHTTPPathsOpen(t, srv.Handler(),
		`{"name":"Q3","reviewers":{"usr_cy":"usr_ada","usr_dee":"usr_bo"}}`)

	got := pendingWorkPathsRead(t, srv, "?principal=usr_ada")
	if got.Counts.Recertifications != 2 {
		t.Fatalf("counts = %+v items = %+v, want Cy's two rows and not Dee's", got.Counts, got.Items)
	}
	var disputed int
	for _, it := range got.Items {
		if strings.Contains(it.What, "usr_dee") {
			t.Errorf("a row addressed to usr_bo reached ada: %+v", it)
		}
		if strings.Contains(it.What, "a reconciliation finding stands against it") {
			disputed++
			if !strings.Contains(it.What, "still need vpn") {
				t.Errorf("the dispute is on the wrong row: %+v", it)
			}
		}
	}
	if disputed != 1 {
		t.Errorf("%d item(s) mention the dispute, want exactly Cy's VPN", disputed)
	}
}

// TestPendingWorkSingleUserModeAnswersForNobodyInParticular. Without
// authentication the request carries no principal: asking about the caller
// answers an empty list rather than everybody's work, and asking about a named
// person is allowed — there is nobody else to keep it from.
func TestPendingWorkSingleUserModeAnswersForNobodyInParticular(t *testing.T) {
	srv := newServerForErrors(t)
	pendingWorkPathsAccount(t, srv, User{ID: "usr_ada", Username: "ada", Email: "ada@example.org"})

	own := pendingWorkPathsRead(t, srv, "")
	if own.Principal != "" || own.Counts.Total != 0 || own.Items == nil {
		t.Errorf("own = %+v, want nobody's work as an empty list", own)
	}
	if named := pendingWorkPathsRead(t, srv, "?principal=ada@example.org"); named.Principal != "usr_ada" {
		t.Errorf("named = %+v, want ada resolved", named)
	}
}

// TestPendingWorkWithAuthenticationARequestMustCarrySomebody. Behind the access
// boundary a request without a principal is answered 401 for itself and 403 for
// somebody else — never with another person's work.
func TestPendingWorkWithAuthenticationARequestMustCarrySomebody(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())

	if code, body := pendingWorkPathsServe(srv, nil, ""); code != http.StatusUnauthorized ||
		!strings.Contains(body, "no principal") {
		t.Errorf("own, nobody = %d %s, want 401", code, body)
	}
	if code, body := pendingWorkPathsServe(srv, nil, "?principal=root"); code != http.StatusForbidden ||
		!strings.Contains(body, "operator's") {
		t.Errorf("other, nobody = %d %s, want 403", code, body)
	}
}

// TestPendingWorkAStoreThatCannotBeReadIsAFault. Each read behind the answer —
// the accounts and groups a name resolves through, the campaigns and the rows a
// review comes from, the groups a row may be offered to — fails the request with
// a 500. An empty list would tell a reminder process that nothing is waiting.
func TestPendingWorkAStoreThatCannotBeReadIsAFault(t *testing.T) {
	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada"}
	// The first two resolve a named person in single-user mode; the last two read
	// the signed-in caller's own reviews behind the access boundary.
	cases := []struct {
		name  string
		as    *httpapi.Principal
		query string
		dir   func(*Server) string
	}{
		{"the accounts", nil, "?principal=ada", func(s *Server) string { return s.users.Dir() }},
		{"the groups, resolving a name", nil, "?principal=ada", func(s *Server) string { return s.groups.Dir() }},
		{"the campaigns", ada, "", func(s *Server) string { return s.recertifications.campaigns.Dir() }},
		{"the groups, for a row", ada, "", func(s *Server) string { return s.groups.Dir() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []Option
			if tc.as != nil {
				opts = append(opts, WithAuth())
			}
			srv := newServerWithOptions(t, opts...)
			pendingWorkPathsAccount(t, srv, User{ID: "usr_ada", Username: "ada", Email: "ada@example.org"})
			recertifyHTTPPathsBreakDir(t, tc.dir(srv))

			code, body := pendingWorkPathsServe(srv, tc.as, tc.query)
			if code != http.StatusInternalServerError {
				t.Errorf("= %d %s, want 500", code, body)
			}
		})
	}

	t.Run("a campaign's rows", func(t *testing.T) {
		srv := newServerWithOptions(t, WithAuth())
		var err error
		srv.do(func() {
			err = srv.recertifications.saveCampaign(recertifyCampaign{ID: "cmp_open", Name: "Q3",
				OpenedBy: "usr_ada", OpenedAt: 1})
		})
		if err != nil {
			t.Fatalf("seed campaign: %v", err)
		}
		recertifyHTTPPathsBreakDir(t, srv.recertifications.rows.Dir())

		code, body := pendingWorkPathsServe(srv, ada, "")
		if code != http.StatusInternalServerError || !strings.Contains(body, "recertifyrowstore") {
			t.Errorf("= %d %s, want 500 naming the row store", code, body)
		}
	})
}

// TestPendingWorkAServerShuttingDownSaysSo. Both halves report the shutdown
// rather than an empty list, for the caller and for a named person — and as the
// 503 every other route gives a stopping server, so a reminder process retries
// rather than treating it as a fault.
func TestPendingWorkAServerShuttingDownSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/pending-work", nil)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("own = %d %s, want 503 naming the shutdown", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/pending-work?principal=ada", nil)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("named = %d %s, want 503 naming the shutdown", code, body)
	}
}
