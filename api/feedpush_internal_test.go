package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
)

// Push delivery of the event feed
// (ADR-0433): a subscription on a
// cloudevents Worker is sent the feed after its cursor, a batch at a time, and the cursor
// moves only when the endpoint accepted the batch.

// feedEndpoint is a consumer of pushed batches: it records each request and answers
// what the test tells it to.
type feedEndpoint struct {
	mu       sync.Mutex
	status   int
	location string
	batches  [][]cloudEvent
	headers  []http.Header
}

func (e *feedEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var batch []cloudEvent
	_ = json.Unmarshal(body, &batch)
	e.batches = append(e.batches, batch)
	e.headers = append(e.headers, r.Header.Clone())
	if e.location != "" {
		w.Header().Set("Location", e.location)
	}
	if e.status != 0 {
		w.WriteHeader(e.status)
		_, _ = w.Write([]byte("the billing system is down for maintenance"))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (e *feedEndpoint) answer(status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = status
}

// received answers the batches delivered so far.
func (e *feedEndpoint) received() [][]cloudEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]cloudEvent(nil), e.batches...)
}

// feedPushServer is a server with the IT and HR catalogue's five feed rows, a consumer,
// and a cloudevents Worker pointing at it whose credential is in the vault. Delivery is
// driven by the test, on a clock the test holds.
func feedPushServer(t *testing.T) (*Server, *feedEndpoint, *time.Time) {
	t.Helper()
	srv := newServerWithOptions(t, WithFeedPushInterval(0))
	feedReachFixture(t, srv)
	ep := &feedEndpoint{}
	ts := httptest.NewServer(ep)
	t.Cleanup(ts.Close)
	if _, err := srv.vault.Set("billing-key", "s3cret"); err != nil {
		t.Fatalf("vault: %v", err)
	}
	var err error
	srv.do(func() {
		err = srv.connectors.Save(connector{ID: "wk-billing", Name: "billing", Kind: connectorKindCloudEvents,
			Endpoint: ts.URL + "/events", CredentialsRef: "billing-key", Enabled: true})
	})
	if err != nil {
		t.Fatalf("save worker: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	srv.feedPushClock = func() time.Time { return now }
	return srv, ep, &now
}

// subscribe creates a subscription over the route and answers it.
func subscribe(t *testing.T, srv *Server, body string) feedSubView {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/feed-subscriptions", body, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("subscribe %s: %d (%s)", body, code, raw)
	}
	var v feedSubView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return v
}

// storedSub reads a subscription's record back.
func storedSub(t *testing.T, srv *Server, id string) feedSubscription {
	t.Helper()
	var (
		rec feedSubscription
		ok  bool
		err error
	)
	srv.do(func() { rec, ok, err = srv.feedSubs.Get(id) })
	if err != nil || !ok {
		t.Fatalf("subscription %s: %v %v", id, ok, err)
	}
	return rec
}

// lastPosition is the position of the feed's newest row.
func lastPosition(t *testing.T, srv *Server) uint64 {
	t.Helper()
	at, err := srv.feedCursorAt(feedFromNow)
	if err != nil {
		t.Fatalf("last position: %v", err)
	}
	return at
}

// TestAFeedSubscriptionDeliversTheFeedToItsEndpoint: the feed's rows after the cursor are
// POSTed as one CloudEvents batch, with the Worker's credential as a bearer token, and the
// cursor moves to the last of them; the next round has nothing new and sends nothing.
func TestAFeedSubscriptionDeliversTheFeedToItsEndpoint(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)
	if sub.Cursor != 0 || !sub.Enabled {
		t.Fatalf("a new subscription = %+v, want enabled at the oldest row", sub)
	}

	srv.pushFeed(context.Background())
	got := ep.received()
	if len(got) != 1 || len(got[0]) != 5 {
		t.Fatalf("delivered %d batches %v, want one of five events", len(got), got)
	}
	h := ep.headers[0]
	if h.Get("Content-Type") != "application/cloudevents-batch+json" || h.Get("Authorization") != "Bearer s3cret" ||
		h.Get("Atlas-Feed-Subscription") != sub.ID {
		t.Fatalf("headers = %v", h)
	}
	if got[0][0].Type != "atlas.entitlement.granted" || got[0][0].SpecVersion != "1.0" || homeOf(got[0][0]) != "cat-it" {
		t.Fatalf("first event = %+v", got[0][0])
	}
	rec := storedSub(t, srv, sub.ID)
	if rec.Cursor != lastPosition(t, srv) || rec.DeliveredAt != 1_800_000_000 {
		t.Fatalf("after delivery = %+v, want the cursor at the last row", rec)
	}

	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 1 {
		t.Fatalf("a round with nothing new sent %d batches in all, want still 1", n)
	}
}

