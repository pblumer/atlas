package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDecisionEvaluationsViaTool deploys a project whose business rule task
// evaluates a DMN decision, runs an instance through it, then reads that
// decision's cross-instance evaluation history by decision id. An unknown id
// yields an empty list (the endpoint filters by id rather than 404ing).
func TestDecisionEvaluationsViaTool(t *testing.T) {
	atlas := newAtlas(t)

	projJSON := callOne(t, atlas, "atlas_create_project", map[string]any{"name": "DecEval"})
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(projJSON), &proj); err != nil || proj.ID == "" {
		t.Fatalf("create_project = %q (err=%v), want an id", projJSON, err)
	}
	callOne(t, atlas, "atlas_upload_decision_model", map[string]any{"handle": "rowvalid", "xml": mustRead(t, exampleFile("pruefe-datensaetze.dmn"))})
	callOne(t, atlas, "atlas_register_decision", map[string]any{"name": "RowValid", "modelRef": "rowvalid", "projectId": proj.ID})
	callOne(t, atlas, "atlas_save_form", map[string]any{"id": "csv-upload-form", "projectId": proj.ID, "schema": mustReadObject(t, exampleFile("csv-upload-form.json"))})
	callOne(t, atlas, "atlas_save_form", map[string]any{"id": "row-correction-form", "projectId": proj.ID, "schema": mustReadObject(t, exampleFile("row-correction-form.json"))})
	callOne(t, atlas, "atlas_save_draft", map[string]any{"xml": mustRead(t, exampleFile("pruefe-datensaetze.bpmn")), "projectId": proj.ID})

	depJSON := callOne(t, atlas, "atlas_deploy_project", map[string]any{"id": proj.ID})
	var dep struct {
		Definitions []struct {
			Key uint64 `json:"key"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(depJSON), &dep); err != nil || len(dep.Definitions) == 0 {
		t.Fatalf("deploy_project = %q (err=%v), want a deployed definition", depJSON, err)
	}
	callOne(t, atlas, "atlas_create_instance", map[string]any{"key": dep.Definitions[0].Key})

	upload := tasksWithName(t, callOne(t, atlas, "atlas_list_tasks", map[string]any{}), "CSV hochladen")
	if len(upload) != 1 {
		t.Fatalf("upload tasks = %d, want 1", len(upload))
	}
	callOne(t, atlas, "atlas_complete_task", map[string]any{
		"key":       upload[0],
		"variables": map[string]any{"csvText": "email,group,license\nada@x.io,users,PRO\nbob,ops,NONE\n"},
	})

	// The decision's evaluation history now records the per-row evaluations, each
	// tied to the instance that made it.
	evalsJSON := callOne(t, atlas, "atlas_decision_evaluations", map[string]any{"id": "RowValid"})
	var evals []struct {
		InstanceKey uint64          `json:"instanceKey"`
		Outputs     json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(evalsJSON), &evals); err != nil {
		t.Fatalf("decode evaluations %q: %v", evalsJSON, err)
	}
	if len(evals) == 0 || evals[0].InstanceKey == 0 || len(evals[0].Outputs) == 0 {
		t.Fatalf("decision_evaluations = %q, want at least one evaluation with an instance and outputs", evalsJSON)
	}

	// An unknown decision id is not an error — it simply has no evaluations.
	if empty := callOne(t, atlas, "atlas_decision_evaluations", map[string]any{"id": "nope"}); strings.TrimSpace(empty) != "[]" {
		t.Fatalf("decision_evaluations of unknown id = %q, want []", empty)
	}
}
