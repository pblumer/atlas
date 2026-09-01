package panorama

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contentRequest(t *testing.T, fx *serviceFixture, path string,
	handler http.HandlerFunc, body map[string]any, status int) *httptest.ResponseRecorder {
	t.Helper()
	full := "/api/v1/panorama/models/" + testModelID + "/" + path
	req := httptest.NewRequest(http.MethodPost, full, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", testModelID)
	return invoke(t, handler, req, status)
}

// TestAddElementRouteCreatesAndPlacesIt, and hands back the shape's identifier —
// which is what the canvas needs to select what somebody just made, rather than
// guessing at it or re-reading the whole document to find out.
func TestAddElementRouteCreatesAndPlacesIt(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "app-1")

	var made created
	decodeResponse(t, contentRequest(t, fx, "elements", fx.service.HandleAddElement, map[string]any{
		"expectedRevision": 1, "type": "ApplicationService", "name": "Billing API",
		"viewId": "v1", "x": 40, "y": 300, "w": 160, "h": 70,
	}, http.StatusOK), &made)

	if made.Revision != 2 || made.CreatedID == "" {
		t.Fatalf("created = %+v", made)
	}
	stored, _, err := fx.store.Get(testModelID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(stored.XML, `xsi:type="ApplicationService"`) ||
		!strings.Contains(stored.XML, "Billing API") {
		t.Errorf("the element was not stored:\n%s", stored.XML)
	}
	if !strings.Contains(stored.XML, `identifier="`+made.CreatedID+`"`) {
		t.Errorf("the shape id %q is not in the document", made.CreatedID)
	}
	// Nothing else moved: the two shapes that were there keep their geometry.
	if !strings.Contains(stored.XML, `<node identifier="n-1" elementRef="app-1" x="10" y="20" w="160" h="70"/>`) {
		t.Error("adding an element disturbed an existing shape")
	}
}

// TestAddRelationshipRouteEnforcesTheMatrix. A caller that went around the canvas
// gets the same answer the canvas would have given, and learns the rule rather than
// only that it was refused.
func TestAddRelationshipRouteEnforcesTheMatrix(t *testing.T) {
	fx := newServiceFixture(t)
	seedView(t, fx, "app-1")

	// Two application components: association is permitted between anything.
	var made created
	decodeResponse(t, contentRequest(t, fx, "relationships", fx.service.HandleAddRelationship,
		map[string]any{
			"expectedRevision": 1, "type": "Association",
			"source": "app-1", "target": "app-2", "viewId": "v1",
		}, http.StatusOK), &made)
	if made.Revision != 2 || made.CreatedID == "" {
		t.Fatalf("created = %+v", made)
	}

	// And one the notation forbids, refused with the matrix's own explanation.
	body := contentRequest(t, fx, "relationships", fx.service.HandleAddRelationship,
		map[string]any{
			"expectedRevision": 2, "type": "Access",
			"source": "app-1", "target": "app-2", "viewId": "v1",
		}, http.StatusBadRequest).Body.String()
	if !strings.Contains(body, "access runs from behaviour") {
		t.Errorf("body = %s, want the matrix's own reason", body)
	}
}

// TestCreatingTakesTheSameChecksAsEveryOtherWrite. Authoring content is authoring
// the model: the same rights, the same revision check, the same 404 for a model
// this caller cannot see.
func TestCreatingTakesTheSameChecksAsEveryOtherWrite(t *testing.T) {
	element := map[string]any{
		"expectedRevision": 1, "type": "Node", "name": "Host",
		"viewId": "v1", "x": 0, "y": 0, "w": 100, "h": 50,
	}

	t.Run("a viewer may not author", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "viewer")
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusForbidden)
	})

	t.Run("a hidden model is absent", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "hidden")
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusNotFound)
	})

	t.Run("no such model", func(t *testing.T) {
		fx := newServiceFixture(t)
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusNotFound)
	})

	t.Run("a stale revision conflicts", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "app-1")
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusOK)
		body := contentRequest(t, fx, "elements", fx.service.HandleAddElement,
			element, http.StatusConflict).Body.String()
		if !strings.Contains(body, "revision conflict") {
			t.Errorf("body = %s", body)
		}
	})

	t.Run("no revision at all", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "app-1")
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, map[string]any{
			"type": "Node", "name": "Host", "viewId": "v1", "w": 10, "h": 10,
		}, http.StatusBadRequest)
	})

	t.Run("malformed body", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "app-1")
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/panorama/models/"+testModelID+"/elements", strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleAddElement, req, http.StatusBadRequest)

		req = httptest.NewRequest(http.MethodPost,
			"/api/v1/panorama/models/"+testModelID+"/relationships", strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleAddRelationship, req, http.StatusBadRequest)
	})

	t.Run("the access resolver fails", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "app-1")
		fx.service.access = func(*http.Request, string) (ApplicationAccess, error) {
			return ApplicationAccess{}, errors.New("access failed")
		}
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusInternalServerError)
	})

	t.Run("the store cannot be read", func(t *testing.T) {
		fx := newServiceFixture(t)
		seedView(t, fx, "app-1")
		if err := os.WriteFile(filepath.Join(fx.store.Dir(), testModelID+".json"),
			[]byte("{"), 0o644); err != nil {
			t.Fatalf("write corrupt model: %v", err)
		}
		contentRequest(t, fx, "elements", fx.service.HandleAddElement, element, http.StatusInternalServerError)
	})
}

// TestCreatingRefusesToStoreAnInvalidDocument. The writer splices, so this should
// be unreachable — which is why it is checked rather than assumed. An insert can
// produce a dangling reference in a way a changed number cannot, so it matters more
// here than on a move.
func TestCreatingRefusesToStoreAnInvalidDocument(t *testing.T) {
	fx := newServiceFixture(t)
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: "app-1", Name: "Broken", Notation: NotationArchiMate32,
		Revision: 1, UpdatedAt: 1,
		XML: `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="ApplicationComponent"><name>A</name></element>
  </elements>
  <views><diagrams><view identifier="v1" xsi:type="Diagram"><name>V</name>
    <node identifier="na" elementRef="missing" x="0" y="0" w="10" h="10"/>
  </view></diagrams></views>
</model>`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := contentRequest(t, fx, "elements", fx.service.HandleAddElement, map[string]any{
		"expectedRevision": 1, "type": "Node", "name": "Host",
		"viewId": "v1", "x": 0, "y": 0, "w": 100, "h": 50,
	}, http.StatusBadRequest).Body.String()
	if !strings.Contains(body, "would not validate") {
		t.Errorf("body = %s, want it to say the result was refused", body)
	}
}