// TestAFailingEndpointIsHeldAndTriedAgainOnTheLadder: a refusal keeps the cursor where it
// was and holds the subscription — the next round does not ask — until the ladder's wait
// has passed; then a batch the endpoint accepts delivers everything and lifts the hold.
func TestAFailingEndpointIsHeldAndTriedAgainOnTheLadder(t *testing.T) {
	srv, ep, now := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)
	ep.answer(http.StatusServiceUnavailable)

	srv.pushFeed(context.Background())
	if rec := storedSub(t, srv, sub.ID); rec.Cursor != 0 {
		t.Fatalf("a refused batch moved the cursor to %d", rec.Cursor)
	}
	h, held := srv.feedPushes.hold(sub.ID)
	if !held || h.Failures != 1 || !h.RetryAt.Equal(now.Add(10*time.Second)) ||
		!strings.Contains(h.LastError, "503") || !strings.Contains(h.LastError, "maintenance") {
		t.Fatalf("hold = %+v %v", h, held)
	}
	_, list := serveInternal(t, srv, http.MethodGet, "/api/v1/feed-subscriptions", "", "")
	if !strings.Contains(string(list), `"failures":1`) || !strings.Contains(string(list), `"workerName":"billing"`) {
		t.Fatalf("the listing does not show the hold: %s", list)
	}

	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 1 {
		t.Fatalf("a held subscription was asked again: %d requests", n)
	}

	ep.answer(0)
	*now = now.Add(10 * time.Second)
	srv.pushFeed(context.Background())
	if got := ep.received(); len(got) != 2 || len(got[1]) != 5 {
		t.Fatalf("after the wait = %d batches, want the five events delivered", len(got))
	}
	if _, held := srv.feedPushes.hold(sub.ID); held {
		t.Fatal("an accepted batch left the hold in place")
	}
	if rec := storedSub(t, srv, sub.ID); rec.Cursor != lastPosition(t, srv) {
		t.Fatalf("cursor after recovery = %d", rec.Cursor)
	}
}

// TestTheLadderDoublesToItsCeiling: 10 s after the first failure, doubling, and never
// more than five minutes — the breaker's own ladder.
func TestTheLadderDoublesToItsCeiling(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 10 * time.Second, 2: 20 * time.Second, 3: 40 * time.Second,
		6: 5 * time.Minute, 40: 5 * time.Minute} {
		if got := feedPushBackoff(n); got != want {
			t.Errorf("after %d failures = %v, want %v", n, got, want)
		}
	}
}

