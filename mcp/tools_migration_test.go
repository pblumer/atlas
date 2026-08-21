package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Two versions of one process, differing where it matters: v2 inserts a task before the
// one a token parks on, so the element indices disagree and a migration that kept them
// would move the token onto the wrong element (ADR-0162).
const migrateToolsV1 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mcpmigrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="done"/>
  </process>
</definitions>`

const migrateToolsV2 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mcpmigrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="triage" name="Triage"/>
    <userTask id="review" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="triage"/>
    <sequenceFlow id="f1" sourceRef="triage" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="done"/>
  </process>
</definitions>`

// deployVersion deploys one XML through the tools and returns its definition key.
func deployVersion(t *testing.T, atlas *httptest.Server, id int, xml string) uint64 {
	t.Helper()
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_deploy", map[string]any{"xml": xml}))[0]))
	if isErr {
		t.Fatalf("deploy: %s", text)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(text), &dep); err != nil {
		t.Fatalf("decode deploy %q: %v", text, err)
	}
	return dep.Key
}

// runningInstanceOf returns the key of a running instance of defKey, via the tools.
func runningInstanceOf(t *testing.T, atlas *httptest.Server, id int, defKey uint64) uint64 {
	t.Helper()
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_list_instances", map[string]any{"process": defKey}))[0]))
	if isErr {
		t.Fatalf("list_instances: %s", text)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("decode instances %q: %v", text, err)
	}
	if len(rows) == 0 {
		t.Fatalf("no running instance of definition %d", defKey)
	}
	return rows[0].Key
}

// TestMigrationToolsPlanThenMigrate drives the operator loop an agent would run: ask
// what a migration would do, then do it. The plan must be answerable *without* changing
// anything — that is why it is a separate tool rather than a flag.
func TestMigrationToolsPlanThenMigrate(t *testing.T) {
	atlas := newAtlas(t)
	v1 := deployVersion(t, atlas, 1, migrateToolsV1)
	v2 := deployVersion(t, atlas, 2, migrateToolsV2)
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_create_instance", map[string]any{"key": v1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	piKey := runningInstanceOf(t, atlas, 4, v1)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_migration_plan", map[string]any{
		"key": piKey, "targetProcessDefKey": v2,
	}))[0]))
	if isErr {
		t.Fatalf("migration_plan: %s", text)
	}
	var plan struct {
		Migratable bool `json:"migratable"`
		Mapping    []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"mapping"`
	}
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		t.Fatalf("decode plan %q: %v", text, err)
	}
	if !plan.Migratable || len(plan.Mapping) == 0 {
		t.Fatalf("plan = %+v, want a migratable instance with a mapping", plan)
	}

	text, isErr = toolText(t, result(t, run(t, atlas, callTool(6, "atlas_migrate_instance", map[string]any{
		"key": piKey, "targetProcessDefKey": v2, "reason": "the review step was wrong",
	}))[0]))
	if isErr {
		t.Fatalf("migrate_instance: %s", text)
	}
	// The instance now runs the target version — it is listed under v2, not v1.
	if got := runningInstanceOf(t, atlas, 7, v2); got != piKey {
		t.Errorf("instance under v2 = %d, want the migrated %d", got, piKey)
	}
}

// TestMigrationToolsRefuseAndBatch covers the two remaining tool paths: a refusal comes
// back with the reason rather than a bare failure, and the batch form reports what it
// moved.
func TestMigrationToolsRefuseAndBatch(t *testing.T) {
	atlas := newAtlas(t)
	v1 := deployVersion(t, atlas, 1, migrateToolsV1)
	v2 := deployVersion(t, atlas, 2, migrateToolsV2)
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_create_instance", map[string]any{"key": v1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	piKey := runningInstanceOf(t, atlas, 4, v1)

	// Migrating to the version it already runs is refused, and says so.
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_migrate_instance", map[string]any{
		"key": piKey, "targetProcessDefKey": v1, "reason": "no-op",
	}))[0]))
	if !isErr && !strings.Contains(text, "already running this version") {
		t.Errorf("migrate to the current version = (%q, isErr=%v), want a refusal saying so", text, isErr)
	}

	// The batch form moves what it can and reports the count.
	text, isErr = toolText(t, result(t, run(t, atlas, callTool(6, "atlas_migrate_instances", map[string]any{
		"key": v1, "targetProcessDefKey": v2, "reason": "fixed the review step", "limit": 10,
	}))[0]))
	if isErr {
		t.Fatalf("migrate_instances: %s", text)
	}
	var batch struct {
		Migrated  int  `json:"migrated"`
		Remaining bool `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(text), &batch); err != nil {
		t.Fatalf("decode batch %q: %v", text, err)
	}
	if batch.Migrated != 1 || batch.Remaining {
		t.Errorf("batch = %+v, want one migrated and nothing remaining", batch)
	}

	// A missing reason never reaches the server: the tool's own schema requires it.
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(7, "atlas_migrate_instance", map[string]any{
		"key": piKey, "targetProcessDefKey": v2,
	}))[0])); !isErr {
		t.Errorf("migrate with no reason = %q, want an error", text)
	}
}
