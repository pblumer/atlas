package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/wikiwikibangbang"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// wikiStub is the part of a wikiwikibangbang wiki a watch reads — the account's
// standing, the watch of a space, the change feed and the finished items among the ones
// asked for — scripted by the test. The connector's own tests hold the reading to the
// wiki's behaviour; this one is about what the engine does with what it read.
type wikiStub struct {
	srv *httptest.Server

	mu       sync.Mutex
	scope    string
	last     int
	changes  []map[string]any
	items    map[string]map[string]any
	finished map[string]bool
	watched  map[string]bool
}

func newWikiStub(t *testing.T, scope string) *wikiStub {
	t.Helper()
	w := &wikiStub{scope: scope, last: 5, items: map[string]map[string]any{}, finished: map[string]bool{}, watched: map[string]bool{}}
	w.srv = httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	return w
}

var quotedID = regexp.MustCompile(`"([0-9a-f]{32})"`)

func (w *wikiStub) serve(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	reply := func(v any) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/me":
		reply(map[string]any{"admin": false, "erasure": false, "scope": w.scope,
			"user": map[string]any{"id": "0f0e", "username": "atlas-leser"}})
	case r.Method == http.MethodPut && r.URL.Path == "/api/spaces/OPS/watch":
		w.watched["OPS"] = true
		reply(map[string]any{"watching": true})
	case r.Method == http.MethodGet && r.URL.Path == "/api/changes":
		after, err := strconv.Atoi(r.URL.Query().Get("after"))
		out := []any{}
		for _, c := range w.changes {
			if err == nil && c["seq"].(int) > after {
				out = append(out, c)
			}
		}
		reply(map[string]any{"changes": out, "last": w.last})
	case r.Method == http.MethodGet && r.URL.Path == "/api/items":
		out := []any{}
		for _, m := range quotedID.FindAllStringSubmatch(r.URL.Query().Get("query"), -1) {
			if it, ok := w.items[m[1]]; ok && w.finished[m[1]] {
				out = append(out, it)
			}
		}
		reply(map[string]any{"items": out})
	default:
		rw.WriteHeader(http.StatusNotFound)
		reply(map[string]any{"error": "no such route"})
	}
}

// finish records a task of OPS as finished at a time, and the change that did it.
func (w *wikiStub) finish(id, key string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last++
	w.changes = append(w.changes, map[string]any{"seq": w.last, "item": id, "at": at})
	w.items[id] = map[string]any{"id": id, "key": key, "type": "task", "space": "OPS", "title": "Aufgabe " + key,
		"state": "done", "stateSince": at.UTC().Format(time.RFC3339Nano), "properties": map[string]any{"assignee": "ana"},
		"labels": []any{}, "version": 3, "body": []any{map[string]any{"kind": "paragraph", "text": "secret notes"}}}
	w.finished[id] = true
}

const (
	wikiTaskA = "0de480317d5a01d67a16aa208b67a4d2"
	wikiTaskB = "4dfc48248f1e23b35bc005ea230b2059"
)

