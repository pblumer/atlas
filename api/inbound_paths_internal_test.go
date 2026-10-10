package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// inboundPathsSeed files a clio worker and one subscription on it, on the loop.
func inboundPathsSeed(t *testing.T, srv *Server, sub inboundSubscription) {
	t.Helper()
	var err error
	srv.do(func() {
		if err = srv.connectors.Save(connector{ID: "w-clio", Name: "events", Kind: connectorKindClio,
			Endpoint: "https://clio.example", Enabled: true}); err != nil {
			return
		}
		err = srv.inboundSubs.Save(sub)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// inboundPathsGet reads a subscription back, on the loop.
func inboundPathsGet(t *testing.T, srv *Server, id string) inboundSubscription {
	t.Helper()
	var (
		rec inboundSubscription
		ok  bool
		err error
	)
	srv.do(func() { rec, ok, err = srv.inboundSubs.Get(id) })
	if err != nil || !ok {
		t.Fatalf("read subscription %s: ok=%v err=%v", id, ok, err)
	}
	return rec
}

// TestInboundSwitchingATrippedWatchBackOnStartsItsBudgetOver: the guard switched the
// watch off for publishing too much. Switching it on again must clear the reason and
// open a fresh window — the old count would trip it on the very next event, and the
// old reason would say a running watch is off. Its cap can be changed in the same
// edit.
func TestInboundSwitchingATrippedWatchBackOnStartsItsBudgetOver(t *testing.T) {
	srv := newServerForErrors(t)
	inboundPathsSeed(t, srv, inboundSubscription{ID: "sub-1", ConnectorID: "w-clio", MessageName: "evt",
		WatchedSubject: "orders", Enabled: false, DisabledReason: "published 60 events in an hour",
		WindowStart: 1, PublishedInWindow: 60, MaxPerHour: 50})

	code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/inbound-subscriptions/sub-1",
		`{"enabled":true,"maxPerHour":100}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("re-enable: %d (%s)", code, body)
	}
	got := inboundPathsGet(t, srv, "sub-1")
	if !got.Enabled || got.DisabledReason != "" || got.PublishedInWindow != 0 || got.WindowStart <= 1 || got.MaxPerHour != 100 {
		t.Errorf("after re-enabling = %+v, want it on, the reason gone, a fresh window and the new cap", got)
	}

	// Enabling what is already enabled under the same name is not a fresh claim on
	// that name, and leaves the watch as it is.
	if code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/inbound-subscriptions/sub-1",
		`{"enabled":true}`, "application/json"); code != http.StatusOK {
		t.Errorf("enable an enabled watch: %d (%s)", code, body)
	}
}

// TestInboundFailsOnASubscriptionItCannotRead: who may change or remove a watch is
// decided by the worker it belongs to, which is read from the subscription. An
// unreadable subscription cannot be authorized, so neither edit nor delete proceeds —
// and a delete must not answer 204 for a watch that may still be polling.
func TestInboundFailsOnASubscriptionItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	corrupt(t, srv.inboundSubs.Dir(), "sub-1")
	for _, tc := range []struct{ method, body string }{
		{http.MethodPatch, `{"maxPerHour":1}`},
		{http.MethodDelete, ""},
	} {
		code, body := serveInternal(t, srv, tc.method, "/api/v1/inbound-subscriptions/sub-1", tc.body, "application/json")
		if code != http.StatusInternalServerError || !strings.Contains(string(body), "read subscription") {
			t.Errorf("%s over an unreadable subscription: %d (%s), want 500", tc.method, code, body)
		}
	}
}

// TestInboundReportsAnEditItCouldNotStore: an edit that did not land must not come
// back as the edited watch — the operator would believe the new cap is in force while
// the old one keeps throttling, or the other way round.
func TestInboundReportsAnEditItCouldNotStore(t *testing.T) {
	srv := newServerForErrors(t)
	inboundPathsSeed(t, srv, inboundSubscription{ID: "sub-1", ConnectorID: "w-clio", MessageName: "evt",
		WatchedSubject: "orders", Enabled: true, MaxPerHour: 50})
	before := inboundPathsGet(t, srv, "sub-1")
	if err := os.MkdirAll(srv.inboundSubs.FileFor("sub-1")+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/inbound-subscriptions/sub-1",
		`{"maxPerHour":5}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "update subscription") {
		t.Fatalf("unwritable subscription: %d (%s), want 500", code, body)
	}
	if after := inboundPathsGet(t, srv, "sub-1"); !reflect.DeepEqual(before, after) {
		t.Errorf("a failed edit changed the subscription: %+v -> %+v", before, after)
	}
}

// inboundPathsListenerBPMN starts on the message "evt".
const inboundPathsListenerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m" name="evt"/>
  <process id="listener" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="m"/></startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestInboundRenameCannotBeCheckedAgainstApplicationsItCannotRead: pointing a watch
// at a name a deployed process listens for is a claim on that process's events, and
// whether the caller may make it depends on the application the process is in. With
// the applications unreadable the claim cannot be judged, and the rename stops.
func TestInboundRenameCannotBeCheckedAgainstApplicationsItCannotRead(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	inboundPathsSeed(t, srv, inboundSubscription{ID: "sub-1", ConnectorID: "w-clio", MessageName: "other",
		WatchedSubject: "orders", Enabled: true})
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(inboundPathsListenerBPMN))
	req.Header.Set("Content-Type", "application/xml")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), admin))
	rec := httptest.NewRecorder()
	srv.handleDeploy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy listener: %d (%s)", rec.Code, rec.Body)
	}
	approvalsPathsDirAsFile(t, srv.projects.Dir())

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/inbound-subscriptions/sub-1", strings.NewReader(`{"messageName":"evt"}`))
	req.SetPathValue("id", "sub-1")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), admin))
	rec = httptest.NewRecorder()
	srv.handleUpdateInboundSubscription(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "read subscriptions") {
		t.Fatalf("rename over unreadable applications: %d (%s), want 500", rec.Code, rec.Body)
	}
	if after := inboundPathsGet(t, srv, "sub-1"); after.MessageName != "other" {
		t.Errorf("message name = %q after a refused rename, want it unchanged", after.MessageName)
	}
}
