package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewriteRequestBodyEncodesJSONAndRejectsUnsupportedValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := rewriteRequestBody(request, map[string]string{"workerTypeId": "atlas.mail"}); err != nil {
		t.Fatalf("rewrite JSON request: %v", err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read rewritten request: %v", err)
	}
	if string(body) != `{"workerTypeId":"atlas.mail"}` || request.ContentLength != int64(len(body)) {
		t.Fatalf("rewritten request body=%q contentLength=%d", body, request.ContentLength)
	}

	if err := rewriteRequestBody(request, make(chan int)); err == nil {
		t.Fatal("rewrite unsupported JSON value: want an error")
	}
}

func TestConfiguredWorkerProjectionMakesUnknownLegacyKindExplicit(t *testing.T) {
	worker, err := configuredWorkerFromConnectorJSON([]byte(`{
		"id":"legacy-1",
		"name":"legacy",
		"kind":"vendor-old",
		"endpoint":"https://legacy.example.test",
		"credentialsRef":"legacy-token",
		"enabled":true
	}`))
	if err != nil {
		t.Fatalf("project legacy connector: %v", err)
	}
	if worker.WorkerTypeID != "" || worker.WorkerTypeVersion != "" {
		t.Fatalf("unknown connector kind received an invented Worker Type identity: %+v", worker)
	}
	if !strings.Contains(worker.CompatibilityError, "vendor-old") {
		t.Fatalf("unknown connector kind has no explicit compatibility error: %+v", worker)
	}
	if worker.Config == nil || worker.Config.Endpoint != "https://legacy.example.test" || worker.CredentialsRef != "legacy-token" {
		t.Fatalf("legacy connector configuration was not preserved in the projection: %+v", worker)
	}
}

func TestConfiguredWorkerProjectionPreservesCatalogAccessBoundary(t *testing.T) {
	worker, err := configuredWorkerFromConnectorJSON([]byte(`{
		"id":"catalog-1",
		"name":"mail-prod",
		"kind":"mail",
		"enabled":true
	}`))
	if err != nil {
		t.Fatalf("project catalog entry: %v", err)
	}
	if worker.WorkerTypeID != "atlas.mail" || worker.WorkerTypeVersion != initialBuiltInWorkerTypeVersion {
		t.Fatalf("catalog entry has wrong Worker Type identity: %+v", worker)
	}
	if worker.Config != nil || worker.CredentialsRef != "" {
		t.Fatalf("catalog-only projection exposed configuration: %+v", worker)
	}
}

func TestBufferedResponsePreservesStatusHeadersAndBody(t *testing.T) {
	implicitOK := newBufferedResponse()
	if _, err := implicitOK.Write([]byte("ok")); err != nil {
		t.Fatalf("capture implicit OK response: %v", err)
	}
	if implicitOK.status != http.StatusOK || implicitOK.body.String() != "ok" {
		t.Fatalf("implicit response status=%d body=%q", implicitOK.status, implicitOK.body.String())
	}

	captured := newBufferedResponse()
	captured.Header().Set("X-Test", "preserved")
	captured.WriteHeader(http.StatusTeapot)
	if _, err := captured.Write([]byte("legacy error")); err != nil {
		t.Fatalf("capture response body: %v", err)
	}

	recorder := httptest.NewRecorder()
	flushBufferedResponse(recorder, captured)
	if recorder.Code != http.StatusTeapot || recorder.Header().Get("X-Test") != "preserved" || recorder.Body.String() != "legacy error" {
		t.Fatalf("flushed response changed: status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	empty := newBufferedResponse()
	recorder = httptest.NewRecorder()
	flushBufferedResponse(recorder, empty)
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty captured response status=%d, want 200", recorder.Code)
	}
}

func TestConfiguredWorkerResponseRejectsInvalidLegacyJSON(t *testing.T) {
	captured := newBufferedResponse()
	captured.status = http.StatusOK
	_, _ = captured.body.WriteString("{")

	recorder := httptest.NewRecorder()
	writeConfiguredWorkerResponse(recorder, captured)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "project configured Worker response") {
		t.Fatalf("invalid legacy response status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
