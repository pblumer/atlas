package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/clio"
)

// budgetFixture deploys the message-start process, configures a clio worker and one
// watch on it, and returns the server, the subscription id and the fake reader whose
// events the watch republishes.
func budgetFixture(t *testing.T, watch string) (*Server, string, *fakeClioReader) {
	t.Helper()
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("Onboard")
	x.saveDraft(pid, employeeStartBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"http://x"}`)
	if code != http.StatusOK {
		t.Fatalf("create worker: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions", watch)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)
	fake := &fakeClioReader{}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })
	return srv, sub.ID, fake
}

// seedEvents appends n fresh events to what the fake already holds. Appending rather
// than replacing is what the fake's resume semantics need: it finds the boundary by
// scanning for the cursor's own id, so an event log that forgets its past reads as
// empty.
func seedEvents(fake *fakeClioReader, from, n int) {
	for i := from; i < from+n; i++ {
		fake.events = append(fake.events, clio.InboundEvent{
			ID: strconv.Itoa(i), Subject: "/employees/E-" + strconv.Itoa(i), Type: "employee.created",
		})
	}
}

// subRecord reads a watch back out of the store.
func subRecord(t *testing.T, srv *Server, id string) inboundSubscription {
	t.Helper()
	var rec inboundSubscription
	srv.do(func() {
		r, ok, err := srv.inboundSubs.Get(id)
		if err != nil || !ok {
			t.Fatalf("get subscription %s: ok=%v err=%v", id, ok, err)
		}
		rec = r
	})
	return rec
}

// A watch that keeps publishing past its hourly budget is switched off, and says why.
//
// The guard exists because a watch can feed itself: a process started by an event
// writes to the system the watch reads, the watch matches what it wrote, and the loop
// has no natural end. Every instance is well-formed and every task succeeds, so nothing
// downstream can tell it apart from a busy morning — only the *rate* can, which is why
// the ceiling lives here and not in the model.
func TestInboundWatchTripsItsHourlyBudget(t *testing.T) {
	srv, subID, fake := budgetFixture(t,
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created","startFromTip":false,"maxPerHour":3}`)

	// Three fit the budget.
	seedEvents(fake, 1, 3)
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 3 {
		t.Fatalf("active after the first poll = %d, want 3 (inside the budget)", n)
	}
	if rec := subRecord(t, srv, subID); !rec.Enabled {
		t.Fatal("the watch was switched off while inside its budget")
	}

	// The fourth would exceed it: nothing is published, and the watch is off.
	seedEvents(fake, 4, 1)
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 3 {
		t.Errorf("active after the over-budget poll = %d, want 3 — the batch that "+
			"crossed the ceiling must not be published", n)
	}
	rec := subRecord(t, srv, subID)
	if rec.Enabled {
		t.Fatal("the watch is still enabled after exceeding its budget")
	}
	// The reason has to be readable by whoever finds the watch off, and has to name the
	// number to raise: "it stopped" without "why" is indistinguishable from a bug.
	if !strings.Contains(rec.DisabledReason, "3") || !strings.Contains(rec.DisabledReason, "hour") {
		t.Errorf("DisabledReason = %q, want it to name the ceiling it hit", rec.DisabledReason)
	}

	// And it stays off: a disabled watch is not polled, so the next tick publishes
	// nothing even though events are waiting.
	seedEvents(fake, 5, 1)
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 3 {
		t.Errorf("active after a poll of the disabled watch = %d, want 3", n)
	}
}

// The budget is a rate, not a lifetime total: an hour later the window starts again.
// Without this a long-lived watch on a normal project would eventually switch itself
// off for doing exactly its job.
func TestInboundWatchBudgetWindowRolls(t *testing.T) {
	srv, subID, fake := budgetFixture(t,
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created","startFromTip":false,"maxPerHour":3}`)
	now := time.Now()
	srv.inboundClock = func() time.Time { return now }

	seedEvents(fake, 1, 3)
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 3 {
		t.Fatalf("active after the first window = %d, want 3", n)
	}

	now = now.Add(61 * time.Minute)
	seedEvents(fake, 4, 3)
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 6 {
		t.Fatalf("active after the second window = %d, want 6 — the budget is per hour, "+
			"not per watch lifetime", n)
	}
	if rec := subRecord(t, srv, subID); !rec.Enabled {
		t.Fatal("the watch was switched off across a window boundary")
	}
}

// A watch that names no ceiling gets the default one, so the guard protects a watch
// nobody configured — which is every watch that existed before it.
func TestInboundWatchDefaultBudgetApplies(t *testing.T) {
	srv, subID, fake := budgetFixture(t,
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created","startFromTip":false}`)
	if rec := subRecord(t, srv, subID); rec.MaxPerHour != 0 {
		t.Fatalf("MaxPerHour = %d, want it stored as 0 (meaning the default)", rec.MaxPerHour)
	}
	if got := inboundBudget(subRecord(t, srv, subID)); got != defaultInboundPerHour {
		t.Fatalf("inboundBudget of an unconfigured watch = %d, want the default %d", got, defaultInboundPerHour)
	}
	// And it is a real ceiling, not a stored zero that lets everything through.
	seedEvents(fake, 1, 2)
	srv.pollInbound(context.Background())
	if rec := subRecord(t, srv, subID); rec.PublishedInWindow != 2 {
		t.Errorf("PublishedInWindow = %d after two events, want 2 — an unconfigured watch "+
			"must still be counted", rec.PublishedInWindow)
	}
}
