package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
)

// approvalsPathsBPMN is an installation's own approval model: a catalogue binds a
// product to it by naming the process as the approval kind (order.Line.ApprovalProcess),
// which is what lets these cases make an approval out of nothing but a deployed model,
// a started instance and an order record.
const approvalsPathsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="kunden-genehmigung" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="Genehmigen" name="Genehmigen">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="alice"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="Genehmigen"/>
    <sequenceFlow id="f2" sourceRef="Genehmigen" targetRef="end"/>
  </process>
</definitions>`

// approvalsPathsDeploy deploys the approval model once.
func approvalsPathsDeploy(t *testing.T, srv *Server) {
	t.Helper()
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", approvalsPathsBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy approval model: %d (%s)", code, body)
	}
}

// approvalsPathsStart starts one approval instance with the given variables (a JSON
// object) and returns the key of the user task it parks on. Keys are compared
// before and after rather than "the newest", so a case may start several.
func approvalsPathsStart(t *testing.T, srv *Server, vars string) uint64 {
	t.Helper()
	before := approvalsPathsTaskKeys(t, srv)
	body := `{"processId":"kunden-genehmigung","variables":` + vars + `}`
	if code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/instances", body, "application/json"); code != http.StatusOK {
		t.Fatalf("start approval: %d (%s)", code, b)
	}
	for k := range approvalsPathsTaskKeys(t, srv) {
		if !before[k] {
			return k
		}
	}
	t.Fatal("starting the approval parked no user task")
	return 0
}

// approvalsPathsTaskKeys is the set of open user-task keys, read on the loop.
func approvalsPathsTaskKeys(t *testing.T, srv *Server) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	srv.do(func() {
		_ = srv.store.ActivatableJobs(compiler.UserTaskJobTypeIndex, func(k uint64) error {
			out[k] = true
			return nil
		})
	})
	return out
}

// approvalsPathsOrder files an order whose single line is decided by the model above.
// It is written on the loop, which owns the store (I3).
func approvalsPathsOrder(t *testing.T, srv *Server, o order.Order) {
	t.Helper()
	var err error
	srv.do(func() { err = srv.orderStore.Save(o) })
	if err != nil {
		t.Fatalf("save order: %v", err)
	}
}

// approvalsPathsLine is an order line that asks kunden-genehmigung to decide it.
func approvalsPathsLine(item, variant string) order.Line {
	return order.Line{ItemID: item, VariantID: variant, Approval: order.Approval{Kind: "kunden-genehmigung"}}
}

// approvalsPathsDirAsFile puts a regular file where a store expects its directory.
// It is the portable way to make a store unreadable and unwritable: going through a
// file fails on every platform (sidecar.Store reports it as such on Windows too),
// and it needs no permission bits, which a test running as root would ignore.
func approvalsPathsDirAsFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove %s: %v", dir, err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write %s: %v", dir, err)
	}
}

// approvalsPathsList reads the approval page as the given request context sees it.
func approvalsPathsList(t *testing.T, srv *Server, query string) (int, []byte) {
	t.Helper()
	return serveInternal(t, srv, http.MethodGet, "/api/v1/approvals"+query, "", "")
}

// TestApprovalsListRefusesAMalformedCursor: the cursor is a job key, and a page asked
// to continue from something that is not one must say so rather than start over from
// the newest task — which would hand the caller the first page twice and look like
// a duplicate approval.
func TestApprovalsListRefusesAMalformedCursor(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})

	code, body := approvalsPathsList(t, srv, "?before=not-a-key")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "invalid before cursor") {
		t.Fatalf("malformed cursor: %d (%s), want 400 naming the cursor", code, body)
	}

	// The cursor is an exclusive upper bound: continuing from the task's own key
	// leaves it out, continuing from just above it keeps it.
	for _, tc := range []struct {
		before uint64
		want   int
	}{{key, 0}, {key + 1, 1}} {
		code, body := approvalsPathsList(t, srv, fmt.Sprintf("?before=%d", tc.before))
		if code != http.StatusOK {
			t.Fatalf("before=%d: %d (%s)", tc.before, code, body)
		}
		var rows []approvalResp
		if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if len(rows) != tc.want {
			t.Errorf("before=%d: %d approvals, want %d", tc.before, len(rows), tc.want)
		}
	}
}

// TestApprovalsListHandsOnACursorWhenTheScanBudgetBites: a page that stopped at the
// scan budget must say where to continue, or the approvals past it are unreachable
// from the page while its total reads like the whole list (ADR-0378).
func TestApprovalsListHandsOnACursorWhenTheScanBudgetBites(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	first := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	second := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})

	defer SetMaxFolderScanForTest(1)()
	code, body := approvalsPathsList(t, srv, "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var page struct {
		Items      []approvalResp `json:"items"`
		Truncated  bool           `json:"truncated"`
		NextCursor string         `json:"nextCursor"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	newest := max(first, second)
	if !page.Truncated || page.NextCursor != fmt.Sprint(newest) {
		t.Fatalf("truncated=%v cursor=%q, want a truncated page continuing below %d", page.Truncated, page.NextCursor, newest)
	}
	if len(page.Items) != 1 || page.Items[0].Task.Key != newest {
		t.Errorf("page = %+v, want only the newest approval", page.Items)
	}
}

