package panorama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// viewModelXML is a stored model with one view and two shapes on it.
const viewModelXML = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Arrangeable</name>
  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent"><name xml:lang="en">Orders</name></element>
    <element identifier="app-2" xsi:type="ApplicationComponent"><name xml:lang="en">Billing</name></element>
  </elements>
  <views><diagrams>
    <view identifier="v1" xsi:type="Diagram"><name>Cooperation</name>
      <node identifier="n-1" elementRef="app-1" x="10" y="20" w="160" h="70"/>
      <node identifier="n-2" elementRef="app-2" x="300" y="20" w="160" h="70"/>
    </view>
  </diagrams></views>
</model>`

// seedView stores viewModelXML under the fixture's model id.
func seedView(t *testing.T, fx *serviceFixture, applicationID string) {
	t.Helper()
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: applicationID, Name: "Arrangeable",
		Notation: NotationArchiMate32, Revision: 1, XML: viewModelXML, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func layoutRequest(t *testing.T, fx *serviceFixture, body map[string]any, status int) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/panorama/models/" + testModelID + "/layout"
	req := httptest.NewRequest(http.MethodPut, path, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", testModelID)
	return invoke(t, fx.service.HandleSetLayout, req, status)
}

// TestSetLayoutRouteMovesShapesAndBumpsTheRevision is the route end to end: shapes
// move, the document keeps everything else, and the revision advances so a second
// editor's stale save is caught.
func TestSetLayoutRouteMovesShapesAndBumpsTheRevision(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "app-1")

	var summary Summary
	decodeResponse(t, layoutRequest(t, fx, map[string]any{
		"expectedRevision": 1,
		"changes": []map[string]any{
			{"nodeId": "n-1", "x": 42, "y": 84, "w": 200, "h": 90},
		},
	}, http.StatusOK), &summary)
	if summary.Revision != 2 {
		t.Fatalf("revision = %d, want it advanced", summary.Revision)
	}

	stored, _, err := fx.store.Get(testModelID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(stored.XML, `<node identifier="n-1" elementRef="app-1" x="42" y="84" w="200" h="90"/>`) {
		t.Errorf("the shape did not move: %s", stored.XML)
	}
	// The shape nobody touched is untouched, and so is the rest of the document.
	if !strings.Contains(stored.XML, `<node identifier="n-2" elementRef="app-2" x="300" y="20" w="160" h="70"/>`) {
		t.Error("moving one shape moved another")
	}
	if !strings.Contains(stored.XML, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("the declaration was lost, so this route re-serialised rather than spliced")
	}
}

// TestSetLayoutRouteRefusesAStaleRevision. Two people arranging one view is exactly
// where a lost update is invisible: the loser's boxes drift back with nothing to
// say why.
func TestSetLayoutRouteRefusesAStaleRevision(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "app-1")

	change := []map[string]any{{"nodeId": "n-1", "x": 1, "y": 1, "w": 10, "h": 10}}
	layoutRequest(t, fx, map[string]any{"expectedRevision": 1, "changes": change}, http.StatusOK)

	body := layoutRequest(t, fx,
		map[string]any{"expectedRevision": 1, "changes": change}, http.StatusConflict).Body.String()
	if !strings.Contains(body, "revision conflict") {
		t.Errorf("body = %s, want it to name the conflict", body)
	}
}

// TestSetLayoutRouteRefusesWhatItCannotDo. Each of these is the caller's mistake and
// comes back naming what is wrong, rather than as an opaque failure from inside the
// writer.
func TestSetLayoutRouteRefusesWhatItCannotDo(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "app-1")

	for name, body := range map[string]map[string]any{
		"no revision": {"changes": []map[string]any{{"nodeId": "n-1", "w": 1, "h": 1}}},
		// A save that changes nothing would still bump the revision, and make every
		// other open editor conflict for no reason.
		"nothing moved": {"expectedRevision": 1, "changes": []map[string]any{}},
		"unknown shape": {"expectedRevision": 1, "changes": []map[string]any{
			{"nodeId": "n-ghost", "x": 1, "y": 1, "w": 10, "h": 10}}},
		"no size": {"expectedRevision": 1, "changes": []map[string]any{
			{"nodeId": "n-1", "x": 1, "y": 1, "w": 0, "h": 10}}},
	} {
		t.Run(name, func(t *testing.T) {
			layoutRequest(t, fx, body, http.StatusBadRequest)
		})
	}

	// None of them stored anything.
	stored, _, err := fx.store.Get(testModelID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Revision != 1 {
		t.Errorf("revision = %d after four refusals, want it untouched", stored.Revision)
	}
}

// TestSetLayoutRouteNeedsWriteAccess. Arranging a view is authoring the model, so it
// takes the same rights as any other write to it — a viewer may look and not move.
func TestSetLayoutRouteNeedsWriteAccess(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "viewer")
	layoutRequest(t, fx, map[string]any{
		"expectedRevision": 1,
		"changes":          []map[string]any{{"nodeId": "n-1", "x": 1, "y": 1, "w": 10, "h": 10}},
	}, http.StatusForbidden)

	// And a model this caller cannot see at all is absent rather than forbidden.
	fx2 := newServiceFixture(t)
	seedView(t, fx2, "hidden")
	layoutRequest(t, fx2, map[string]any{
		"expectedRevision": 1,
		"changes":          []map[string]any{{"nodeId": "n-1", "x": 1, "y": 1, "w": 10, "h": 10}},
	}, http.StatusNotFound)
}
