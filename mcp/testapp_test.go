package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// testApps is the application each test server's artifacts are filed under, made on
// first use: every process, decision and form belongs to an application
// (ADR-0544), and the tests in this package are
// about the adapter, not about filing.
var testApps sync.Map // *httptest.Server -> application id

// testApp returns the id of the application a test files its artifacts under on ts,
// creating it the first time it is asked.
func testApp(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	if id, ok := testApps.Load(ts); ok {
		return id.(string)
	}
	res, err := http.Post(ts.URL+"/api/v1/applications", "application/json", bytes.NewReader([]byte(`{"name":"Test"}`)))
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	defer res.Body.Close()
	var app struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&app); err != nil || app.ID == "" {
		t.Fatalf("create application: status %d, %v", res.StatusCode, err)
	}
	t.Cleanup(func() { testApps.Delete(ts) })
	actual, _ := testApps.LoadOrStore(ts, app.ID)
	return actual.(string)
}
