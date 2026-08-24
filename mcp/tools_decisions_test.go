package mcp_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// exampleFile returns a path into the shipped examples directory.
func exampleFile(name string) string { return filepath.Join("..", "examples", name) }

// TestGetDecisionModelViaTool uploads a DMN model under a handle and reads its raw
// XML back through the tool.
func TestGetDecisionModelViaTool(t *testing.T) {
	atlas := newAtlas(t)
	callOne(t, atlas, "atlas_upload_decision_model", map[string]any{
		"handle": "rowvalid", "xml": mustRead(t, exampleFile("pruefe-datensaetze.dmn")),
	})

	xml := callOne(t, atlas, "atlas_get_decision_model", map[string]any{"ref": "rowvalid"})
	if !strings.Contains(xml, `id="RowValid"`) {
		t.Fatalf("get_decision_model = %q, want the RowValid DMN XML", xml)
	}

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_get_decision_model", map[string]any{"ref": "nope"}))[0]))
	if !isErr || !strings.Contains(text, "no DMN model matches") {
		t.Fatalf("get missing model = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestDmnRefGraphViaTool registers a decision reference over an uploaded model and
// reads its decision requirements graph by the reference id.
func TestDmnRefGraphViaTool(t *testing.T) {
	atlas := newAtlas(t)
	callOne(t, atlas, "atlas_upload_decision_model", map[string]any{
		"handle": "rowvalid", "xml": mustRead(t, exampleFile("pruefe-datensaetze.dmn")),
	})
	refJSON := callOne(t, atlas, "atlas_register_decision", map[string]any{"name": "RowValid", "modelRef": "rowvalid"})
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(refJSON), &ref); err != nil || ref.ID == "" {
		t.Fatalf("register_decision = %q (err=%v), want an id", refJSON, err)
	}

	graph := callOne(t, atlas, "atlas_dmnref_graph", map[string]any{"id": ref.ID})
	if !strings.Contains(graph, `"nodes"`) || !strings.Contains(graph, "RowValid") {
		t.Fatalf("dmnref_graph = %q, want a graph with the RowValid decision node", graph)
	}

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_dmnref_graph", map[string]any{"id": "nope"}))[0]))
	if !isErr || !strings.Contains(text, "no dmn reference") {
		t.Fatalf("graph of missing ref = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestDeployedAndInstanceDecisionsViaTool deploys a project whose business rule
// task evaluates a DMN decision, runs an instance through it, then reads the
// deployed-decisions overview and the instance's own evaluation history.
func TestDeployedAndInstanceDecisionsViaTool(t *testing.T) {
	atlas := newAtlas(t)

	projJSON := callOne(t, atlas, "atlas_create_project", map[string]any{"name": "Decisions"})
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(projJSON), &proj); err != nil || proj.ID == "" {
		t.Fatalf("create_project = %q (err=%v), want an id", projJSON, err)
	}

	callOne(t, atlas, "atlas_upload_decision_model", map[string]any{
		"handle": "rowvalid", "xml": mustRead(t, exampleFile("pruefe-datensaetze.dmn")),
	})
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

	// Deployed decisions list the bundled RowValid decision right after deploy.
	if deployed := callOne(t, atlas, "atlas_deployed_decisions", map[string]any{}); !strings.Contains(deployed, `"decisionId":"RowValid"`) {
		t.Fatalf("deployed_decisions = %q, want the RowValid decision", deployed)
	}

	// Start an instance and find its key.
	callOne(t, atlas, "atlas_create_instance", map[string]any{"key": dep.Definitions[0].Key})
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(callOne(t, atlas, "atlas_list_instances", map[string]any{})), &instances); err != nil || len(instances) == 0 {
		t.Fatalf("list_instances: err=%v, want an instance", err)
	}
	instanceKey := instances[0].Key

	// Complete the upload task; the in-process pipeline evaluates RowValid per row.
	upload := tasksWithName(t, callOne(t, atlas, "atlas_list_tasks", map[string]any{}), "CSV hochladen")
	if len(upload) != 1 {
		t.Fatalf("upload tasks = %d, want 1", len(upload))
	}
	callOne(t, atlas, "atlas_complete_task", map[string]any{
		"key":       upload[0],
		"variables": map[string]any{"csvText": "email,group,license\nada@x.io,users,PRO\nbob,ops,NONE\n"},
	})

	// The instance's decision history now records the RowValid evaluations it made.
	instDecisions := callOne(t, atlas, "atlas_instance_decisions", map[string]any{"key": instanceKey})
	if !strings.Contains(instDecisions, `"decisionId":"RowValid"`) {
		t.Fatalf("instance_decisions = %q, want a RowValid evaluation", instDecisions)
	}
}