// TestANarrowedSubscriptionIsSentItsCataloguesOnly: a subscription narrowed to HR is
// sent HR's two rows; one narrowed to a catalogue no row belongs to is sent nothing,
// and its cursor still moves past the rows, so it never reads them again.
func TestANarrowedSubscriptionIsSentItsCataloguesOnly(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	hr := subscribe(t, srv, `{"workerId":"wk-billing","reach":["cat-hr"]}`)
	srv.pushFeed(context.Background())
	got := ep.received()
	if len(got) != 1 || len(got[0]) != 2 || homeOf(got[0][0]) != "cat-hr" || homeOf(got[0][1]) != "cat-hr" {
		t.Fatalf("HR was sent %v", got)
	}
	if rec := storedSub(t, srv, hr.ID); rec.Cursor != lastPosition(t, srv) {
		t.Fatalf("HR's cursor = %d, want past the adopted right too", rec.Cursor)
	}

	srv.do(func() { _ = srv.catalogStore.SaveCatalog(catalog.Catalog{ID: "cat-empty"}) })
	empty := subscribe(t, srv, `{"workerId":"wk-billing","reach":["cat-empty"]}`)
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 1 {
		t.Fatalf("a subscription with nothing to send made a request: %d", n)
	}
	if rec := storedSub(t, srv, empty.ID); rec.Cursor != lastPosition(t, srv) || rec.DeliveredAt != 0 {
		t.Fatalf("the empty subscription = %+v, want its cursor at the end and nothing delivered", rec)
	}
}

// TestABacklogDrainsInBatchesWithOneCursorWrite: with a batch of two, five rows go in
// three requests in one round, in order.
func TestABacklogDrainsInBatchesWithOneCursorWrite(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing","batchSize":2}`)
	srv.pushFeed(context.Background())
	got := ep.received()
	if len(got) != 3 || len(got[0]) != 2 || len(got[1]) != 2 || len(got[2]) != 1 {
		t.Fatalf("batches = %d, want 2+2+1", len(got))
	}
	if got[0][1].ID == got[1][0].ID || storedSub(t, srv, sub.ID).Cursor != lastPosition(t, srv) {
		t.Fatalf("the batches overlap or the cursor did not reach the end")
	}
}

