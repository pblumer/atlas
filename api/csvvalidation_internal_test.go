package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// buildCSVMultipart assembles a multipart/form-data body carrying a CSV file and
// a JSON column layout, for driving the Slice-1 ingestion endpoint from an
// internal (package api) test.
func buildCSVMultipart(t *testing.T, fileName, csv, config string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("config", config); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fw, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := io.WriteString(fw, csv); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// TestCSVUploadRunsRowValidation is the Slice-1 + Slice-2 end-to-end proof
// (ADR-0084): the shipped example decision (pruefe-datensaetze.dmn) and process
// (pruefe-datensaetze.bpmn) are deployed as-is, a CSV is uploaded through the
// ingestion endpoint, and the multi-instance business rule task validates each
// row against the DMN rules — collecting one structured verdict per row into the
// `verdicts` process variable, in input order. This is the first test in the repo
// to combine a business rule task inside a multi-instance subprocess with an
// output collection, the exact composition Slice 2 introduces.
func TestCSVUploadRunsRowValidation(t *testing.T) {
	srv, dir := newValidateServer(t)

	// Ship the example files verbatim: the test deploys exactly what an operator
	// would, so a drift in either example breaks the build.
	dmnModel, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		t.Fatalf("read example DMN: %v", err)
	}
	bpmn, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.bpmn"))
	if err != nil {
		t.Fatalf("read example BPMN: %v", err)
	}
	// Seed the model where the DirResolver reads it, then reference it by handle.
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "rowvalid.dmn"), dmnModel, 0o644); err != nil {
		t.Fatalf("seed rowvalid.dmn: %v", err)
	}

	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"RowValid","modelRef":"rowvalid"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	code, b := x.do(http.MethodPost, "/api/v1/deployments", string(bpmn))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(b, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, b)
	}

	// Upload a 2-row CSV: one clean record and one that violates every rule.
	csv := "email,group,license\nada@x.io,users,PRO\nbob,ops,NONE\n"
	config := `{"columns":[{"name":"email"},{"name":"group"},{"name":"license"}]}`
	body, ct := buildCSVMultipart(t, "records.csv", csv, config)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances-from-csv", body)
	req.Header.Set("Content-Type", ct)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Find the instance and read its variables (it ran to completion in-process).
	code, b = x.do(http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, b)
	}
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(b, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}

	code, b = x.do(http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(instances[0].Key, 10)+"/variables", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: %d %s", code, b)
	}
	var vars map[string]any
	if err := json.Unmarshal(b, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, b)
	}
	verdicts, ok := vars["verdicts"].([]any)
	if !ok || len(verdicts) != 2 {
		t.Fatalf("verdicts = %v (%T), want an array of 2 (per-row verdicts collected?)", vars["verdicts"], vars["verdicts"])
	}

	// Row order is preserved: verdict[0] is the clean record, verdict[1] the bad one.
	want := []map[string]bool{
		{"emailOk": true, "groupOk": true, "licenseOk": true, "valid": true},
		{"emailOk": false, "groupOk": false, "licenseOk": false, "valid": false},
	}
	for i, w := range want {
		v, ok := verdicts[i].(map[string]any)
		if !ok {
			t.Fatalf("verdicts[%d] = %v (%T), want an object", i, verdicts[i], verdicts[i])
		}
		for field, exp := range w {
			got, isBool := v[field].(bool)
			if !isBool || got != exp {
				t.Errorf("verdicts[%d].%s = %v, want %v", i, field, v[field], exp)
			}
		}
	}
}
