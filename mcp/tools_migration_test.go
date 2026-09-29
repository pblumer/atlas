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
	return firstInstanceKey(t, atlas, id, map[string]any{"process": defKey})
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

// An agent draining a version does what the description says: repeat while remaining,
// handing each call's cursor to the next. The tool has to carry that cursor through to
// the server — without it every call starts from the oldest instance again — and has to
// refuse one it cannot pass on rather than drop it and restart the walk.
func TestMigrationToolsBatchCursor(t *testing.T) {
	atlas := newAtlas(t)
	v1 := deployVersion(t, atlas, 1, migrateToolsV1)
	v2 := deployVersion(t, atlas, 2, migrateToolsV2)
	for id := 3; id < 5; id++ {
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_create_instance", map[string]any{"key": v1}))[0])); isErr {
			t.Fatal("create_instance failed")
		}
	}
	type step struct {
		Migrated   int    `json:"migrated"`
		Remaining  bool   `json:"remaining"`
		NextCursor string `json:"nextCursor"`
	}
	call := func(id int, args map[string]any) step {
		t.Helper()
		text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_migrate_instances", args))[0]))
		if isErr {
			t.Fatalf("migrate_instances %v: %s", args, text)
		}
		var s step
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			t.Fatalf("decode %q: %v", text, err)
		}
		return s
	}

	first := call(5, map[string]any{"key": v1, "targetProcessDefKey": v2, "reason": "drain", "limit": 1})
	if first.Migrated != 1 || !first.Remaining || first.NextCursor == "" {
		t.Fatalf("first call = %+v, want one migrated and a cursor", first)
	}
	// A cursor past every instance selects nothing — which a call that dropped the
	// cursor would not answer, since the second instance is still waiting at the front.
	if past := call(8, map[string]any{"key": v1, "targetProcessDefKey": v2, "reason": "drain", "after": "18446744073709551615"}); past.Migrated != 0 || past.Remaining {
		t.Errorf("a cursor past the end = %+v, want nothing selected", past)
	}
	second := call(6, map[string]any{"key": v1, "targetProcessDefKey": v2, "reason": "drain", "limit": 1, "after": first.NextCursor})
	if second.Migrated != 1 || second.Remaining || second.NextCursor != "" {
		t.Errorf("second call = %+v, want the other instance migrated and the walk finished", second)
	}

	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(7, "atlas_migrate_instances", map[string]any{
		"key": v1, "targetProcessDefKey": v2, "reason": "drain", "after": 42,
	}))[0])); !isErr || !strings.Contains(text, "nextCursor") {
		t.Errorf("a numeric cursor = (%q, isErr=%v), want a refusal naming what to pass", text, isErr)
	}
}

// The repair beside the migration: an operator (or an agent driving one) can bring a
// version's running instances back in line with what it declares searchable, without
// having to reason about when that version was deployed. The tool is a plain proxy, so
// what this covers is that it reaches the endpoint and hands the answer back whole.
func TestReindexInstancesTool(t *testing.T) {
	atlas := newAtlas(t)
	v1 := deployVersion(t, atlas, 1, migrateToolsV1)
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_create_instance", map[string]any{"key": v1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_reindex_instances", map[string]any{
		"key": v1, "limit": 10,
	}))[0]))
	if isErr {
		t.Fatalf("reindex_instances: %s", text)
	}
	var got struct {
		ProcessDefKey uint64   `json:"processDefKey"`
		Searchable    []string `json:"searchable"`
		Submitted     int      `json:"submitted"`
		Remaining     bool     `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	// This version declares nothing searchable, so the repair has one instance to visit
	// and nothing to write for it — which is exactly the shape of a no-op run.
	if got.ProcessDefKey != v1 || got.Submitted != 1 || got.Remaining || len(got.Searchable) != 0 {
		t.Errorf("reindex = %+v, want the definition, one instance submitted, nothing remaining, no declaration", got)
	}
}

// The repair walks a version the way the migration batch does, and for a sharper reason:
// a repaired instance stays on its version, so without the cursor every call would
// select the same page. The tool carries the cursor through and refuses one it cannot.
func TestReindexInstancesToolCursor(t *testing.T) {
	atlas := newAtlas(t)
	v1 := deployVersion(t, atlas, 1, migrateToolsV1)
	for id := 2; id < 4; id++ {
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_create_instance", map[string]any{"key": v1}))[0])); isErr {
			t.Fatal("create_instance failed")
		}
	}
	type step struct {
		Submitted  int    `json:"submitted"`
		Remaining  bool   `json:"remaining"`
		NextCursor string `json:"nextCursor"`
	}
	call := func(id int, args map[string]any) step {
		t.Helper()
		text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_reindex_instances", args))[0]))
		if isErr {
			t.Fatalf("reindex_instances %v: %s", args, text)
		}
		var s step
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			t.Fatalf("decode %q: %v", text, err)
		}
		return s
	}

	first := call(4, map[string]any{"key": v1, "limit": 1})
	if first.Submitted != 1 || !first.Remaining || first.NextCursor == "" {
		t.Fatalf("first call = %+v, want one submitted and a cursor", first)
	}
	second := call(5, map[string]any{"key": v1, "limit": 1, "after": first.NextCursor})
	if second.Submitted != 1 || second.Remaining || second.NextCursor != "" {
		t.Errorf("second call = %+v, want the other instance and the walk finished", second)
	}
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(6, "atlas_reindex_instances", map[string]any{
		"key": v1, "after": 7,
	}))[0])); !isErr || !strings.Contains(text, "nextCursor") {
		t.Errorf("a numeric cursor = (%q, isErr=%v), want a refusal naming what to pass", text, isErr)
	}
}