// TestASubscriptionTheRetentionPassedIsSwitchedOff: rows the feed dropped before they were
// delivered are not delivered around. The subscription is switched off with the reason
// and nothing is sent; enabling it and moving it to the oldest row held resumes it.
func TestASubscriptionTheRetentionPassedIsSwitchedOff(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)
	srv.do(func() {
		srv.proc.PruneFeed(2)
		_ = srv.proc.RunUntilIdle()
	})
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 0 {
		t.Fatalf("a subscription behind the retention was sent %d batches", n)
	}
	rec := storedSub(t, srv, sub.ID)
	if rec.Enabled || !strings.Contains(rec.DisabledReason, "retention dropped the rows after position 0") {
		t.Fatalf("after the cut = %+v", rec)
	}

	code, raw := serveInternal(t, srv, http.MethodPatch, "/api/v1/feed-subscriptions/"+sub.ID,
		`{"enabled":true,"from":"oldest"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("resume: %d (%s)", code, raw)
	}
	if rec := storedSub(t, srv, sub.ID); !rec.Enabled || rec.DisabledReason != "" || rec.Cursor != 2 {
		t.Fatalf("resumed = %+v, want enabled at the cut", rec)
	}
	srv.pushFeed(context.Background())
	if got := ep.received(); len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("after resuming = %v, want the three rows held", got)
	}
}

// TestDeliveryAsksOnlyAnEnabledCloudEventsWorker: a disabled subscription, one on a
// disabled Worker and one whose Worker went away are not delivered; nor does a redirect
// count as delivered — the credential is for the endpoint the Worker names.
func TestDeliveryAsksOnlyAnEnabledCloudEventsWorker(t *testing.T) {
	srv, ep, now := feedPushServer(t)
	off := subscribe(t, srv, `{"workerId":"wk-billing","enabled":false}`)
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 0 {
		t.Fatalf("a disabled subscription was sent %d batches", n)
	}
	serveInternal(t, srv, http.MethodPatch, "/api/v1/feed-subscriptions/"+off.ID, `{"enabled":true}`, "application/json")
	srv.do(func() {
		w, _, _ := srv.connectors.Get("wk-billing")
		w.Enabled = false
		_ = srv.connectors.Save(w)
	})
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 0 {
		t.Fatalf("a disabled worker was sent %d batches", n)
	}
	srv.do(func() {
		w, _, _ := srv.connectors.Get("wk-billing")
		w.Enabled = true
		_ = srv.connectors.Save(w)
	})

	ep.mu.Lock()
	ep.status, ep.location = http.StatusFound, "https://elsewhere.example/"
	ep.mu.Unlock()
	srv.pushFeed(context.Background())
	if h, held := srv.feedPushes.hold(off.ID); !held || !strings.Contains(h.LastError, "302") {
		t.Fatalf("a redirect = %+v %v, want a failure naming it", h, held)
	}
	if rec := storedSub(t, srv, off.ID); rec.Cursor != 0 {
		t.Fatalf("a redirect moved the cursor to %d", rec.Cursor)
	}

	*now = now.Add(time.Minute)
	srv.do(func() { _ = srv.connectors.Delete("wk-billing") })
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 1 {
		t.Fatalf("a subscription whose worker is gone was delivered: %d requests", n)
	}
}

// TestTheFeedSubscriptionRoutesRefuseWhatCannotBeDelivered: a subscription names a
// cloudevents Worker that exists, a batch size within the page bounds, a known "from" and
// catalogues that exist; its Worker does not change; and an unknown one is 404.
func TestTheFeedSubscriptionRoutesRefuseWhatCannotBeDelivered(t *testing.T) {
	srv, _, _ := feedPushServer(t)
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "wk-mail", Name: "mail", Kind: connectorKindMail, Enabled: true})
	})
	for body, want := range map[string]string{
		`{}`:                       "workerId is required",
		`{"workerId":"wk-nobody"}`: "no worker wk-nobody",
		`{"workerId":"wk-mail"}`:   "is a mail worker",
		`{"workerId":"wk-billing","from":"later"}`:   "from must be",
		`{"workerId":"wk-billing","batchSize":1001}`: "batchSize must be between",
		`{"workerId":"wk-billing","reach":["nope"]}`: "no catalogue",
		`not json`: "invalid JSON body",
	} {
		code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/feed-subscriptions", body, "application/json")
		if code != http.StatusBadRequest || !strings.Contains(string(raw), want) {
			t.Errorf("%s = %d (%s), want 400 naming %q", body, code, raw, want)
		}
	}

	sub := subscribe(t, srv, `{"workerId":"wk-billing","from":"now"}`)
	if sub.Cursor != lastPosition(t, srv) {
		t.Fatalf("a subscription from now starts at %d, want the last row", sub.Cursor)
	}
	path := "/api/v1/feed-subscriptions/" + sub.ID
	if code, raw := serveInternal(t, srv, http.MethodPatch, path, `{"workerId":"wk-mail"}`, "application/json"); code != http.StatusBadRequest ||
		!strings.Contains(string(raw), "does not change") {
		t.Errorf("changing the worker = %d (%s)", code, raw)
	}
	if code, _ := serveInternal(t, srv, http.MethodPatch, path, `{"reach":["nope"]}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("an unknown catalogue on update = %d, want 400", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodPatch, path, `{"batchSize":-1}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("a negative batch size = %d, want 400", code)
	}
	code, raw := serveInternal(t, srv, http.MethodPatch, path, `{"reach":["cat-it"],"batchSize":50,"from":"oldest"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("update: %d (%s)", code, raw)
	}
	if rec := storedSub(t, srv, sub.ID); rec.Cursor != 0 || rec.BatchSize != 50 || len(rec.Reach) != 1 {
		t.Fatalf("updated = %+v", rec)
	}
	if code, _ := serveInternal(t, srv, http.MethodPatch, "/api/v1/feed-subscriptions/nope", `{"enabled":false}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("updating an unknown subscription = %d, want 404", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, path, "", ""); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, path, "", ""); code != http.StatusNoContent {
		t.Errorf("deleting it again = %d, want 204", code)
	}
}

// TestDeletingTheWorkerEndsItsSubscriptions: a cloudevents Worker's subscriptions are its
// configuration and go with it.
func TestDeletingTheWorkerEndsItsSubscriptions(t *testing.T) {
	srv, _, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)
	if code, raw := serveInternal(t, srv, http.MethodDelete, "/api/v1/configured-workers/wk-billing", "", ""); code != http.StatusNoContent {
		t.Fatalf("delete worker: %d (%s)", code, raw)
	}
	var ok bool
	srv.do(func() { _, ok, _ = srv.feedSubs.Get(sub.ID) })
	if ok {
		t.Fatal("the subscription outlived its worker")
	}
}

// TestFeedSubscriptionsFailOverAnUnreadableStore: a store that cannot be read answers
// 500 on every route, and a delivery round over it delivers nothing.
func TestFeedSubscriptionsFailOverAnUnreadableStore(t *testing.T) {
	srv, ep, _ := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing"}`)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "feed-subscriptions"))
	if code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/feed-subscriptions", "", ""); code != http.StatusInternalServerError {
		t.Errorf("list = %d, want 500", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/feed-subscriptions", `{"workerId":"wk-billing"}`, "application/json"); code != http.StatusInternalServerError {
		t.Errorf("create = %d, want 500", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodPatch, "/api/v1/feed-subscriptions/"+sub.ID, `{"enabled":false}`, "application/json"); code != http.StatusInternalServerError {
		t.Errorf("update = %d, want 500", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, "/api/v1/feed-subscriptions/"+sub.ID, "", ""); code != http.StatusInternalServerError {
		t.Errorf("delete = %d, want 500", code)
	}
	srv.pushFeed(context.Background())
	if n := len(ep.received()); n != 0 {
		t.Errorf("a round over an unreadable store delivered %d batches", n)
	}
	var delErr error
	srv.do(func() { delErr = srv.deleteFeedSubscriptionsOf("wk-billing") })
	if delErr == nil {
		t.Error("ending a worker's subscriptions over an unreadable store reported success")
	}
}

// TestACloudEventsWorkerNamesAnHTTPSEndpoint: the endpoint the feed goes to is https,
// or http on this machine; it carries no credentials of its own; an edit is held to the
// same rule.
func TestACloudEventsWorkerNamesAnHTTPSEndpoint(t *testing.T) {
	for endpoint, want := range map[string]string{
		"":                                  "requires the endpoint",
		"billing.example/events":            "absolute https URL",
		"ftp://billing.example/events":      "absolute https URL",
		"http://billing.example/events":     "must use https",
		"https://user:pw@billing.example/x": "carries no credentials",
		"https://billing.example/events":    "",
		"http://127.0.0.1:8080/events":      "",
		"http://localhost/events":           "",
	} {
		p := createConnectorParams{Kind: connectorKindCloudEvents, Endpoint: endpoint, Model: "x", Provider: "smtp"}
		if got := validateCloudEventsConnector(&p); (want == "" && got != "") || !strings.Contains(got, want) {
			t.Errorf("%q = %q, want %q", endpoint, got, want)
		}
		if p.Model != "" || p.Provider != "" {
			t.Errorf("%q kept fields of another kind: %+v", endpoint, p)
		}
	}
	rec := connector{Kind: connectorKindCloudEvents, Endpoint: "http://billing.example/events"}
	if msg := normalizeConnectorUpdate(&rec); !strings.Contains(msg, "must use https") {
		t.Errorf("an edit to plain http = %q", msg)
	}
	if k, ok := lookupBuiltInManagedWorkerType(connectorKindCloudEvents); !ok || k.Title != "CloudEvents endpoint" {
		t.Errorf("the worker type = %+v %v", k, ok)
	}
	srv := newServerWithOptions(t, WithFeedPushInterval(0))
	for _, kind := range managedConnectorKinds {
		if kind.name != connectorKindCloudEvents {
			continue
		}
		kind.newRegistry(srv)
		kind.registerHandlers(srv, nil)
		if err := kind.rebuild(srv); err != nil {
			t.Errorf("rebuild: %v", err)
		}
		if _, ok := kind.problem(srv, "billing"); ok {
			t.Error("a cloudevents worker reported a registry problem")
		}
	}
}

// TestTheFeedPusherRunsUntilTheServerStops: with an interval set, delivery runs on its
// own and stops with the server.
func TestTheFeedPusherRunsUntilTheServerStops(t *testing.T) {
	ep := &feedEndpoint{}
	ts := httptest.NewServer(ep)
	defer ts.Close()
	srv := newServerWithOptions(t, WithFeedPushInterval(20*time.Millisecond))
	feedReachFixture(t, srv)
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "wk-1", Name: "one", Kind: connectorKindCloudEvents, Endpoint: ts.URL, Enabled: true})
	})
	subscribe(t, srv, `{"workerId":"wk-1"}`)
	deadline := time.Now().Add(5 * time.Second)
	for len(ep.received()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(ep.received()) == 0 {
		t.Fatal("the pusher delivered nothing within five seconds")
	}
	if h := ep.headers[0].Get("Authorization"); h != "" {
		t.Errorf("a worker without a credential sent %q", h)
	}
}

