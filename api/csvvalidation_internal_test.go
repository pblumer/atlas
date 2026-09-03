package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// saveForm stores a form-js schema under an id so a user task's formId resolves.
func saveForm(t *testing.T, x deployTestHarness, id, name, schemaPath string) {
	t.Helper()
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read form %s: %v", schemaPath, err)
	}
	body, err := json.Marshal(map[string]any{"id": id, "name": name, "schema": json.RawMessage(schema)})
	if err != nil {
		t.Fatalf("marshal form: %v", err)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/forms", string(body)); code != http.StatusOK {
		t.Fatalf("save form %s: %d %s", id, code, b)
	}
}

// TestCSVProcessUploadAndCorrection is the end-to-end proof of the process-driven
// flow (ADR-0087, ADR-0139): the shipped example decision, process, and forms are
// deployed as-is; an instance starts and parks at a "CSV hochladen" user task;
// completing it with the file content (as the Tasks app would from an uploaded file)
// drives the in-process pipeline — the CSV-to-JSON task parses it into rows
// with its layout authored on the task, and the multi-instance subprocess validates
// each row. The invalid row parks on a correction task; correcting it re-validates and
// runs the instance to completion with all verdicts valid. Loads the example files
// verbatim so a drift breaks the build.
func TestCSVProcessUploadAndCorrection(t *testing.T) {
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

	// Seed the DMN model for the resolver and register it as a decision reference.
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "rowvalid.dmn"), dmnModel, 0o644); err != nil {
		t.Fatalf("seed rowvalid.dmn: %v", err)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"RowValid","modelRef":"rowvalid"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	// Both forms the two user tasks bind to.
	saveForm(t, x, "csv-upload-form", "CSV hochladen", filepath.Join("..", "examples", "csv-upload-form.json"))
	saveForm(t, x, "row-correction-form", "Datensatz korrigieren", filepath.Join("..", "examples", "row-correction-form.json"))

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

	// Start the process; it parks at the "CSV hochladen" user task.
	if code, ib := x.do(http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", "{}"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, ib)
	}
	upload := listTasks(t, x)
	if len(upload) != 1 || upload[0].Name != "CSV hochladen" {
		t.Fatalf("tasks = %+v, want one \"CSV hochladen\"", upload)
	}

	// Complete the upload task with the file content — what the Tasks app submits
	// from the picked file — as the csvText variable.
	csv := "email,group,license\nada@x.io,users,PRO\nbob,ops,NONE\n"
	uploadBody, err := json.Marshal(map[string]any{"variables": map[string]any{"csvText": csv}})
	if err != nil {
		t.Fatalf("marshal upload: %v", err)
	}
	if code, cb := x.do(http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(upload[0].Key, 10)+"/complete", string(uploadBody)); code != http.StatusOK {
		t.Fatalf("complete upload: %d %s", code, cb)
	}

	// The CSV worker parsed the rows against its authored layout and validation
	// ran: exactly the invalid row (bob) parks on a correction task; the clean ada
	// passed straight through.
	tasks := listTasks(t, x)
	if len(tasks) != 1 || tasks[0].Name != "Datensatz korrigieren" {
		t.Fatalf("tasks after upload = %+v, want one \"Datensatz korrigieren\"", tasks)
	}
	if tasks[0].ElementInstanceKey == 0 {
		t.Fatal("correction task is missing its elementInstanceKey (needed for form pre-fill)")
	}
	prefill := instanceVars(t, x, tasks[0].ElementInstanceKey)
	if prefill["email"] != "bob" || prefill["group"] != "ops" || prefill["license"] != "NONE" {
		t.Fatalf("task pre-fill = %v, want email=bob group=ops license=NONE", prefill)
	}

	// Correct the record; the output mapping rewrites row, the back-edge re-validates,
	// and the instance runs to completion.
	fix := `{"variables":{"email":"bob@x.io","group":"users","license":"BASIC"}}`
	if code, cb := x.do(http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(tasks[0].Key, 10)+"/complete", fix); code != http.StatusOK {
		t.Fatalf("complete correction: %d %s", code, cb)
	}
	if rem := listTasks(t, x); len(rem) != 0 {
		t.Fatalf("tasks after correction = %d, want 0", len(rem))
	}

	// Both verdicts are valid, collected in input order at the process root.
	verdicts := instanceVar(t, x, singleInstanceKey(t, x), "verdicts")
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
			t.Errorf("verdicts[%d].valid = %v, want true", i, obj["valid"])
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
	return instanceVars(t, x, key)[name]
}
