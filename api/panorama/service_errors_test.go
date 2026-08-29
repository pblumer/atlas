package panorama

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func validCreatePayload(applicationID string) map[string]any {
	return map[string]any{
		"applicationId": applicationID,
		"name":          "Landscape",
		"notation":      NotationArchiMate32,
		"xml":           string(mustFixture()),
	}
}

func mustFixture() []byte {
	b, err := os.ReadFile("testdata/minimal.archimate.xml")
	if err != nil {
		panic(err)
	}
	return b
}

func TestApplicationCountsAndStoreFailures(t *testing.T) {
	fx := newServiceFixture(t)
	for _, model := range []Model{
		{ID: strings.Repeat("a", 32), ApplicationID: "app-1", UpdatedAt: 1},
		{ID: strings.Repeat("b", 32), ApplicationID: "app-1", UpdatedAt: 2},
		{ID: strings.Repeat("c", 32), ApplicationID: "viewer", UpdatedAt: 3},
	} {
		if err := fx.store.Save(model); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	var count int
	var counts map[string]int
	var err error
	fx.service.loop.Do(func() {
		count, err = fx.service.CountForApplicationOnLoop("app-1")
	})
	if err != nil || count != 2 {
		t.Fatalf("CountForApplicationOnLoop = %d, %v; want 2, nil", count, err)
	}
	fx.service.loop.Do(func() {
		counts, err = fx.service.CountsByApplicationOnLoop()
	})
	if err != nil || counts["app-1"] != 2 || counts["viewer"] != 1 {
		t.Fatalf("CountsByApplicationOnLoop = %v, %v", counts, err)
	}

	if err := os.RemoveAll(fx.store.Dir()); err != nil {
		t.Fatalf("remove store: %v", err)
	}
	fx.service.loop.Do(func() {
		_, err = fx.service.CountForApplicationOnLoop("app-1")
	})
	if err == nil {
		t.Fatal("CountForApplicationOnLoop on missing store: want error")
	}
	fx.service.loop.Do(func() {
		_, err = fx.service.CountsByApplicationOnLoop()
	})
	if err == nil {
		t.Fatal("CountsByApplicationOnLoop on missing store: want error")
	}
}

func TestCreateRejectsMalformedRequestsAndAuthorizationFailures(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		fx := newServiceFixture(t)
		request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", strings.NewReader("{"), http.StatusBadRequest)
	})
	t.Run("unreadable body", func(t *testing.T) {
		fx := newServiceFixture(t)
		request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", failingReader{}, http.StatusBadRequest)
	})
	t.Run("oversized body", func(t *testing.T) {
		fx := newServiceFixture(t)
		request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			strings.NewReader(strings.Repeat("x", maxJSONBytes+1)), http.StatusRequestEntityTooLarge)
	})
	t.Run("missing application", func(t *testing.T) {
		fx := newServiceFixture(t)
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			map[string]any{"xml": string(minimalModel(t))}, http.StatusBadRequest)
	})
	t.Run("unsupported notation", func(t *testing.T) {
		fx := newServiceFixture(t)
		body := validCreatePayload("app-1")
		body["notation"] = "uaf"
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", body, http.StatusBadRequest)
	})
	t.Run("unknown application", func(t *testing.T) {
		fx := newServiceFixture(t)
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			validCreatePayload("missing"), http.StatusBadRequest)
	})
	t.Run("protected application", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.access["protected"] = ApplicationAccess{Exists: true, CanView: true, CanEdit: true, Protected: true}
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			validCreatePayload("protected"), http.StatusForbidden)
	})
	t.Run("id generator failure", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.service.newID = func() (string, error) { return "", errors.New("entropy unavailable") }
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			validCreatePayload("app-1"), http.StatusInternalServerError)
	})
	t.Run("id collision", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := fx.store.Save(Model{ID: testModelID, ApplicationID: "app-1"}); err != nil {
			t.Fatalf("Save collision: %v", err)
		}
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			validCreatePayload("app-1"), http.StatusInternalServerError)
	})
	t.Run("access resolver failure", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.service.access = func(*http.Request, string) (ApplicationAccess, error) {
			return ApplicationAccess{}, errors.New("application store failed")
		}
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models",
			validCreatePayload("app-1"), http.StatusInternalServerError)
	})
}

func TestCreateDefaultsNameAndRecordsAuthenticatedActor(t *testing.T) {
	fx := newServiceFixture(t)
	body := validCreatePayload("app-1")
	body["name"] = " "
	req := httptest.NewRequest(http.MethodPost, "/api/v1/panorama/models", jsonBody(t, body))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{
		UserID: "user-1", Username: "pat",
	}))
	res := invoke(t, fx.service.HandleCreate, req, http.StatusCreated)
	var summary Summary
	decodeResponse(t, res, &summary)
	if summary.Name != "Minimal Panorama Model" || summary.CreatedBy != "pat" || summary.UpdatedBy != "pat" {
		t.Fatalf("created summary = %#v", summary)
	}
}