// TestDeliveryNeverMovesACursorAnAdministratorMoved: a batch that was out while an
// administrator rewound the subscription does not write its cursor over the rewind, nor
// does a cut switch off a subscription that moved meanwhile.
func TestDeliveryNeverMovesACursorAnAdministratorMoved(t *testing.T) {
	srv, _, now := feedPushServer(t)
	sub := subscribe(t, srv, `{"workerId":"wk-billing","from":"now"}`)
	last := lastPosition(t, srv)
	srv.do(func() {
		srv.advanceFeedCursor(sub.ID, 0, 99, true, *now)
		srv.disableFeedSubscription(sub.ID, 0, "feed behind: gone")
		srv.advanceFeedCursor("nope", 0, 99, true, *now)
	})
	if rec := storedSub(t, srv, sub.ID); rec.Cursor != last || !rec.Enabled || rec.DeliveredAt != 0 {
		t.Fatalf("after a stale write = %+v, want it untouched", rec)
	}
}

// TestAnEndpointThatCannotBeReachedOrAFeedThatCannotBeReadHolds: a refused connection and
// a catalogue store that cannot be read are failures like a refusal — the subscription is
// held where it was, never moved past rows it could not place.
func TestAnEndpointThatCannotBeReachedOrAFeedThatCannotBeReadHolds(t *testing.T) {
	srv, _, now := feedPushServer(t)
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "wk-gone", Name: "gone", Kind: connectorKindCloudEvents,
			Endpoint: "http://127.0.0.1:1/events", Enabled: true})
	})
	gone := subscribe(t, srv, `{"workerId":"wk-gone"}`)
	srv.feedPushClient = &http.Client{Timeout: time.Second}
	srv.pushFeed(context.Background())
	if h, held := srv.feedPushes.hold(gone.ID); !held || !strings.Contains(h.LastError, "deliver to http://127.0.0.1:1/events") {
		t.Fatalf("an unreachable endpoint = %+v %v", h, held)
	}

	billing := subscribe(t, srv, `{"workerId":"wk-billing","reach":["cat-it"]}`)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "catalog", "items"))
	*now = now.Add(time.Hour)
	srv.pushFeed(context.Background())
	if h, held := srv.feedPushes.hold(billing.ID); !held || h.Failures != 1 {
		t.Fatalf("an unreadable catalogue = %+v %v, want a hold", h, held)
	}
	if rec := storedSub(t, srv, billing.ID); rec.Cursor != 0 {
		t.Fatalf("an unreadable catalogue moved the cursor to %d", rec.Cursor)
	}
}
