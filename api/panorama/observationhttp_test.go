package panorama

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createObservedModel imports a model that binds one application, and returns its id.
func createObservedModel(t *testing.T, fx *serviceFixture) string {
	t.Helper()
	created := requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
		map[string]any{
			"applicationId": "app-1", "name": "Landscape",
			"notation": NotationArchiMate32, "xml": string(minimalModel(t)),
		}, http.StatusCreated)
	var summary Summary
	decodeResponse(t, created, &summary)
	return summary.ID
}

// observe drives the route for one model.
func observe(t *testing.T, fx *serviceFixture, id string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+id+"/observations", nil)
	req.SetPathValue("id", id)
	return invoke(t, fx.service.HandleObservations, req, status)
}

// TestObservationsRouteAnswersFromTheServersFacts is the route end to end: a
// stored model, the server's current view of what it binds, and a document that
// joins them without the model being touched.
func TestObservationsRouteAnswersFromTheServersFacts(t *testing.T) {
	fx := newServiceFixture(t)
	id := createObservedModel(t, fx)
	fx.facts = Facts{Applications: map[string]Fact{"app-1": {
		Source: SourceDeployments, State: StateHealthy, Reason: "2 process(es) deployed.",
	}}}

	var doc ObservationDocument
	decodeResponse(t, observe(t, fx, id, http.StatusOK), &doc)
	if doc.ObservedAt == 0 || doc.ContractVersion == 0 {
		t.Fatalf("document header = %+v", doc)
	}
	// Nothing is out of this document's reach, and the empty list is how it says so.
	if len(doc.Unavailable) != 0 {
		t.Errorf("the document declares %#v unavailable", doc.Unavailable)
	}

	// Reading the live view must not change the stored document. That is ADR-0189's
	// "the declarative XML is never mutated by polling", asserted rather than
	// assumed: a drawing that accumulated health would be a second copy of the
	// truth, and a stale one.
	xmlReq := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+id+"/xml", nil)
	xmlReq.SetPathValue("id", id)
	exported := invoke(t, fx.service.HandleXML, xmlReq, http.StatusOK)
	if exported.Body.String() != string(minimalModel(t)) {
		t.Error("observing the model changed the stored document")
	}
	if got := exported.Header().Get("X-Panorama-Revision"); got != "1" {
		t.Errorf("revision after observing = %q, want it untouched at 1", got)
	}
}

// TestObservationsRefuseRatherThanReportADeadLandscape covers the three ways this
// route can fail to gather facts. Each one would otherwise produce a document of
// unbound observations — a model that looks like an architecture where nothing is
// running, which is the most damaging thing a live view can say incorrectly.
func TestObservationsRefuseRatherThanReportADeadLandscape(t *testing.T) {
	t.Run("the collector fails", func(t *testing.T) {
		fx := newServiceFixture(t)
		id := createObservedModel(t, fx)
		fx.factsErr = errors.New("the instance table is on fire")
		body := observe(t, fx, id, http.StatusInternalServerError).Body.String()
		if !strings.Contains(body, "collect observations") {
			t.Errorf("body = %s, want it to name which half failed", body)
		}
	})

	t.Run("the caller may not read the model", func(t *testing.T) {
		fx := newServiceFixture(t)
		id := createObservedModel(t, fx)
		// The application's scope closes underneath an existing model.
		fx.access["app-1"] = ApplicationAccess{Exists: true}
		observe(t, fx, id, http.StatusNotFound)
	})

	t.Run("no such model", func(t *testing.T) {
		fx := newServiceFixture(t)
		observe(t, fx, testModelID, http.StatusNotFound)
	})
}

// TestObservationsRefuseWhenNothingObserves: a server wired without a fact source
// answers 501 rather than with a document of silence. "This build does not do
// that" and "everything you asked about is idle" are different answers, and only
// one of them is true.
func TestObservationsRefuseWhenNothingObserves(t *testing.T) {
	fx := newServiceFixture(t)
	id := createObservedModel(t, fx)
	fx.service.facts = nil

	body := observe(t, fx, id, http.StatusNotImplemented).Body.String()
	if !strings.Contains(body, "observes nothing") {
		t.Errorf("body = %s, want it to say this server observes nothing", body)
	}
}

// TestObservationsRefuseAClosingServer. The resolver takes its own run-loop turn
// now, because part of what it gathers waits on the network — so "the loop
// declined to run" is a condition it reports rather than one this handler can
// observe, and [ErrShuttingDown] is that report.
//
// The distinction is worth a status of its own: a document built from facts nobody
// gathered says every element is unobserved, which reads as an architecture where
// nothing is running. "The server is going away" and "something broke" are also
// different things to tell a caller, which is why this is 503 and not 500.
func TestObservationsRefuseAClosingServer(t *testing.T) {
	fx := newServiceFixture(t)
	id := createObservedModel(t, fx)
	fx.factsErr = ErrShuttingDown

	body := observe(t, fx, id, http.StatusServiceUnavailable).Body.String()
	if !strings.Contains(body, "shutting down") {
		t.Errorf("body = %s, want it to say the server is shutting down", body)
	}
	// And an ordinary failure is still a 500: the sentinel must not swallow the
	// difference it exists to draw.
	fx.factsErr = errors.New("the instance table is on fire")
	if body := observe(t, fx, id, http.StatusInternalServerError).Body.String(); !strings.Contains(body, "on fire") {
		t.Errorf("body = %s, want the underlying failure", body)
	}
}