// TestApprovalsListFailsWhenTheOrdersCannotBeRead: whether a held task is an approval
// is a question about its order, and an order store that cannot be read is not an
// answer of "no". Listing nothing would tell an approver their queue is empty while
// requests wait on them.
func TestApprovalsListFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))

	code, body := approvalsPathsList(t, srv, "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read approvals") {
		t.Fatalf("unreadable order store: %d (%s), want 500 'read approvals'", code, body)
	}
}

// TestApprovalsListFailsWhenTheReleaseOrCatalogueCannotBeRead: the approval carries
// what is being decided in words — the product's name from the release, the brand
// from the catalogue. A broken catalogue store must fail the page, not serve an
// approval stripped of what it is about.
func TestApprovalsListFailsWhenTheReleaseOrCatalogueCannotBeRead(t *testing.T) {
	for _, broken := range []string{"releases", "catalogs"} {
		t.Run(broken, func(t *testing.T) {
			srv := newServerForErrors(t)
			approvalsPathsDeploy(t, srv)
			approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
			approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", ReleaseID: "rel-1",
				Lines: []order.Line{approvalsPathsLine("vpn", "")}})
			var err error
			srv.do(func() { err = srv.catalogStore.SaveRelease(catalog.Release{ID: "rel-1", CatalogID: "cat-1"}) })
			if err != nil {
				t.Fatalf("save release: %v", err)
			}
			approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "catalog", broken))

			code, body := approvalsPathsList(t, srv, "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), "read approvals") {
				t.Fatalf("unreadable %s: %d (%s), want 500 'read approvals'", broken, code, body)
			}
		})
	}
}

// TestApprovalsListLeavesOutATaskTheOrderDoesNotDelegate: an order's own user task —
// a provisioning step asking somebody to do something by hand — carries the order id
// too. It is only an approval when the order says this process decides that line.
func TestApprovalsListLeavesOutATaskTheOrderDoesNotDelegate(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	// The line exists, but its approval is decided by somebody else's process.
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{
		{ItemID: "vpn", Approval: order.Approval{Kind: "another-process"}}}})

	code, body := approvalsPathsList(t, srv, "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []approvalResp
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 0 {
		t.Errorf("a task the order does not delegate was listed as an approval: %+v", rows)
	}
}

// TestApprovalsListShowsNothingToAnUnauthenticatedCallerWithAuthOn: with
// authentication on, an approval is one person's, and a request with no principal is
// nobody — so it holds none, even though tasks are open.
func TestApprovalsListShowsNothingToAnUnauthenticatedCallerWithAuthOn(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})
	// Deployed and started through the handlers directly, as an administrator, since
	// the routes are closed to an anonymous request.
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}
	for _, step := range []struct{ path, body, ct string }{
		{"/api/v1/deployments", approvalsPathsBPMN, "application/xml"},
		{"/api/v1/instances", `{"processId":"kunden-genehmigung","variables":{"orderId":"ord-1","itemId":"vpn"}}`, "application/json"},
	} {
		req := httptest.NewRequest(http.MethodPost, step.path, strings.NewReader(step.body))
		req.Header.Set("Content-Type", step.ct)
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		if step.path == "/api/v1/deployments" {
			srv.handleDeploy(rec, req)
		} else {
			srv.handleCreateInstanceByProcessID(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d (%s)", step.path, rec.Code, rec.Body)
		}
	}
	if len(approvalsPathsTaskKeys(t, srv)) != 1 {
		t.Fatal("the approval task was not opened")
	}

	rec := httptest.NewRecorder()
	srv.handleListApprovals(rec, httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d (%s)", rec.Code, rec.Body)
	}
	var rows []approvalResp
	if err := json.Unmarshal(listRows(t, rec.Body.Bytes()), &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if len(rows) != 0 {
		t.Errorf("an anonymous request holds %d approvals, want none", len(rows))
	}
}

// approvalsPathsLogo asks for one task's approval logo.
func approvalsPathsLogo(t *testing.T, srv *Server, key string) (int, string) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/approvals/"+key+"/logo", "", "")
	return code, string(body)
}

