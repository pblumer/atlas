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

// TestCSVUploadCorrectionLoop is the Slice-1 + Slice-2 + Slice-3 end-to-end proof
// (ADR-0084): the shipped example decision, process, and correction form are
// deployed as-is; a CSV with one clean and one invalid record is uploaded through
// the ingestion endpoint; the invalid row parks on a correction user task while
// the clean one passes straight through; and completing the task with corrected
// data re-validates that row and drives the whole instance to completion with all
// verdicts valid. It exercises the per-row correction loop that ADR-0085's
// scope-aware gateways unlocked, and loads the example files verbatim so a drift
// breaks the build.
func TestCSVUploadCorrectionLoop(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	dmnModel, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		t.Fatalf("read example DMN: %v", err)
	}
	bpmn, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.bpmn"))
	if err != nil {
		t.Fatalf("read example BPMN: %v", err)
	}
	formSchema, err := os.ReadFile(filepath.Join("..", "examples", "row-correction-form.json"))
	if err != nil {
		t.Fatalf("read example form: %v", err)
	}

	// Seed the DMN model for the resolver and register it as a decision reference.
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "rowvalid.dmn"), dmnModel, 0o644); err != nil {
		t.Fatalf("seed rowvalid.dmn: %v", err)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"RowValid","modelRef":"rowvalid"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	// Create the correction form the user task binds to (formId row-correction-form).
	formBody, err := json.Marshal(map[string]any{
		"id":     "row-correction-form",
		"name":   "Datensatz korrigieren",
		"schema": json.RawMessage(formSchema),
	})
	if err != nil {
		t.Fatalf("marshal form: %v", err)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/forms", string(formBody)); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, b)
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

	// Exactly one row (the invalid bob) parked on a correction user task; the clean
	// ada passed straight to the row end — proof the in-subprocess gateway routed on
	// the per-iteration verdict (ADR-0085).
	tasks := listTasks(t, x)
	if len(tasks) != 1 {
		t.Fatalf("active user tasks = %d, want 1 (only the invalid row parks)", len(tasks))
	}
	if tasks[0].Name != "Datensatz korrigieren" {
		t.Fatalf("task name = %q, want \"Datensatz korrigieren\"", tasks[0].Name)
	}

	// The correction form pre-fills from the task's own element-instance scope (Slice
	// 4): its input mappings expose the invalid row's original values flat there, so
	// the Quality Manager sees and edits exactly what was wrong.
	if tasks[0].ElementInstanceKey == 0 {
		t.Fatal("task is missing its elementInstanceKey (needed for form pre-fill)")
	}
	prefill := instanceVars(t, x, tasks[0].ElementInstanceKey)
	if prefill["email"] != "bob" || prefill["group"] != "ops" || prefill["license"] != "NONE" {
		t.Fatalf("task pre-fill = %v, want email=bob group=ops license=NONE from the invalid row", prefill)
	}

	// The Quality Manager corrects the record; the output mapping rewrites `row`, the
	// back-edge re-validates, and the whole instance runs to completion.
	fix := `{"variables":{"email":"bob@x.io","group":"users","license":"BASIC"}}`
	if code, cb := x.do(http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(tasks[0].Key, 10)+"/complete", fix); code != http.StatusOK {
		t.Fatalf("complete task: %d %s", code, cb)
	}
	if rem := listTasks(t, x); len(rem) != 0 {
		t.Fatalf("active user tasks after correction = %d, want 0", len(rem))
	}

	// Both verdicts are now valid, collected in input order at the process root.
	instKey := singleInstanceKey(t, x)
	verdicts := instanceVar(t, x, instKey, "verdicts")
	arr, ok := verdicts.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("verdicts = %v (%T), want an array of 2", verdicts, verdicts)
	}
	for i, v := range arr {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("verdicts[%d] = %v (%T), want an object", i, v, v)
		}
		if valid, _ := obj["valid"].(bool); !valid {
			t.Errorf("verdicts[%d].valid = %v, want true (row validated after correction)", i, obj["valid"])
		}
	}
}

type taskRow struct {
	Key                uint64 `json:"key"`
	Name               string `json:"name"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
}

func listTasks(t *testing.T, x deployTestHarness) []taskRow {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", code, b)
	}
	var tasks []taskRow
	if err := json.Unmarshal(b, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, b)
	}
	return tasks
}

func singleInstanceKey(t *testing.T, x deployTestHarness) uint64 {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/instances", "")
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
	return instances[0].Key
}

// instanceVars reads a scope's variables as a typed map — the process instance's
// root scope, or (with an element-instance key) a task's own scope.
func instanceVars(t *testing.T, x deployTestHarness, key uint64) map[string]any {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(key, 10)+"/variables", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: %d %s", code, b)
	}
	var vars map[string]any
	if err := json.Unmarshal(b, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, b)
	}
	return vars
}

func instanceVar(t *testing.T, x deployTestHarness, key uint64, name string) any {
	t.Helper()
	vars := instanceVars(t, x, key)
	return vars[name]
}
