package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// incidentFeedBPMN is one service task, so a failed job parks a token on it.
const incidentFeedBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas.test">
  <process id="zahlung" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="buchen"><extensionElements><zeebe:taskDefinition type="buchen"/></extensionElements></serviceTask>
    <endEvent id="ende"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="buchen"/>
    <sequenceFlow id="f2" sourceRef="buchen" targetRef="ende"/>
  </process>
</definitions>`

// secretInMessage is what a careless worker reports: it must never leave on the feed.
const secretInMessage = "booking for anna@example.org refused: password hunter2 expired"

// raiseIncident deploys the model, starts an instance and fails its job with no retries
// left, as a worker does. It answers the definition key and the parked element instance.
func raiseIncident(t *testing.T, srv *Server) (defKey, elementInstance uint64) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", incidentFeedBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", `{}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, raw)
	}
	job := lease(t, srv, "buchen")
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"retries":0,"message":%q}`, job.LeaseToken, secretInMessage)
	code, raw = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", job.Key), body, "application/json")
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("fail: %d %s", code, raw)
	}
	var found []*model.IncidentValue
	srv.do(func() {
		_ = srv.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
			found = append(found, v)
			return nil
		})
	})
	if len(found) != 1 {
		t.Fatalf("incidents = %d, want 1", len(found))
	}
	return dep.Key, found[0].ElementInstanceKey
}

// eventsOfType are the events of one type on a page.
func eventsOfType(page eventPage, typ string) []cloudEvent {
	var out []cloudEvent
	for _, ev := range page.Events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// TestAnIncidentRaisedAndResolvedLeavesOnTheFeed (ADR-0435): a job that runs out of
// retries puts atlas.incident.raised on the feed, from the engine's source, naming the
// parked element instance and its cause — definition, element, type — and never the
// message; resolving it puts atlas.incident.resolved after it. A reader narrowed to
// catalogues is given neither: a fact of the engine belongs to no catalogue.
func TestAnIncidentRaisedAndResolvedLeavesOnTheFeed(t *testing.T) {
	srv := newServerWithOptions(t, WithExternalURL("https://atlas.example"))
	defKey, elementInstance := raiseIncident(t, srv)

	code, page, _ := feedPage(t, srv, "")
	raised := eventsOfType(page, eventcatalog.IncidentRaised)
	if code != http.StatusOK || len(raised) != 1 {
		t.Fatalf("feed = %d %+v, want one incident raised", code, page)
	}
	ev := raised[0]
	if ev.Source != "https://atlas.example/engine" || !strings.HasPrefix(ev.Subject, "instances/") {
		t.Errorf("the incident's envelope = %+v", ev)
	}
	data := ev.Data.(map[string]any)
	for name, want := range map[string]any{
		"elementInstanceKey": float64(elementInstance), "processDefKey": float64(defKey), "processId": "zahlung",
		"elementId": "buchen", "incidentType": "job", "version": float64(1),
	} {
		if data[name] != want {
			t.Errorf("data %s = %v, want %v", name, data[name], want)
		}
	}
	if _, ok := data["jobKey"]; !ok {
		t.Errorf("a job's incident carries no jobKey: %v", data)
	}
	if _, ok := data["homeCatalog"]; ok {
		t.Errorf("a fact of the engine names a home catalogue: %v", data)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "anna@example.org") {
		t.Fatalf("the incident's message left on the feed: %s", raw)
	}

	// A reader narrowed to catalogues passes over it, its cursor with it.
	view, nodeID, part, defs, err := srv.feedSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	narrowed, err := srv.readFeedPage(view, defs, part, nodeID, 0, 100, map[string]bool{"cat-it": true})
	view.Close()
	if err != nil || len(narrowed.Events) != 0 || narrowed.Next != page.Next {
		t.Fatalf("a narrowed reader = %+v %v, want nothing and the cursor at %s", narrowed, err, page.Next)
	}

	code, out := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/resolve", elementInstance), `{"retries":1}`, "application/json")
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("resolve: %d %s", code, out)
	}
	code, after, _ := feedPage(t, srv, "?after="+page.Next)
	resolved := eventsOfType(after, eventcatalog.IncidentResolved)
	if code != http.StatusOK || len(resolved) != 1 {
		t.Fatalf("after the resolve = %d %+v, want one incident resolved", code, after)
	}
	rd := resolved[0].Data.(map[string]any)
	if rd["elementInstanceKey"] != float64(elementInstance) || rd["resolvedAt"] == nil || rd["processDefKey"] != float64(defKey) {
		t.Errorf("the resolution's data = %v", rd)
	}
}

// TestWithTheCatalogueOffTheFeedCarriesTheEnginesFactsOnly (ADR-0435 §6): a server
// whose service catalogue is switched off still serves the feed and pushes it, passing
// over the catalogue's rows, so an incident is not silenced by the shop being off.
func TestWithTheCatalogueOffTheFeedCarriesTheEnginesFactsOnly(t *testing.T) {
	srv := newServerWithOptions(t, WithoutCatalogue(), WithFeedPushInterval(0))
	srv.do(func() {
		srv.proc.GrantEntitlement(model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1",
			Since: 1000, Origin: model.OriginOrdered})
		if err := srv.proc.RunUntilIdle(); err != nil {
			t.Errorf("RunUntilIdle: %v", err)
		}
	})
	raiseIncident(t, srv)

	// The route is mounted, and answers the engine's facts only.
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/events", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/events with the catalogue off = %d %s", code, raw)
	}
	var page eventPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Type != eventcatalog.IncidentRaised {
		t.Fatalf("the feed with the catalogue off = %+v, want the incident alone", page.Events)
	}
	// The grant is in the feed's rows all the same — applyToState folds it whatever
	// the switch says (I4) — and the read passes over it.
	view, _, part, _, err := srv.feedSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var kinds []state.FeedKind
	_ = view.FeedAfter(part, 0, func(e state.FeedEntry) error { kinds = append(kinds, e.Kind); return nil })
	view.Close()
	if len(kinds) != 2 || kinds[0] != state.FeedGranted || kinds[1] != state.FeedIncidentRaised {
		t.Fatalf("the feed's rows = %v, want the grant and the incident", kinds)
	}

	// And pushed: a subscription is sent the incident, not the grant.
	ep := &feedEndpoint{}
	ts := httptest.NewServer(ep)
	t.Cleanup(ts.Close)
	if _, err := srv.vault.Set("billing-key", "s3cret"); err != nil {
		t.Fatalf("vault: %v", err)
	}
	srv.do(func() {
		err = srv.connectors.Save(connector{ID: "wk-billing", Name: "billing", Kind: connectorKindCloudEvents,
			Endpoint: ts.URL + "/events", CredentialsRef: "billing-key", Enabled: true})
	})
	if err != nil {
		t.Fatalf("save worker: %v", err)
	}
	subscribe(t, srv, `{"workerId":"wk-billing"}`)
	srv.pushFeed(context.Background())
	batches := ep.received()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].Type != eventcatalog.IncidentRaised {
		t.Fatalf("pushed with the catalogue off = %+v, want one batch with the incident", batches)
	}
}
