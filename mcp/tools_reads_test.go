package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// collabRuntimeBPMN is a two-pool collaboration (Buyer throws an "order" message,
// Seller catches it), used to exercise the collaboration-runtime read.
const collabRuntimeBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <collaboration id="Collab_1">
    <participant id="P_buyer" name="Buyer" processRef="buyer"/>
    <participant id="P_seller" name="Seller" processRef="seller"/>
  </collaboration>
  <message id="Msg_order" name="order">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="buyer" isExecutable="true">
    <startEvent id="b_start"/>
    <intermediateThrowEvent id="b_throw"><messageEventDefinition messageRef="Msg_order"/></intermediateThrowEvent>
    <endEvent id="b_end"/>
    <sequenceFlow id="bf1" sourceRef="b_start" targetRef="b_throw"/>
    <sequenceFlow id="bf2" sourceRef="b_throw" targetRef="b_end"/>
  </process>
  <process id="seller" isExecutable="true">
    <startEvent id="s_start"/>
    <intermediateCatchEvent id="s_catch"><messageEventDefinition messageRef="Msg_order"/></intermediateCatchEvent>
    <endEvent id="s_end"/>
    <sequenceFlow id="sf1" sourceRef="s_start" targetRef="s_catch"/>
    <sequenceFlow id="sf2" sourceRef="s_catch" targetRef="s_end"/>
  </process>
</definitions>`

// TestCollaborationRuntimeViaTool reads a deployed collaboration's live runtime
// state by one pool's definition key.
func TestCollaborationRuntimeViaTool(t *testing.T) {
	atlas := newAtlas(t)
	depJSON := callOne(t, atlas, "atlas_deploy", map[string]any{"xml": collabRuntimeBPMN})
	var dep struct {
		Deployments []struct {
			Key uint64 `json:"key"`
		} `json:"deployments"`
	}
	if err := json.Unmarshal([]byte(depJSON), &dep); err != nil || len(dep.Deployments) == 0 {
		t.Fatalf("deploy collaboration = %q (err=%v), want deployments", depJSON, err)
	}

	rt := callOne(t, atlas, "atlas_collaboration_runtime", map[string]any{"key": dep.Deployments[0].Key})
	if !strings.Contains(rt, `"pools"`) || !strings.Contains(rt, "Buyer") || !strings.Contains(rt, "Seller") {
		t.Fatalf("collaboration_runtime = %q, want both pools", rt)
	}

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_collaboration_runtime", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no deployment") {
		t.Fatalf("collaboration_runtime missing = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestListAndGetFormViaTool saves forms (one under a project), lists them all and
// filtered by project, reads one back, and errors on an unknown id.
func TestListAndGetFormViaTool(t *testing.T) {
	atlas := newAtlas(t)
	projJSON := callOne(t, atlas, "atlas_create_project", map[string]any{"name": "FormsProj"})
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(projJSON), &proj); err != nil || proj.ID == "" {
		t.Fatalf("create_project = %q (err=%v), want an id", projJSON, err)
	}
	callOne(t, atlas, "atlas_save_form", map[string]any{"id": "f1", "projectId": proj.ID, "schema": mustReadObject(t, exampleFile("csv-upload-form.json"))})
	callOne(t, atlas, "atlas_save_form", map[string]any{"id": "f2", "schema": mustReadObject(t, exampleFile("row-correction-form.json"))})

	if all := callOne(t, atlas, "atlas_list_forms", map[string]any{}); !strings.Contains(all, "f1") || !strings.Contains(all, "f2") {
		t.Fatalf("list_forms = %q, want both f1 and f2", all)
	}
	// Parse the filtered list rather than substring-matching the raw JSON: the
	// project id is a random hash that can itself contain "f2" (e.g.
	// "e1f2b4fe852fac60"), which would false-positive a Contains(filtered, "f2")
	// check even when only f1 is returned.
	filtered := callOne(t, atlas, "atlas_list_forms", map[string]any{"projectId": proj.ID})
	var filteredForms []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(filtered), &filteredForms); err != nil {
		t.Fatalf("list_forms filtered = %q, unmarshal err=%v", filtered, err)
	}
	if len(filteredForms) != 1 || filteredForms[0].ID != "f1" {
		t.Fatalf("list_forms filtered = %q, want f1 only", filtered)
	}
	if form := callOne(t, atlas, "atlas_get_form", map[string]any{"id": "f1"}); !strings.Contains(form, "f1") {
		t.Fatalf("get_form = %q, want the f1 form", form)
	}
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_get_form", map[string]any{"id": "nope"}))[0]))
	if !isErr || !strings.Contains(text, "no form") {
		t.Fatalf("get missing form = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestListAndGetDraftViaTool saves a draft, lists it, reads its XML back, and
// errors on an unknown process id.
func TestListAndGetDraftViaTool(t *testing.T) {
	atlas := newAtlas(t)
	callOne(t, atlas, "atlas_save_draft", map[string]any{"xml": sampleBPMN})

	if list := callOne(t, atlas, "atlas_list_drafts", map[string]any{}); !strings.Contains(list, "order") {
		t.Fatalf("list_drafts = %q, want the order draft", list)
	}
	if xml := callOne(t, atlas, "atlas_get_draft_xml", map[string]any{"id": "order"}); !strings.Contains(xml, `id="order"`) {
		t.Fatalf("get_draft_xml = %q, want the order BPMN", xml)
	}
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_get_draft_xml", map[string]any{"id": "nope"}))[0]))
	if !isErr || !strings.Contains(text, "no draft") {
		t.Fatalf("get missing draft = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestListDecisionRefsAndDecisionsViaTool registers a decision reference and lists
// both the references and the decisions discoverable from them.
func TestListDecisionRefsAndDecisionsViaTool(t *testing.T) {
	atlas := newAtlas(t)
	callOne(t, atlas, "atlas_upload_decision_model", map[string]any{"handle": "rowvalid", "xml": mustRead(t, exampleFile("pruefe-datensaetze.dmn"))})
	callOne(t, atlas, "atlas_register_decision", map[string]any{"name": "RowValid", "modelRef": "rowvalid"})

	if refs := callOne(t, atlas, "atlas_list_decision_refs", map[string]any{}); !strings.Contains(refs, "RowValid") || !strings.Contains(refs, "rowvalid") {
		t.Fatalf("list_decision_refs = %q, want the RowValid reference", refs)
	}
	if decisions := callOne(t, atlas, "atlas_list_decisions", map[string]any{}); !strings.Contains(decisions, "RowValid") {
		t.Fatalf("list_decisions = %q, want the RowValid decision", decisions)
	}
}
