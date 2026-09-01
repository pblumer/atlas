package panorama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// contextRequest drives the route for one element and window.
func contextRequest(t *testing.T, fx *serviceFixture, query string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/panorama/models/"+testModelID+"/context?"+query, nil)
	req.SetPathValue("id", testModelID)
	return invoke(t, fx.service.HandleContext, req, status)
}

// TestContextRouteAsksAboutEveryBoundValue is the route end to end: the element's
// bindings become queries, and what the stores answered comes back assembled.
func TestContextRouteAsksAboutEveryBoundValue(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	fx.contextResults = []ContextResult{
		{
			Source: ContextSourceEvents, Key: KeyApplicationID, Value: "proj-abc",
			State: ContextAvailable,
			Measures: []Measure{{
				Name: "instancesStarted", Label: "Instances started", Total: 12,
				Buckets: []Bucket{{At: 1_699_999_000, Value: 5}, {At: 1_699_999_600, Value: 7}},
			}},
		},
		{
			Source: ContextSourceMetrics, Key: KeyApplicationID, Value: "proj-abc",
			State: ContextUnidentifiable, Reason: "no per-element labels",
		},
	}

	var doc ContextDocument
	decodeResponse(t, contextRequest(t, fx, "element=app-c&window=1h", http.StatusOK), &doc)

	if doc.ElementID != "app-c" || doc.Window.Window != Window1h {
		t.Fatalf("document = %+v", doc)
	}
	if doc.Window.To-doc.Window.From != 3600 {
		t.Errorf("window spans %ds", doc.Window.To-doc.Window.From)
	}
	if len(doc.Results) != 2 {
		t.Fatalf("results = %+v, want one per source", doc.Results)
	}
	// The measure survives the round trip with its buckets, because a total with no
	// shape is the one thing this whole route was not worth building for.
	events := doc.Results[0]
	if events.Source != ContextSourceEvents || len(events.Measures) != 1 ||
		len(events.Measures[0].Buckets) != 2 || events.Measures[0].Total != 12 {
		t.Errorf("events result = %+v", events)
	}
	// A source that could not help is a row, not an omission: a missing row is
	// indistinguishable from a store nobody thought to ask.
	if doc.Results[1].State != ContextUnidentifiable {
		t.Errorf("metrics result = %+v", doc.Results[1])
	}

	// The resolver was asked about the element's bound value and nothing else.
	if len(fx.contextQueries) != 1 || fx.contextQueries[0].Value != "proj-abc" ||
		fx.contextQueries[0].Key != KeyApplicationID {
		t.Errorf("queries = %+v", fx.contextQueries)
	}
}

// TestContextRefusesAWindowOutsideTheAllowlist. A window is the bound on somebody
// else's cluster doing work for a page of ours, so an unlisted one is refused with
// the list rather than clamped to something the caller did not ask for.
func TestContextRefusesAWindowOutsideTheAllowlist(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	body := contextRequest(t, fx, "element=app-c&window=90d", http.StatusBadRequest).Body.String()
	for _, want := range Windows() {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not offer %q: %s", want, body)
		}
	}

	// An unnamed window is not a wrong question, only an unspecific one.
	var doc ContextDocument
	decodeResponse(t, contextRequest(t, fx, "element=app-c", http.StatusOK), &doc)
	if doc.Window.Window != DefaultWindow {
		t.Errorf("an unnamed window resolved to %q", doc.Window.Window)
	}
}

// TestContextRequiresAnElement. A model-wide answer would multiply one panel's
// question by the whole landscape's bindings against somebody else's cluster, so
// naming the element is what bounds the cost.
func TestContextRequiresAnElement(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	body := contextRequest(t, fx, "", http.StatusBadRequest).Body.String()
	if !strings.Contains(body, "element") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
	if len(fx.contextQueries) != 0 {
		t.Errorf("a refused request still asked the stores: %+v", fx.contextQueries)
	}
}

// TestContextOfAnUnboundElementIsAnAnswer. An element that binds nothing has no
// context to fetch, and that is a complete answer rather than an error — but the
// document still carries its limits, because "this element binds nothing" and "no
// store could answer" are different findings.
func TestContextOfAnUnboundElementIsAnAnswer(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	var doc ContextDocument
	decodeResponse(t, contextRequest(t, fx, "element=bp-1", http.StatusOK), &doc)
	if len(doc.Results) != 0 {
		t.Fatalf("an unbound element produced %+v", doc.Results)
	}
	if len(doc.Limits) != 3 {
		t.Errorf("limits = %+v, want them on every answer", doc.Limits)
	}
	if len(fx.contextQueries) != 0 {
		t.Errorf("an unbound element still asked the stores: %+v", fx.contextQueries)
	}
}

// TestContextHonoursTheModelsAccess. Context is a reading of the model's own
// bindings, so it must not outlive the permission to read them.
func TestContextHonoursTheModelsAccess(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "hidden")
	contextRequest(t, fx, "element=app-c", http.StatusNotFound)
}

// TestContextRefusesOnAClosingServer and reports a resolver failure as one. A
// document built from stores nobody could ask would report every source as absent —
// a landscape with no history rather than a question that was not put.
func TestContextRefusesOnAClosingServer(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")

	fx.contextErr = ErrShuttingDown
	contextRequest(t, fx, "element=app-c", http.StatusServiceUnavailable)

	fx.contextErr = errStub
	body := contextRequest(t, fx, "element=app-c", http.StatusInternalServerError).Body.String()
	if !strings.Contains(body, "read context") {
		t.Errorf("body = %s, want it to name what failed", body)
	}
}

// TestContextRefusedWhenNothingReadsHistory. A build with no adapter compiled in
// cannot know whether a store is wired, so it says it reads no history rather than
// answering with a document of not-configured rows it has no basis for.
func TestContextRefusedWhenNothingReadsHistory(t *testing.T) {
	fx := newServiceFixture(t)
	seedBound(t, fx, "app-1")
	fx.service.context = nil

	body := contextRequest(t, fx, "element=app-c", http.StatusNotImplemented).Body.String()
	if !strings.Contains(body, "historical context") {
		t.Errorf("body = %s", body)
	}
}

// TestContextReportsADocumentItCannotRead. A model whose bindings cannot be parsed
// is a failure to say out loud: answering 200 with no results would report an
// element that binds nothing, which is a claim about the architecture rather than
// about the document.
func TestContextReportsADocumentItCannotRead(t *testing.T) {
	fx := newServiceFixture(t)
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: "app-1", Name: "Broken",
		Notation: NotationArchiMate32, Revision: 1, XML: "<model", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := contextRequest(t, fx, "element=app-c", http.StatusInternalServerError).Body.String()
	if !strings.Contains(body, "read bindings") {
		t.Errorf("body = %s, want it to name what could not be read", body)
	}
}