// TestApprovalsLogoRefusesWhatIsNotAnApprovalWithAMark walks the logo route's
// refusals in the order a request meets them. Each one is a different thing to tell
// the approval page — a bad link, a task that is gone, a task that decides no order,
// a catalogue that simply has no brand — and none of them may serve an image.
func TestApprovalsLogoRefusesWhatIsNotAnApprovalWithAMark(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	plain := approvalsPathsStart(t, srv, `{"note":"no order at all"}`)
	branded := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", ReleaseID: "rel-1",
		Lines: []order.Line{approvalsPathsLine("vpn", "")}})
	var err error
	srv.do(func() {
		if err = srv.catalogStore.SaveCatalog(catalog.Catalog{ID: "cat-1", Rank: 1}); err != nil {
			return
		}
		err = srv.catalogStore.SaveRelease(catalog.Release{ID: "rel-1", CatalogID: "cat-1"})
	})
	if err != nil {
		t.Fatalf("seed catalogue: %v", err)
	}

	for _, tc := range []struct {
		name, key string
		code      int
		says      string
	}{
		{"not a key", "abc", http.StatusBadRequest, "invalid task key"},
		{"no such task", "999999999", http.StatusNotFound, "no open task"},
		{"a task deciding no order", fmt.Sprint(plain), http.StatusNotFound, "decides no catalogue order"},
		{"a catalogue without a mark", fmt.Sprint(branded), http.StatusNotFound, "has no logo"},
	} {
		code, body := approvalsPathsLogo(t, srv, tc.key)
		if code != tc.code || !strings.Contains(body, tc.says) {
			t.Errorf("%s: %d (%s), want %d saying %q", tc.name, code, body, tc.code, tc.says)
		}
	}
}

// TestApprovalsLogoFailsOnAnUnreadableMark: a mark that is there but cannot be read
// is a server fault, not "no logo" — answering 404 would make the page fall back to
// the default brand and hide a broken data directory.
func TestApprovalsLogoFailsOnAnUnreadableMark(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", ReleaseID: "rel-1",
		Lines: []order.Line{approvalsPathsLine("vpn", "")}})
	var err error
	srv.do(func() { err = srv.catalogStore.SaveRelease(catalog.Release{ID: "rel-1", CatalogID: "cat-1"}) })
	if err != nil {
		t.Fatalf("save release: %v", err)
	}
	// A directory where each format of the mark would be: present, but not a file
	// anything can read.
	for _, ext := range []string{"png", "svg"} {
		p := filepath.Join(srv.dataDir, "catalog", "logos", hex.EncodeToString([]byte("cat-1"))+"."+ext)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}

	code, body := approvalsPathsLogo(t, srv, fmt.Sprint(key))
	if code != http.StatusInternalServerError || !strings.Contains(body, "read logo") {
		t.Errorf("unreadable mark: %d (%s), want 500 'read logo'", code, body)
	}
}

// TestApprovalsLogoFailsWhenTheOrdersCannotBeRead: the logo route decides whether a
// task is an approval the same way the listing does, so a broken order store is a
// 500 there too rather than a 404 that says the task decides nothing.
func TestApprovalsLogoFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))

	code, body := approvalsPathsLogo(t, srv, fmt.Sprint(key))
	if code != http.StatusInternalServerError || !strings.Contains(body, "read approval") {
		t.Errorf("unreadable order store: %d (%s), want 500 'read approval'", code, body)
	}
}