func TestValidateAWikiWatch(t *testing.T) {
	ok := func() inboundSubscription { return inboundSubscription{WikiSpace: "OPS", StartFromTip: true} }
	for _, tc := range []struct {
		name string
		kind string
		rec  func() inboundSubscription
		want string
	}{
		{"a complete watch", connectorKindWiki, ok, ""},
		{"a narrowed watch", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.WikiQuery = `label = "atlas"`
			return r
		}, ""},
		{"no space", connectorKindWiki, func() inboundSubscription { return inboundSubscription{StartFromTip: true} }, "wikiSpace is required"},
		{"a space that is no key", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.WikiSpace = "ops"
			return r
		}, "not a space key"},
		{"a query that orders", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.WikiQuery = "label = x ORDER BY key"
			return r
		}, "ORDER BY"},
		{"a query that closes the watch's group", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.WikiQuery = "x) OR (y"
			return r
		}, "parenthesis"},
		{"a clio subject", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.WatchedSubject = "/employees"
			return r
		}, "another kind's watch"},
		{"a lag", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.LagSeconds = 30
			return r
		}, "lagSeconds"},
		{"a backfill", connectorKindWiki, func() inboundSubscription {
			r := ok()
			r.StartFromTip = false
			return r
		}, "cannot backfill"},
		{"a wiki space on a jira watch", connectorKindJira, func() inboundSubscription {
			return inboundSubscription{JQL: "project = OPS", WikiSpace: "OPS"}
		}, "belong to a wikiwikibangbang watch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec()
			got := validateInboundWatch(tc.kind, &rec)
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("a usable watch was refused: %s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("refusal = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

func TestDescribeAndPaceAWikiWatch(t *testing.T) {
	if got := describeInboundWatch(connectorKindWiki, inboundSubscription{WikiSpace: "OPS"}); got != "tasks finished in space OPS" {
		t.Errorf("describe = %q", got)
	}
	if got := describeInboundWatch(connectorKindWiki, inboundSubscription{WikiSpace: "OPS", WikiQuery: `label = "atlas"`}); got != `tasks finished in space OPS matching label = "atlas"` {
		t.Errorf("describe = %q", got)
	}
	if got := inboundWatched(inboundSubscription{WikiSpace: "OPS"}); got != "finished tasks in space OPS" {
		t.Errorf("watched = %q", got)
	}
	if got := inboundCadence(connectorKindWiki, inboundSubscription{}); got != wikiDefaultPoll {
		t.Errorf("cadence = %v, want the wiki default", got)
	}
}

// Each task has its own mark, sequenced by when it was finished; the instance gets the
// event as one object, without the task's body, and a correlation key binds the flat
// names and the object's alike.
func TestAWikiSourceMarksEachTaskByWhenItWasFinished(t *testing.T) {
	stub := newWikiStub(t, "read")
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	saveWikiWorker(t, srv, "w1", "wiki-lesen", stub.srv.URL, "wiki-lesen")
	t.Setenv("ATLAS_CONNECTOR_WIKI_LESEN_TOKEN", wikiBundle)
	var (
		client wikiSource
		err    error
	)
	srv.do(func() {
		c, _, _ := srv.connectors.Get("w1")
		client.client, err = srv.wikiWatchClientOf(c, time.Now())
	})
	if err != nil {
		t.Fatalf("wikiWatchClientOf: %v", err)
	}
	rec := inboundSubscription{ID: "s1", ConnectorID: "w1", WikiSpace: "OPS", MessageName: "aufgabe.erledigt", StartFromTip: true}
	cursor, done, err := client.Prime(context.Background(), rec)
	if err != nil || !done || !strings.HasPrefix(cursor, "5|") {
		t.Fatalf("Prime = %q, %v, %v; want the feed's end and done", cursor, done, err)
	}
	if !stub.watched["OPS"] {
		t.Error("the watch primed without watching the space")
	}
	at := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	stub.finish(wikiTaskA, "OPS-7", at)
	rec.LastEventID = cursor
	events, next, err := client.Read(context.Background(), rec, 256)
	if err != nil || len(events) != 1 || !strings.HasPrefix(next, "6|") {
		t.Fatalf("Read = %d events, cursor %q, %v", len(events), next, err)
	}
	ev := events[0]
	if ev.MarkKey != wikiTaskA || ev.Seq != uint64(at.UnixMilli()) {
		t.Errorf("mark = %q/%d, want the task's id and when it was finished", ev.MarkKey, ev.Seq)
	}
	if got := inboundSourceID(connectorKindWiki, rec, ev.MarkKey); got != "wikiwikibangbang:w1:s1:"+wikiTaskA {
		t.Errorf("source id = %q", got)
	}
	payload := ev.payload()
	if len(payload) != 1 {
		t.Fatalf("payload = %v, want the event as one variable", payload)
	}
	event, _ := payload[wikiWatchVariable].(map[string]any)
	if event["eventType"] != "wikiwikibangbang.item.finished" || event["itemKey"] != "OPS-7" || event["state"] != "done" {
		t.Errorf("event = %v", event)
	}
	if item, _ := event["item"].(map[string]any); item == nil || item["body"] != nil {
		t.Errorf("item = %v, want the task without its body", event["item"])
	}
	for _, key := range []string{"itemId", "wikiEvent.itemId"} {
		compiled, err := expr.CompileAuto(key)
		if err != nil {
			t.Fatalf("compile %q: %v", key, err)
		}
		if got := correlationKeyOf(compiled, ev.Fields); got != wikiTaskA {
			t.Errorf("correlation key %q = %q, want the task's id", key, got)
		}
	}
}

// The engine keeps the client it reads a wiki with, so what the client learnt — the
// standing, the watched spaces — is not asked again on every tick; it builds a new one
// when the Worker's endpoint or credential changes, and lets one go that no watch reads
// with any more.
func TestTheEngineKeepsItsWikiClient(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	t.Setenv("ATLAS_CONNECTOR_WIKI_LESEN_TOKEN", wikiBundle)
	c := connector{ID: "w1", Name: "wiki-lesen", Kind: connectorKindWiki, Endpoint: "https://wiki.example.ch", CredentialsRef: "wiki-lesen", Enabled: true}
	now := time.Unix(1_000_000, 0)
	at := func(c connector, d time.Duration) (cl *wikiwikibangbang.Client, err error) {
		srv.do(func() { cl, err = srv.wikiWatchClientOf(c, now.Add(d)) })
		return cl, err
	}
	first, err := at(c, 0)
	if err != nil {
		t.Fatalf("wikiWatchClientOf: %v", err)
	}
	if again, _ := at(c, time.Second); again != first {
		t.Error("a second read built a second client")
	}
	if later, _ := at(c, wikiWatchClientFor+time.Second); later != first {
		t.Error("the credential was read again and had not changed, yet the client was replaced")
	}
	t.Setenv("ATLAS_CONNECTOR_WIKI_LESEN_TOKEN", `{"token":"wwbb_rotated-token-value"}`)
	if rotated, _ := at(c, wikiWatchClientFor+2*time.Second); rotated != first {
		t.Error("a rotated token was read with before the client's time was up")
	}
	rotated, _ := at(c, 2*wikiWatchClientFor+3*time.Second)
	if rotated == first {
		t.Error("a rotated token was not read with once the client's time was up")
	}
	moved := c
	moved.Endpoint = "https://wiki2.example.ch"
	if other, _ := at(moved, 2*wikiWatchClientFor+4*time.Second); other == rotated {
		t.Error("a Worker moved to another wiki kept its old client")
	}
	broken := c
	broken.Endpoint = "http://wiki.example.ch"
	if _, err := at(broken, 0); err == nil {
		t.Error("a Worker whose token would cross the network in the clear got a client")
	}
	srv.do(func() {
		_ = srv.connectors.Save(c)
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s1", ConnectorID: "w1", WikiSpace: "OPS", MessageName: "m", Enabled: true, StartFromTip: true, CreatedAt: 1})
	})
	var subs []pendingSub
	srv.do(func() { subs = srv.resolveInboundSubs() })
	if len(subs) != 1 {
		t.Fatalf("resolved %d watches, want the wiki watch", len(subs))
	}
	if _, ok := subs[0].source.(wikiSource); !ok || subs[0].kind != connectorKindWiki {
		t.Errorf("source = %T kind %q", subs[0].source, subs[0].kind)
	}
	srv.do(func() {
		rec, _, _ := srv.inboundSubs.Get("s1")
		rec.Enabled = false
		_ = srv.inboundSubs.Save(rec)
		_ = srv.resolveInboundSubs()
	})
	var held int
	srv.do(func() { held = len(srv.wikiWatchClients) })
	if held != 0 {
		t.Errorf("the engine holds %d wiki clients no watch reads with", held)
	}
}

// waitForTaskBPMN is the process the watch is for: it waits for the task it opened, by
// the task's id, and then for something that never comes, so the instance stays to be
// read.
const waitForTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Mdone" name="aufgabe.erledigt">
    <extensionElements><zeebe:subscription correlationKey="= aufgabe.id"/></extensionElements>
  </message>
  <message id="Mnever" name="never">
    <extensionElements><zeebe:subscription correlationKey="= aufgabe.id"/></extensionElements>
  </message>
  <process id="aufgabeAbwarten" isExecutable="true">
    <startEvent id="s"/>
    <intermediateCatchEvent id="wait"><messageEventDefinition messageRef="Mdone"/></intermediateCatchEvent>
    <intermediateCatchEvent id="park"><messageEventDefinition messageRef="Mnever"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="park"/>
    <sequenceFlow id="f3" sourceRef="park" targetRef="e"/>
  </process>
</definitions>`

// TestAWikiWatchWakesTheProcessThatWaitsForItsTask is the slice the watch exists for:
// two instances wait, each for its own task; the watch is created over HTTP on a Worker
// whose token reads, primes to the end of the feed, and when one task is finished it
// wakes the instance waiting for that task and no other, handing it the event.
func TestAWikiWatchWakesTheProcessThatWaitsForItsTask(t *testing.T) {
	stub := newWikiStub(t, "read")
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	h := srv.Handler()
	key := deployBPMN(t, h, waitForTaskBPMN)
	startInstance(t, h, key, `{"aufgabe":{"id":"`+wikiTaskA+`","key":"OPS-7"}}`)
	startInstance(t, h, key, `{"aufgabe":{"id":"`+wikiTaskB+`","key":"OPS-8"}}`)

	t.Setenv("ATLAS_CONNECTOR_WIKI_LESEN_TOKEN", wikiBundle)
	x := deployTestHarness{t, h}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"wiki-lesen","kind":"wikiwikibangbang","endpoint":"`+stub.srv.URL+`","credentialsRef":"wiki-lesen"}`)
	if code != http.StatusOK {
		t.Fatalf("create worker: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	if code, b := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"messageName":"aufgabe.erledigt","correlationKey":"= itemId","wikiSpace":"OPS","startFromTip":false}`); code != http.StatusBadRequest || !strings.Contains(string(b), "cannot backfill") {
		t.Fatalf("a backfilling watch = %d %s, want refused", code, b)
	}
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"messageName":"aufgabe.erledigt","correlationKey":"= itemId","wikiSpace":"OPS"}`)
	if code != http.StatusOK {
		t.Fatalf("create watch: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	srv.pollInbound(context.Background()) // primes at the end of the feed
	srv.do(func() { srv.markInboundPolled(sub.ID, 0) })
	srv.pollInbound(context.Background()) // nothing finished yet
	if n := activeInstances(t, srv); n != 2 {
		t.Fatalf("active = %d before any task was finished", n)
	}

	stub.finish(wikiTaskA, "OPS-7", time.Now().Add(time.Minute))
	srv.do(func() { srv.markInboundPolled(sub.ID, 0) })
	srv.pollInbound(context.Background())

	waiting, woken := 0, 0
	for _, vars := range activeInstanceVars(t, srv) {
		if _, ok := vars["wikiEvent"]; !ok {
			waiting++
			if !strings.Contains(vars["aufgabe"], wikiTaskB) {
				t.Errorf("the instance still waiting is %s; want the one for the task not finished", vars["aufgabe"])
			}
			continue
		}
		woken++
		var event struct {
			ItemID  string `json:"itemId"`
			ItemKey string `json:"itemKey"`
			Item    struct {
				Properties map[string]any `json:"properties"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(vars["wikiEvent"]), &event); err != nil {
			t.Fatalf("wikiEvent = %q: %v", vars["wikiEvent"], err)
		}
		if event.ItemID != wikiTaskA || event.ItemKey != "OPS-7" || event.Item.Properties["assignee"] != "ana" {
			t.Errorf("wikiEvent = %+v", event)
		}
	}
	if waiting != 1 || woken != 1 {
		t.Fatalf("waiting %d, woken %d; want one of each", waiting, woken)
	}
	var stored inboundSubscription
	srv.do(func() { stored, _, _ = srv.inboundSubs.Get(sub.ID) })
	if !strings.HasPrefix(stored.LastEventID, "6|") {
		t.Errorf("cursor = %q, want it past the finish", stored.LastEventID)
	}
}

