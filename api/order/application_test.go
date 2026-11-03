package order

import (
	"errors"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// orderableRelease is testRelease with the workplace orderable by one application.
func orderableRelease(t *testing.T, apps ...string) catalog.Release {
	t.Helper()
	rel := testRelease(t)
	for i := range rel.Items {
		if rel.Items[i].ID == "workplace" {
			rel.Items[i].OrderableBy = apps
		}
	}
	return rel
}

func refusalOf(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return r
}

// A process orders in its application's name, for the recipient it names, with
// everything a basket would get: the parts the whole carries, the application as the
// orderer, and an id a retry finds again instead of ordering twice.
func TestAnApplicationOrdersAProductThatListsIt(t *testing.T) {
	s := newService(t)
	a := ApplicationOrder{App: "hr-eintritte", CommandID: "task-42", Release: orderableRelease(t, "hr-eintritte"),
		Items: []string{"workplace"}, Recipient: "usr_neu"}
	got, placed, err := s.PlaceForApplication(a)
	if err != nil || !placed {
		t.Fatalf("PlaceForApplication = %v, %v", placed, err)
	}
	if got.Orderer != "system:application:hr-eintritte" || got.Recipient != "usr_neu" || got.ID != ApplicationOrderID("task-42") {
		t.Errorf("order = %+v, want the application as orderer, the recipient and the task's id", got)
	}
	if lineIDs(got) != "account,laptop,workplace" {
		t.Errorf("lines = %s, want the whole and the parts it carries", lineIDs(got))
	}
	again, placed, err := s.PlaceForApplication(a)
	if err != nil || placed || again.ID != got.ID {
		t.Errorf("a retry = %s, %v, %v; want the same order, not placed again", again.ID, placed, err)
	}
	if ApplicationOrderID("task-43") == got.ID {
		t.Error("two tasks got the same order id")
	}
}

// Only a product that lists the application, offered by the release, for a recipient
// and with something to order; each refusal says which.
func TestAnApplicationOrderIsRefused(t *testing.T) {
	s := newService(t)
	base := ApplicationOrder{App: "hr-eintritte", CommandID: "task-1", Release: orderableRelease(t, "hr-eintritte"),
		Items: []string{"workplace"}, Recipient: "usr_neu"}
	cases := map[string]struct {
		mut    func(*ApplicationOrder)
		status int
	}{
		"no application":            {func(a *ApplicationOrder) { a.App = " " }, http.StatusForbidden},
		"another application":       {func(a *ApplicationOrder) { a.App = "marketing" }, http.StatusForbidden},
		"a product nobody listed":   {func(a *ApplicationOrder) { a.Items = []string{"screen"} }, http.StatusForbidden},
		"a product not in the shop": {func(a *ApplicationOrder) { a.Items = []string{"yacht"} }, http.StatusNotFound},
		"no recipient":              {func(a *ApplicationOrder) { a.Recipient = "" }, http.StatusBadRequest},
		"nothing":                   {func(a *ApplicationOrder) { a.Items = nil }, http.StatusBadRequest},
		"an answer to nothing": {func(a *ApplicationOrder) {
			a.Config = map[string]map[string]string{"yacht": {"farbe": "blau"}}
		}, http.StatusBadRequest},
	}
	for name, c := range cases {
		a := base
		c.mut(&a)
		_, placed, err := s.PlaceForApplication(a)
		if r := refusalOf(t, err); r.Status != c.status || placed {
			t.Errorf("%s: %d %q placed=%v, want %d", name, r.Status, r.Message, placed, c.status)
		}
	}
	if _, _, err := s.PlaceForApplication(ApplicationOrder{App: "x", Items: []string{"workplace"}, Recipient: "u"}); err == nil {
		t.Error("an order without the asking task's id was placed")
	}
}

// A provisioning process that reports before its start is marked keeps its report:
// the start marks only a line that is still pending or blocked.
func TestMarkStartedKeepsAReportThatOutranIt(t *testing.T) {
	s := newService(t)
	o, _, err := s.PlaceForApplication(ApplicationOrder{App: "hr", CommandID: "task-7",
		Release: orderableRelease(t, "hr"), Items: []string{"workplace"}, Recipient: "usr_neu"})
	if err != nil {
		t.Fatal(err)
	}
	started, changed, err := MarkStarted(o, "account", 1800)
	if err != nil || !changed || statusOf(started.Lines, "account") != StatusRunning {
		t.Fatalf("a pending line: changed=%v err=%v status=%s", changed, err, statusOf(started.Lines, "account"))
	}
	done, err := Apply(o, "account", StatusDone, 1801)
	if err != nil {
		t.Fatal(err)
	}
	after, changed, err := MarkStarted(done, "account", 1802)
	if err != nil || changed || statusOf(after.Lines, "account") != StatusDone {
		t.Fatalf("a line reported done first: changed=%v err=%v status=%s, want done kept", changed, err, statusOf(after.Lines, "account"))
	}
	if _, _, err := MarkStarted(o, "yacht", 1803); err == nil {
		t.Error("a line the order does not carry was marked")
	}
}