func TestListAndReadReportStoreAndAccessFailures(t *testing.T) {
	t.Run("application filter", func(t *testing.T) {
		fx := newServiceFixture(t)
		for _, model := range []Model{
			{ID: strings.Repeat("a", 32), ApplicationID: "app-1", Name: "wanted"},
			{ID: strings.Repeat("b", 32), ApplicationID: "viewer", Name: "other"},
		} {
			if err := fx.store.Save(model); err != nil {
				t.Fatalf("Save: %v", err)
			}
		}
		res := request(t, fx.service.HandleList, http.MethodGet,
			"/api/v1/panorama/models?applicationId=app-1", nil, http.StatusOK)
		var list []Summary
		decodeResponse(t, res, &list)
		if len(list) != 1 || list[0].Name != "wanted" {
			t.Fatalf("filtered list = %#v", list)
		}
	})
	t.Run("list access resolver", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := fx.store.Save(Model{ID: testModelID, ApplicationID: "app-1"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		fx.service.access = func(*http.Request, string) (ApplicationAccess, error) {
			return ApplicationAccess{}, errors.New("access failed")
		}
		request(t, fx.service.HandleList, http.MethodGet, "/api/v1/panorama/models", nil, http.StatusInternalServerError)
	})
	t.Run("missing store", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := os.RemoveAll(fx.store.Dir()); err != nil {
			t.Fatalf("remove store: %v", err)
		}
		request(t, fx.service.HandleList, http.MethodGet, "/api/v1/panorama/models", nil, http.StatusInternalServerError)
	})
	t.Run("corrupt model", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := os.WriteFile(filepath.Join(fx.store.Dir(), testModelID+".json"), []byte("{"), 0o644); err != nil {
			t.Fatalf("write corrupt model: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+testModelID, nil)
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleGet, req, http.StatusInternalServerError)
	})
	t.Run("hidden model", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := fx.store.Save(Model{ID: testModelID, ApplicationID: "hidden"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+testModelID+"/xml", nil)
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleXML, req, http.StatusNotFound)
	})
}

func TestUpdateValidatesInputAndAccess(t *testing.T) {
	tests := []struct {
		name        string
		application string
		body        any
		status      int
	}{
		{"missing revision", "app-1", map[string]any{"name": "x"}, http.StatusBadRequest},
		{"no update", "app-1", map[string]any{"expectedRevision": 1}, http.StatusBadRequest},
		{"empty name", "app-1", map[string]any{"expectedRevision": 1, "name": " "}, http.StatusBadRequest},
		{"invalid XML", "app-1", map[string]any{"expectedRevision": 1, "xml": "<model/>"}, http.StatusBadRequest},
		{"hidden", "hidden", map[string]any{"expectedRevision": 1, "name": "x"}, http.StatusNotFound},
		{"viewer", "viewer", map[string]any{"expectedRevision": 1, "name": "x"}, http.StatusForbidden},
		{"missing application", "missing", map[string]any{"expectedRevision": 1, "name": "x"}, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newServiceFixture(t)
			if err := fx.store.Save(Model{ID: testModelID, ApplicationID: tc.application, Revision: 1}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID, jsonBody(t, tc.body))
			req.SetPathValue("id", testModelID)
			invoke(t, fx.service.HandleUpdate, req, tc.status)
		})
	}

	t.Run("malformed JSON", func(t *testing.T) {
		fx := newServiceFixture(t)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID, strings.NewReader("{"))
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleUpdate, req, http.StatusBadRequest)
	})
	t.Run("missing model", func(t *testing.T) {
		fx := newServiceFixture(t)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID,
			jsonBody(t, map[string]any{"expectedRevision": 1, "name": "x"}))
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleUpdate, req, http.StatusNotFound)
	})
	t.Run("access resolver failure", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := fx.store.Save(Model{ID: testModelID, ApplicationID: "app-1", Revision: 1}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		fx.service.access = func(*http.Request, string) (ApplicationAccess, error) {
			return ApplicationAccess{}, errors.New("access failed")
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID,
			jsonBody(t, map[string]any{"expectedRevision": 1, "name": "x"}))
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleUpdate, req, http.StatusInternalServerError)
	})
}

func TestDeleteReportsMissingAccessAndStoreFailures(t *testing.T) {
	tests := []struct {
		name        string
		application string
		status      int
	}{
		{"missing model", "", http.StatusNotFound},
		{"hidden", "hidden", http.StatusNotFound},
		{"viewer", "viewer", http.StatusForbidden},
		{"missing application", "missing", http.StatusNotFound},
		{"protected", "protected", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newServiceFixture(t)
			fx.access["protected"] = ApplicationAccess{Exists: true, CanView: true, CanEdit: true, Protected: true}
			if tc.application != "" {
				if err := fx.store.Save(Model{ID: testModelID, ApplicationID: tc.application}); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/panorama/models/"+testModelID, nil)
			req.SetPathValue("id", testModelID)
			invoke(t, fx.service.HandleDelete, req, tc.status)
		})
	}

	t.Run("access resolver failure", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := fx.store.Save(Model{ID: testModelID, ApplicationID: "app-1"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		fx.service.access = func(*http.Request, string) (ApplicationAccess, error) {
			return ApplicationAccess{}, errors.New("access failed")
		}
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/panorama/models/"+testModelID, nil)
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleDelete, req, http.StatusInternalServerError)
	})
	t.Run("corrupt model", func(t *testing.T) {
		fx := newServiceFixture(t)
		if err := os.WriteFile(filepath.Join(fx.store.Dir(), testModelID+".json"), []byte("{"), 0o644); err != nil {
			t.Fatalf("write corrupt model: %v", err)
		}
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/panorama/models/"+testModelID, nil)
		req.SetPathValue("id", testModelID)
		invoke(t, fx.service.HandleDelete, req, http.StatusInternalServerError)
	})
}

func TestValidateBoundsAndReadErrors(t *testing.T) {
	fx := newServiceFixture(t)
	request(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/panorama/validate",
		failingReader{}, http.StatusBadRequest)
	request(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/panorama/validate",
		strings.NewReader(strings.Repeat("x", MaxXMLBytes+1)), http.StatusRequestEntityTooLarge)
}