// A watch on a Worker whose token can write reads nothing, and says why where the
// operator looks: the watch's own listing.
func TestAWikiWatchSaysWhyItRefusesATokenThatCanWrite(t *testing.T) {
	stub := newWikiStub(t, "full")
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	t.Setenv("ATLAS_CONNECTOR_WIKI_BOT_TOKEN", wikiBundle)
	srv.do(func() {
		saveWikiWorker(t, srv, "w1", "wiki-bot", stub.srv.URL, "wiki-bot")
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s1", ConnectorID: "w1", WikiSpace: "OPS", MessageName: "m",
			Enabled: true, StartFromTip: true, CreatedAt: 1})
	})
	srv.pollInbound(context.Background())
	code, b := serveInternal(t, srv, http.MethodGet, "/api/v1/connectors/w1/inbound-subscriptions", "", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, b)
	}
	if !strings.Contains(string(b), "scope read") || stub.watched["OPS"] {
		t.Errorf("listing = %s; want the refusal named and nothing watched", b)
	}
}

// activeInstanceVars reads the root-scope variables of every active instance, as the stored
// text of each value.
func activeInstanceVars(t *testing.T, srv *Server) []map[string]string {
	t.Helper()
	var out []map[string]string
	srv.do(func() {
		_ = srv.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
			vars := map[string]string{}
			out = append(out, vars)
			return srv.store.VariablesOfScope(key, func(v *model.VariableValue) error {
				vars[v.Name] = v.Text
				return nil
			})
		})
	})
	return out
}
