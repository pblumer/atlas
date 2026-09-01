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
	if len(doc.Unavailable) == 0 {
		t.Error("the document does not say what it cannot observe")
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

// TestObservationsRefuseAClosingServer: runloop.Do declines to run anything once
// the loop is closing, which would leave every fact absent and the whole model
// reported as unobserved. A document that says "nothing is running here" because
// the server was shutting down is the most damaging thing this route could say.
func TestObservationsRefuseAClosingServer(t *testing.T) {
	fx := newServiceFixture(t)
	id := createObservedModel(t, fx)
	// Same service, same store, but a loop that will not run the collector.
	closing := New(stoppedLoop(), fx.store, fx.service.access, fx.service.newID, fx.service.now,
		fx.service.catalog, func(*http.Request) (Facts, error) {
			t.Error("the collector ran on a closed loop; this test no longer covers what it claims")
			return Facts{}, nil
		})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+id+"/observations", nil)
	req.SetPathValue("id", id)
	body := invoke(t, closing.HandleObservations, req, http.StatusServiceUnavailable).Body.String()
	if !strings.Contains(body, "shutting down") {
		t.Errorf("body = %s, want it to say the server is shutting down", body)
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
