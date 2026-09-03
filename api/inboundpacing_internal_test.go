package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/jira"
)

// A watch with no cadence of its own is read at its *kind's* default, not on every
// bridge tick. ADR-0214 puts Jira's at a minute: a Jira site rate-limits per site, and a
// two-second poll per watch spends that whole budget on empty answers — and pays for
// each of them with a run-loop round trip. clio is local and cheap, so its default stays
// the bridge's own tick, which is what every clio subscription has always had.
func TestInboundDueUsesTheKindsDefaultCadence(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	if !inboundDue(connectorKindClio, inboundSubscription{}, now) {
		t.Error("a clio watch with no cadence of its own was skipped; it is read on every tick")
	}
	if inboundDue(connectorKindJira, inboundSubscription{LastPolledAt: now.Unix() - 30}, now) {
		t.Error("a jira watch with no cadence of its own was read 30s in; the kind's default is a minute")
	}
	if !inboundDue(connectorKindJira, inboundSubscription{LastPolledAt: now.Unix() - 60}, now) {
		t.Error("a jira watch was skipped after the kind's default had elapsed")
	}
	// A watch that states a cadence keeps it, in either direction.
	if !inboundDue(connectorKindJira, inboundSubscription{PollSeconds: 5, LastPolledAt: now.Unix() - 5}, now) {
		t.Error("a jira watch's own faster cadence lost to the kind's default")
	}
	if inboundDue(connectorKindClio, inboundSubscription{PollSeconds: 60, LastPolledAt: now.Unix() - 30}, now) {
		t.Error("a clio watch's own cadence was ignored")
	}
}

// A watch that is read on every tick never consults LastPolledAt, so recording it costs
// two fsyncs on the single writer for a value nothing reads — every tick, for every such
// watch. That is design-time state written at run-loop rates, and it lands in front of
// every HTTP request that has to reach the loop.
func TestPollInboundDoesNotRewriteAWatchThatPacesItselfOnEveryTick(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0)) // drive the bridge by hand
	subID := newClioWatch(t, srv, "orders/new", "orderEvent")
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": &fakeClioReader{}}) })

	srv.pollInbound(context.Background())
	assertNotRewritten(t, srv, subID, func() { srv.pollInbound(context.Background()) })
}

// A poll that reads the same window again — which is the steady state of a watch sitting
// at the tip — produces the cursor the record already carries. Writing it is another two
// fsyncs on the single writer that change nothing at all.
func TestAdvanceInboundCursorDoesNotRewriteAnUnchangedCursor(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	subID := newClioWatch(t, srv, "orders/new", "orderEvent")
	srv.do(func() { srv.advanceInboundCursor(subID, "42") })

	assertNotRewritten(t, srv, subID, func() {
		srv.do(func() { srv.advanceInboundCursor(subID, "42") })
	})
}

// The whole of it end to end on the source that motivated it. A jira watch that states
// no cadence of its own is read once a minute, not on every one of the bridge's
// two-second ticks: at the tick it is one Jira search and one run-loop round trip per
// watch per two seconds, spent almost entirely on answers that carry nothing new.
func TestPollInboundPacesAJiraWatchAtTheKindsDefault(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0)) // drive the bridge by hand
	at := time.Unix(1_000_000, 0)
	srv.inboundClock = func() time.Time { return at }

	newJiraWatch(t, srv, "project = OPS", "ticket")
	f := &fakeJiraClient{}
	srv.do(func() { srv.jiraRegistry.Replace(map[string]jira.Client{"tickets": f}) })

	// Three of the bridge's own ticks: the watch is read on the first and paced past
	// the other two.
	for range 3 {
		srv.pollInbound(context.Background())
		at = at.Add(2 * time.Second)
	}
	if len(f.asked) != 1 {
		t.Errorf("jira was searched %d times across three bridge ticks, want 1: a watch with no cadence "+
			"of its own is read at the kind's default of %v", len(f.asked), jiraDefaultPoll)
	}

	// Once the cadence has elapsed it is read again.
	at = at.Add(jiraDefaultPoll)
	srv.pollInbound(context.Background())
	if len(f.asked) != 2 {
		t.Errorf("jira was searched %d times, want 2: the watch was still skipped after its cadence had elapsed", len(f.asked))
	}
}

// assertNotRewritten freezes a subscription record's modification time, runs poll, and
// fails if the record was written again. A sidecar Save renames a fresh temp file over
// the record, so a rewrite always moves the timestamp — no waiting on a clock.
func assertNotRewritten(t *testing.T, srv *Server, subID string, poll func()) {
	t.Helper()
	path := srv.inboundSubs.FileFor(subID)
	frozen := time.Unix(1_000_000, 0)
	if err := os.Chtimes(path, frozen, frozen); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	poll()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !st.ModTime().Equal(frozen) {
		t.Error("the watch record was written again although nothing about it had changed; " +
			"a sidecar Save is two fsyncs on the run-loop goroutine, and every request that needs the loop waits behind them")
	}
}

// newClioWatch creates an enabled clio worker named "events" and a watch on it,
// returning the watch's id.
func newClioWatch(t *testing.T, srv *Server, subject, messageName string) string {
	t.Helper()
	return newWatch(t, srv, `{"name":"events","kind":"clio","endpoint":"http://x"}`,
		`{"watchedSubject":"`+subject+`","messageName":"`+messageName+`","startFromTip":false}`)
}

// newJiraWatch creates an enabled jira worker named "tickets" and a watch on it.
func newJiraWatch(t *testing.T, srv *Server, jql, messageName string) string {
	t.Helper()
	return newWatch(t, srv, `{"name":"tickets","kind":"jira","endpoint":"https://acme.atlassian.net","credentialsRef":"JIRA"}`,
		`{"jql":"`+jql+`","messageName":"`+messageName+`","startFromTip":false}`)
}

func newWatch(t *testing.T, srv *Server, connBody, watchBody string) string {
	t.Helper()
	x := deployTestHarness{t, srv.Handler()}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", connBody)
	if code != http.StatusOK {
		t.Fatalf("create worker: %d %s", code, cb)
	}
	var conn connector
	if err := json.Unmarshal(cb, &conn); err != nil {
		t.Fatalf("decode worker: %v", err)
	}
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions", watchBody)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var rec inboundSubscription
	if err := json.Unmarshal(sb, &rec); err != nil {
		t.Fatalf("decode subscription: %v", err)
	}
	return rec.ID
}
