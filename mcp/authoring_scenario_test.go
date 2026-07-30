package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// callOne calls one tool and returns its text content, failing on a tool error.
func callOne(t *testing.T, ts *httptest.Server, name string, args map[string]any) string {
	t.Helper()
	resps := run(t, ts, callTool(1, name, args))
	text, isErr := toolText(t, result(t, resps[0]))
	if isErr {
		t.Fatalf("%s errored: %s", name, text)
	}
	return text
}

func mustReadObject(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// tasksWithName returns the keys of active tasks whose name matches.
func tasksWithName(t *testing.T, listJSON, name string) []uint64 {
	t.Helper()
	var page struct {
		Items []struct {
			Key  uint64 `json:"key"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(listJSON), &page); err != nil {
		t.Fatalf("decode task page %q: %v", listJSON, err)
	}
	var keys []uint64
	for _, task := range page.Items {
		if task.Name == name {
			keys = append(keys, task.Key)
		}
	}
	return keys
}

// TestAuthoringToolErrors covers each authoring tool's argument-validation
// branches: a missing required argument (or a wrong-typed object) is a tool error.
func TestAuthoringToolErrors(t *testing.T) {
	ts := newAtlas(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"atlas_create_project", map[string]any{}},                                // missing name
		{"atlas_save_draft", map[string]any{}},                                    // missing xml
		{"atlas_save_form", map[string]any{}},                                     // missing id
		{"atlas_save_form", map[string]any{"id": "f"}},                            // missing schema
		{"atlas_save_form", map[string]any{"id": "f", "schema": "not-an-object"}}, // schema not an object
		{"atlas_upload_decision_model", map[string]any{}},                         // missing handle
		{"atlas_upload_decision_model", map[string]any{"handle": "h"}},            // missing xml
		{"atlas_register_decision", map[string]any{}},                             // missing name
		{"atlas_register_decision", map[string]any{"name": "n"}},                  // missing modelRef
		{"atlas_deploy_project", map[string]any{}},                                // missing id
		{"atlas_complete_task", map[string]any{}},                                 // missing key
	}
	for _, tc := range cases {
		resps := run(t, ts, callTool(1, tc.tool, tc.args))
		if _, isErr := toolText(t, result(t, resps[0])); !isErr {
			t.Errorf("%s(%v): want a tool error, got success", tc.tool, tc.args)
		}
	}
}

// TestAuthoringToolsScenario drives the whole CSV-validation scenario through the
// new authoring + task tools — the exact flow an operator asked for: a project
// (folder) holding the diagram, forms, and decision, deployed, then instances
// driven through their user tasks. It exercises every authoring tool end to end
// against a real Atlas server and loads the shipped example files verbatim.
func TestAuthoringToolsScenario(t *testing.T) {
	ts := newAtlas(t)
	ex := func(name string) string { return filepath.Join("..", "examples", name) }

	// 1. A project (folder) to group the scenario.
	projJSON := callOne(t, ts, "atlas_create_project", map[string]any{"name": "CSV_Test"})
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(projJSON), &proj); err != nil || proj.ID == "" {
		t.Fatalf("create_project = %q (err=%v), want an id", projJSON, err)
	}

	// 2. The decision: upload the model and register the reference under the project.
	callOne(t, ts, "atlas_upload_decision_model", map[string]any{
		"handle": "rowvalid", "xml": mustRead(t, ex("pruefe-datensaetze.dmn")),
	})
	callOne(t, ts, "atlas_register_decision", map[string]any{
		"name": "RowValid", "modelRef": "rowvalid", "projectId": proj.ID,
	})

	// 3. The two forms the user tasks bind to.
	callOne(t, ts, "atlas_save_form", map[string]any{
		"id": "csv-upload-form", "projectId": proj.ID, "schema": mustReadObject(t, ex("csv-upload-form.json")),
	})
	callOne(t, ts, "atlas_save_form", map[string]any{
		"id": "row-correction-form", "projectId": proj.ID, "schema": mustReadObject(t, ex("row-correction-form.json")),
	})

	// 4. The diagram draft, then deploy the project (bundling the decision).
	callOne(t, ts, "atlas_save_draft", map[string]any{
		"xml": mustRead(t, ex("pruefe-datensaetze.bpmn")), "projectId": proj.ID,
	})
	depJSON := callOne(t, ts, "atlas_deploy_project", map[string]any{"id": proj.ID})
	var dep struct {
		Definitions []struct {
			Key uint64 `json:"key"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(depJSON), &dep); err != nil || len(dep.Definitions) == 0 {
		t.Fatalf("deploy_project = %q (err=%v), want a deployed definition", depJSON, err)
	}
	defKey := dep.Definitions[0].Key

	// 5. Start an instance; it parks at the "CSV hochladen" upload task.
	callOne(t, ts, "atlas_create_instance", map[string]any{"key": defKey})
	upload := tasksWithName(t, callOne(t, ts, "atlas_list_tasks", map[string]any{}), "CSV hochladen")
	if len(upload) != 1 {
		t.Fatalf("upload tasks = %d, want 1", len(upload))
	}

	// 6. Complete the upload with the file content; the in-process pipeline runs and
	// the invalid row parks on a correction task.
	callOne(t, ts, "atlas_complete_task", map[string]any{
		"key":       upload[0],
		"variables": map[string]any{"csvText": "email,group,license\nada@x.io,users,PRO\nbob,ops,NONE\n"},
	})
	correct := tasksWithName(t, callOne(t, ts, "atlas_list_tasks", map[string]any{}), "Datensatz korrigieren")
	if len(correct) != 1 {
		t.Fatalf("correction tasks = %d, want 1 (only the invalid row)", len(correct))
	}

	// 7. Correct the record; the instance runs to completion.
	callOne(t, ts, "atlas_complete_task", map[string]any{
		"key":       correct[0],
		"variables": map[string]any{"email": "bob@x.io", "group": "users", "license": "BASIC"},
	})
	if rem := tasksWithName(t, callOne(t, ts, "atlas_list_tasks", map[string]any{}), "Datensatz korrigieren"); len(rem) != 0 {
		t.Fatalf("correction tasks after fix = %d, want 0", len(rem))
	}
	instances := callOne(t, ts, "atlas_list_instances", map[string]any{})
	if !strings.Contains(instances, `"completed"`) {
		t.Fatalf("instances = %s, want one completed", instances)
	}

	// The project (folder) lists the artifacts we filed under it.
	if projects := callOne(t, ts, "atlas_list_projects", map[string]any{}); !strings.Contains(projects, "CSV_Test") {
		t.Fatalf("list_projects = %s, want CSV_Test", projects)
	}
}
