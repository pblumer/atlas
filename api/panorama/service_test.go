package panorama

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/runloop"
)

const testModelID = "0123456789abcdef0123456789abcdef"

type serviceFixture struct {
	service *Service
	store   *Store
	access  map[string]ApplicationAccess
	// catalog is what binding resolution resolves against; a test sets it before
	// calling a binding handler. catalogErr makes the resolver fail instead.
	catalog    Catalog
	catalogErr error
}

// errStub is the failure a test injects when it needs the catalog to fail.
var errStub = errors.New("catalog is on fire")

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	access := map[string]ApplicationAccess{
		"app-1":  {Exists: true, CanView: true, CanEdit: true},
		"hidden": {Exists: true},
		"viewer": {Exists: true, CanView: true},
	}
	fx := &serviceFixture{store: store, access: access}
	fx.service = New(loop, store,
		func(_ *http.Request, applicationID string) (ApplicationAccess, error) {
			return access[applicationID], nil
		},
		func() (string, error) { return testModelID, nil },
		func() time.Time { return time.Unix(1_700_000_000, 0) },
		func(*http.Request) (Catalog, error) { return fx.catalog, fx.catalogErr },
	)
	return fx
}

func TestServiceModelLifecycleAndRevisionConflict(t *testing.T) {
	fx := newServiceFixture(t)
	xml := string(minimalModel(t))

	created := requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", map[string]any{
		"applicationId": "app-1",
		"name":          "Atlas Landscape",
		"notation":      NotationArchiMate32,
		"xml":           xml,
	}, http.StatusCreated)
	var summary Summary
	decodeResponse(t, created, &summary)
	if summary.ID != testModelID || summary.Revision != 1 || summary.ApplicationID != "app-1" {
		t.Fatalf("created = %#v", summary)
	}
	if bytes.Contains(created.Body.Bytes(), []byte(`"xml"`)) {
		t.Fatal("create response includes XML; metadata responses must stay lean")
	}

	listed := request(t, fx.service.HandleList, http.MethodGet, "/api/v1/panorama/models", nil, http.StatusOK)
	var list []Summary
	decodeResponse(t, listed, &list)
	if len(list) != 1 || list[0].ID != testModelID {
		t.Fatalf("list = %#v", list)
	}
	if bytes.Contains(listed.Body.Bytes(), []byte(`"xml"`)) {
		t.Fatal("list response includes XML")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+testModelID, nil)
	getReq.SetPathValue("id", testModelID)
	got := invoke(t, fx.service.HandleGet, getReq, http.StatusOK)
	var gotSummary Summary
	decodeResponse(t, got, &gotSummary)
	if gotSummary != summary {
		t.Fatalf("get = %#v, want %#v", gotSummary, summary)
	}

	xmlReq := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+testModelID+"/xml", nil)
	xmlReq.SetPathValue("id", testModelID)
	exported := invoke(t, fx.service.HandleXML, xmlReq, http.StatusOK)
	if exported.Body.String() != xml {
		t.Fatal("export changed the original Open Exchange XML")
	}
	if got := exported.Header().Get("X-Panorama-Revision"); got != "1" {
		t.Errorf("X-Panorama-Revision = %q, want 1", got)
	}

	updatedXML := strings.Replace(xml, "Minimal Panorama Model", "Updated Exchange Name", 1)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID,
		jsonBody(t, map[string]any{"expectedRevision": 1, "name": "Updated Landscape", "xml": updatedXML}))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.SetPathValue("id", testModelID)
	updated := invoke(t, fx.service.HandleUpdate, updateReq, http.StatusOK)
	decodeResponse(t, updated, &summary)
	if summary.Revision != 2 || summary.Name != "Updated Landscape" {
		t.Fatalf("updated = %#v", summary)
	}

	conflictReq := httptest.NewRequest(http.MethodPut, "/api/v1/panorama/models/"+testModelID,
		jsonBody(t, map[string]any{"expectedRevision": 1, "name": "Stale write"}))
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictReq.SetPathValue("id", testModelID)
	conflict := invoke(t, fx.service.HandleUpdate, conflictReq, http.StatusConflict)
	if !strings.Contains(conflict.Body.String(), "revision conflict") {
		t.Fatalf("conflict body = %s", conflict.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/panorama/models/"+testModelID, nil)
	deleteReq.SetPathValue("id", testModelID)
	invoke(t, fx.service.HandleDelete, deleteReq, http.StatusNoContent)

	missingReq := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/models/"+testModelID, nil)
	missingReq.SetPathValue("id", testModelID)
	invoke(t, fx.service.HandleGet, missingReq, http.StatusNotFound)
}

func TestServiceEnforcesApplicationAccessAndValidation(t *testing.T) {
	fx := newServiceFixture(t)
	xml := string(minimalModel(t))
	for i, app := range []string{"hidden", "viewer"} {
		status := http.StatusNotFound
		if app == "viewer" {
			status = http.StatusForbidden
		}
		body := map[string]any{
			"applicationId": app, "name": "Denied", "notation": NotationArchiMate32, "xml": xml,
		}
		requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", body, status)
		if _, ok, err := fx.store.Get(testModelID); err != nil || ok {
			t.Fatalf("denied create %d persisted: ok:%v err:%v", i, ok, err)
		}
	}

	invalid := requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/panorama/models", map[string]any{
		"applicationId": "app-1", "name": "Broken", "xml": `<model/>`,
	}, http.StatusBadRequest)
	if !strings.Contains(invalid.Body.String(), "validation") || !strings.Contains(invalid.Body.String(), "unsupported root element") {
		t.Fatalf("invalid response = %s", invalid.Body.String())
	}

	validation := request(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/panorama/validate",
		strings.NewReader(xml), http.StatusOK)
	var result ValidationResult
	decodeResponse(t, validation, &result)
	if !result.Valid || result.Elements != 2 {
		t.Fatalf("validation = %#v", result)
	}
}

func TestListFiltersModelsTheCallerCannotView(t *testing.T) {
	fx := newServiceFixture(t)
	for _, model := range []Model{
		{ID: strings.Repeat("a", 32), ApplicationID: "app-1", Name: "visible", UpdatedAt: 2},
		{ID: strings.Repeat("b", 32), ApplicationID: "hidden", Name: "secret", UpdatedAt: 3},
	} {
		if err := fx.store.Save(model); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	res := request(t, fx.service.HandleList, http.MethodGet, "/api/v1/panorama/models", nil, http.StatusOK)
	var list []Summary
	decodeResponse(t, res, &list)
	if len(list) != 1 || list[0].Name != "visible" {
		t.Fatalf("list = %#v", list)
	}
}

func requestJSON(t *testing.T, handler http.HandlerFunc, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, handler, method, path, jsonBody(t, body), status)
}

func jsonBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(raw)
}

func request(t *testing.T, handler http.HandlerFunc, method, path string, body io.Reader, status int) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
	}
	return invoke(t, handler, req, status)
}

func invoke(t *testing.T, handler http.HandlerFunc, req *http.Request, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != status {
		t.Fatalf("%s %s status = %d, want %d; body=%s", req.Method, req.URL.Path, w.Code, status, w.Body.String())
	}
	return w
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