// TestObservationsReportADocumentTheyCannotRead: a stored document that no longer
// parses fails loudly. Answering with an empty set of observations would report a
// model with no bindings, which reads as an architecture nobody connected to
// anything rather than as a file that needs attention.
func TestObservationsReportADocumentTheyCannotRead(t *testing.T) {
	fx := newServiceFixture(t)
	broken := Model{
		ID: testModelID, ApplicationID: "app-1", Name: "Broken",
		Notation: NotationArchiMate32, Revision: 1, XML: "<model", UpdatedAt: 1,
	}
	if err := fx.store.Save(broken); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := observe(t, fx, testModelID, http.StatusInternalServerError).Body.String()
	if !strings.Contains(body, "read bindings") {
		t.Errorf("body = %s, want it to name what could not be read", body)
	}
}

// TestDriftRouteAnswersWhatChangedBetweenTwoReads is P5 end to end: two
// observation reads with a different answer between them, and a journal that says
// what moved and when. Nothing polled; the history is exactly what somebody looked
// at, which is the limit the document publishes rather than hides.
func TestDriftRouteAnswersWhatChangedBetweenTwoReads(t *testing.T) {
	fx := newServiceFixture(t)
	// A model that actually binds something: the minimal fixture binds nothing, and
	// a journal of a model with no bindings could only ever be empty.
	seedBound(t, fx, "app-1")
	id := testModelID

	drift := func(status int) DriftDocument {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+id+"/drift", nil)
		req.SetPathValue("id", id)
		var doc DriftDocument
		decodeResponse(t, invoke(t, fx.service.HandleDrift, req, status), &doc)
		return doc
	}

	// Nothing has been read yet, so there is nothing to say — and the document says
	// what it cannot see rather than presenting an empty list as a quiet landscape.
	empty := drift(http.StatusOK)
	if len(empty.Entries) != 0 || len(empty.Limits) != 3 || empty.Since != 0 {
		t.Fatalf("a journal nobody has fed = %+v", empty)
	}

	fx.facts = Facts{Applications: map[string]Fact{"proj-abc": {
		Source: SourceDeployments, State: StateHealthy, Reason: "Nothing is parked.",
	}}}
	observe(t, fx, id, http.StatusOK)
	first := drift(http.StatusOK)
	if len(first.Entries) != 0 {
		t.Fatalf("the first read journalled %+v", first.Entries)
	}
	// Somebody has now looked and nothing has changed, which is a different answer
	// from nobody having looked — `since` is the only field that tells them apart.
	if first.Since == 0 {
		t.Error("a model that has been read still speaks from nowhere")
	}

	fx.facts = Facts{Applications: map[string]Fact{"proj-abc": {
		Source: SourceInstances, State: StateDegraded, Reason: "4 token(s) are parked.",
	}}}
	var enriched ObservationDocument
	decodeResponse(t, observe(t, fx, id, http.StatusOK), &enriched)

	// The observation carries the change, so a panel can say "degraded since" in
	// one request rather than two.
	if enriched.Observations[0].PreviousState != StateHealthy ||
		enriched.Observations[0].ChangedAt == 0 {
		t.Errorf("the observation does not carry its own change: %+v", enriched.Observations[0])
	}

	journal := drift(http.StatusOK)
	if len(journal.Entries) != 1 {
		t.Fatalf("journal = %+v, want the one change", journal.Entries)
	}
	entry := journal.Entries[0]
	if entry.From != StateHealthy || entry.To != StateDegraded || entry.Value != "proj-abc" {
		t.Errorf("entry = %+v", entry)
	}
	if !strings.Contains(entry.Reason, "4 token(s)") {
		t.Errorf("reason = %q, want the new state's own sentence", entry.Reason)
	}
	if journal.Since == 0 {
		t.Error("the journal does not say from when it can speak")
	}
}

// TestDriftHonoursTheModelsAccess. A history of what changed is a history of the
// model's own bindings, so it must not outlive the permission to read them —
// otherwise a closed sharing scope leaves a readable record of what was behind it.
func TestDriftHonoursTheModelsAccess(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	id := testModelID
	fx.facts = Facts{Applications: map[string]Fact{"proj-abc": {
		Source: SourceDeployments, State: StateHealthy,
	}}}
	observe(t, fx, id, http.StatusOK)

	// The application's scope closes underneath the model.
	fx.access["app-1"] = ApplicationAccess{Exists: true}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+id+"/drift", nil)
	req.SetPathValue("id", id)
	invoke(t, fx.service.HandleDrift, req, http.StatusNotFound)
}
