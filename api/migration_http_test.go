package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Two versions of one process, differing where it matters: v2 inserts a task before the
// one a token parks on, so the element *indices* disagree between them. A migration that
// kept the index would move the token onto the new task; these tests are what says it
// does not (ADR-0162).
const migrateV1BPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="migrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="done"/>
  </process>
</definitions>`

const migrateV2BPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="migrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="triage" name="Triage"/>
    <userTask id="review" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="triage"/>
    <sequenceFlow id="f1" sourceRef="triage" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="done"/>
  </process>
</definitions>`

// migrateV2RenamedBPMN is v2 with the parked task's id changed — the case the default
// id matching cannot cover and an explicit override exists for.
const migrateV2RenamedBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="migrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review_v2" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review_v2"/>
    <sequenceFlow id="f2" sourceRef="review_v2" targetRef="done"/>
  </process>
</definitions>`

func deployXML(t *testing.T, ts *httptest.Server, xml string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	return deploy.Key
}

// startInstance creates one instance of defKey and returns its key, read back from the
// instance list — the create response reports the definition and the running totals, not
// the new instance's key, so the newest instance of that definition is the one just made.
func startInstance(t *testing.T, ts *httptest.Server, defKey uint64) uint64 {
	t.Helper()
	before := instanceKeysOf(t, ts, defKey)
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	for k := range instanceKeysOf(t, ts, defKey) {
		if !before[k] {
			return k
		}
	}
	t.Fatalf("no new instance of definition %d after creating one", defKey)
	return 0
}

// instanceKeysOf is the set of a definition's currently listed instance keys.
func instanceKeysOf(t *testing.T, ts *httptest.Server, defKey uint64) map[uint64]bool {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d", defKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	keys := map[uint64]bool{}
	for _, r := range rows {
		keys[r.Key] = true
	}
	return keys
}

type planResp struct {
	InstanceKey       uint64 `json:"instanceKey"`
	FromProcessDefKey uint64 `json:"fromProcessDefKey"`
	FromVersion       int32  `json:"fromVersion"`
	ToProcessDefKey   uint64 `json:"toProcessDefKey"`
	ToVersion         int32  `json:"toVersion"`
	Migratable        bool   `json:"migratable"`
	Mapping           []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"mapping"`
	Problems []struct {
		ElementID string `json:"elementId"`
		Reason    string `json:"reason"`
	} `json:"problems"`
}

func migrateCall(t *testing.T, ts *httptest.Server, path, body string) (int, planResp, []byte) {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodPost, path, body, "application/json")
	var p planResp
	_ = json.Unmarshal(raw, &p)
	return code, p, raw
}

// TestMigrationPlanAnswersWithoutWriting is why the plan endpoint exists: the mapping is
// derived from two graphs an operator cannot diff by eye, and asking must not be the
// same act as doing.
func TestMigrationPlanAnswersWithoutWriting(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	piKey := startInstance(t, ts, v1)

	code, plan, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/plan", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2))
	if code != http.StatusOK {
		t.Fatalf("plan: status=%d body=%s", code, raw)
	}
	if !plan.Migratable || len(plan.Problems) != 0 {
		t.Fatalf("plan = %+v, want a migratable instance with no problems", plan)
	}
	if plan.FromProcessDefKey != v1 || plan.ToProcessDefKey != v2 {
		t.Errorf("plan endpoints = %d → %d, want %d → %d", plan.FromProcessDefKey, plan.ToProcessDefKey, v1, v2)
	}
	if plan.FromVersion != 1 || plan.ToVersion != 2 {
		t.Errorf("plan versions = v%d → v%d, want v1 → v2", plan.FromVersion, plan.ToVersion)
	}
	// The mapping is in element ids, the vocabulary an operator can check against a
	// diagram — never compiled indices.
	pairs := map[string]string{}
	for _, p := range plan.Mapping {
		pairs[p.From] = p.To
	}
	for _, id := range []string{"start", "review", "done"} {
		if pairs[id] != id {
			t.Errorf("mapping[%q] = %q, want the same id in the target", id, pairs[id])
		}
	}
	if _, ok := pairs["triage"]; ok {
		t.Error("mapping names an element only the target has; it maps source → target")
	}

	// Nothing was written: the instance is still on v1.
	if got := instanceDefKey(t, ts, piKey); got != v1 {
		t.Errorf("after planning, the instance is on def %d; a plan must write nothing", got)
	}
}

// TestMigrateMovesTheInstance is the endpoint doing its job, and the API's half of the
// promise that the instance survives: same key, same variables, new version.
func TestMigrateMovesTheInstance(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	piKey := startInstance(t, ts, v1)

	// A variable set before the migration must still be there after it — the whole
	// point of rebinding rather than cancelling and restarting.
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", piKey),
		`{"variables":{"betrag":42}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("set variable: status=%d body=%s", code, body)
	}

	code, plan, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"the review step was wrong"}`, v2))
	if code != http.StatusOK {
		t.Fatalf("migrate: status=%d body=%s", code, raw)
	}
	if !plan.Migratable {
		t.Fatalf("migrate returned a non-migratable plan: %+v", plan)
	}
	if got := instanceDefKey(t, ts, piKey); got != v2 {
		t.Errorf("instance is on def %d after migrating, want %d", got, v2)
	}
	code, vars := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", piKey), "", "")
	if code != http.StatusOK || !strings.Contains(string(vars), `"betrag"`) {
		t.Errorf("variables after migrating = %d %s, want betrag to have survived", code, vars)
	}
}

// TestMigrateRefusesWithThePlanItself: a refusal has to be as informative as a dry run,
// or an operator has to ask twice to learn why.
func TestMigrateRefusesWithThePlanItself(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	renamed := deployXML(t, ts, migrateV2RenamedBPMN)
	piKey := startInstance(t, ts, v1)

	code, plan, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"try it"}`, renamed))
	if code != http.StatusConflict {
		t.Fatalf("migrate onto a renamed element = %d %s, want 409", code, raw)
	}
	if plan.Migratable || len(plan.Problems) == 0 {
		t.Fatalf("refusal carried no problems: %+v", plan)
	}
	if plan.Problems[0].ElementID != "review" {
		t.Errorf("problem names element %q, want the parked one", plan.Problems[0].ElementID)
	}
	if !strings.Contains(plan.Problems[0].Reason, "stranded") {
		t.Errorf("problem reason = %q, want it to say the token would be stranded", plan.Problems[0].Reason)
	}
	if got := instanceDefKey(t, ts, piKey); got != v1 {
		t.Errorf("a refused migration moved the instance to def %d", got)
	}

	// Naming the renamed element explicitly is exactly what overrides are for.
	code, plan, raw = migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"map the rename","mapping":[{"from":"review","to":"review_v2"}]}`, renamed))
	if code != http.StatusOK {
		t.Fatalf("migrate with an override = %d %s, want 200", code, raw)
	}
	if got := instanceDefKey(t, ts, piKey); got != renamed {
		t.Errorf("instance is on def %d, want the override to have moved it to %d", got, renamed)
	}
}

// TestMigrateValidatesItsRequest covers the gates in front of the plan: a migration is
// an operator action on live state, so it is admin-shaped, explained, and refuses a
// target that is not another version of the same process.
func TestMigrateValidatesItsRequest(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	other := deployXML(t, ts, incidentUserTaskBPMN) // a different process id entirely
	piKey := startInstance(t, ts, v1)

	base := fmt.Sprintf("/api/v1/instances/%d/migrate", piKey)
	if code, _, raw := migrateCall(t, ts, base, fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2)); code != http.StatusBadRequest {
		t.Errorf("migrate with no reason = %d %s, want 400", code, raw)
	}
	if code, _, raw := migrateCall(t, ts, base, `{"reason":"x"}`); code != http.StatusBadRequest {
		t.Errorf("migrate with no target = %d %s, want 400", code, raw)
	}
	if code, _, raw := migrateCall(t, ts, base, `{`); code != http.StatusBadRequest {
		t.Errorf("migrate with a broken body = %d %s, want 400", code, raw)
	}
	if code, _, raw := migrateCall(t, ts, "/api/v1/instances/999999/migrate",
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2)); code != http.StatusNotFound {
		t.Errorf("migrate an unknown instance = %d %s, want 404", code, raw)
	}

	// A different process: every element means something else, so this is not a
	// migration at all.
	code, plan, raw := migrateCall(t, ts, base, fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"wrong process"}`, other))
	if code != http.StatusConflict {
		t.Fatalf("migrate to another process = %d %s, want 409", code, raw)
	}
	if len(plan.Problems) == 0 || !strings.Contains(plan.Problems[0].Reason, "its own process") {
		t.Errorf("problems = %+v, want it to say an instance moves only between versions of its own process", plan.Problems)
	}

	// The version it is already on.
	code, plan, raw = migrateCall(t, ts, base, fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"same"}`, v1))
	if code != http.StatusConflict || len(plan.Problems) == 0 ||
		!strings.Contains(plan.Problems[0].Reason, "already running this version") {
		t.Errorf("migrate to the current version = %d %s, want 409 saying so", code, raw)
	}

	// An override naming an element neither version has is a typo, and a silently
	// ignored override is an operator believing they mapped something they did not.
	code, plan, raw = migrateCall(t, ts, base,
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"typo","mapping":[{"from":"nope","to":"review"}]}`, v2))
	if code != http.StatusConflict || len(plan.Problems) == 0 ||
		!strings.Contains(plan.Problems[0].Reason, `no element "nope"`) {
		t.Errorf("migrate with an unknown override = %d %s, want 409 naming the id", code, raw)
	}
}

// TestMigrateInstancesOfProcessBatches covers the batch form: every instance is its own
// command, so one refusal does not cost the others, and the cap is reported rather than
// silently truncating.
func TestMigrateInstancesOfProcessBatches(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	var keys []uint64
	for i := 0; i < 3; i++ {
		keys = append(keys, startInstance(t, ts, v1))
	}

	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"fixed the review step"}`, v2), "application/json")
	if code != http.StatusOK {
		t.Fatalf("batch migrate: status=%d body=%s", code, body)
	}
	var resp struct {
		Migrated  int  `json:"migrated"`
		Remaining bool `json:"remaining"`
		Refused   []struct {
			InstanceKey uint64 `json:"instanceKey"`
		} `json:"refused"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Migrated != len(keys) || len(resp.Refused) != 0 || resp.Remaining {
		t.Fatalf("batch result = %+v, want all %d migrated and nothing remaining", resp, len(keys))
	}
	for _, k := range keys {
		if got := instanceDefKey(t, ts, k); got != v2 {
			t.Errorf("instance %d is on def %d, want %d", k, got, v2)
		}
	}

	// A limit below the population reports that more remain rather than pretending it
	// finished — the caller repeats.
	for i := 0; i < 2; i++ {
		startInstance(t, ts, v2)
	}
	v3 := deployXML(t, ts, migrateV1BPMN) // a third version of the same process
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances?limit=1", v2),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"onward"}`, v3), "application/json")
	if code != http.StatusOK {
		t.Fatalf("batch migrate with a limit: status=%d body=%s", code, body)
	}
	resp.Migrated, resp.Remaining = 0, false
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Migrated != 1 || !resp.Remaining {
		t.Errorf("limited batch = %+v, want one migrated and remaining=true", resp)
	}

	// Bad requests are refused before anything is scanned.
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2), "application/json"); code != http.StatusBadRequest {
		t.Errorf("batch with no reason = %d %s, want 400", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances?limit=0", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2), "application/json"); code != http.StatusBadRequest {
		t.Errorf("batch with limit=0 = %d %s, want 400", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances?after=next", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2), "application/json"); code != http.StatusBadRequest {
		t.Errorf("batch with a non-numeric cursor = %d %s, want 400", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/999999/migrate-instances",
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2), "application/json"); code != http.StatusNotFound {
		t.Errorf("batch on an unknown definition = %d %s, want 404", code, body)
	}
}

// instanceDefKey reads which deployed definition an instance is currently bound to.
func instanceDefKey(t *testing.T, ts *httptest.Server, piKey uint64) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", piKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, body)
	}
	var tl struct {
		ProcessDefKey uint64 `json:"processDefKey"`
	}
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	return tl.ProcessDefKey
}

// TestMigrationEndpointGuards covers the cheap refusals in front of the plan — a
// malformed key, a missing target, an instance that is not running — on both endpoints,
// so neither reaches the run loop with nonsense.
func TestMigrationEndpointGuards(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	piKey := startInstance(t, ts, v1)

	for _, path := range []string{"/api/v1/instances/nope/migrate", "/api/v1/instances/nope/migrate/plan"} {
		if code, _, raw := migrateCall(t, ts, path, fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2)); code != http.StatusBadRequest {
			t.Errorf("%s with a non-numeric key = %d %s, want 400", path, code, raw)
		}
	}
	if code, _, raw := migrateCall(t, ts, "/api/v1/processes/nope/migrate-instances",
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"x"}`, v2)); code != http.StatusBadRequest {
		t.Errorf("batch with a non-numeric definition key = %d %s, want 400", code, raw)
	}
	planPath := fmt.Sprintf("/api/v1/instances/%d/migrate/plan", piKey)
	if code, _, raw := migrateCall(t, ts, planPath, `{}`); code != http.StatusBadRequest {
		t.Errorf("plan with no target = %d %s, want 400", code, raw)
	}
	if code, _, raw := migrateCall(t, ts, planPath, `{`); code != http.StatusBadRequest {
		t.Errorf("plan with a broken body = %d %s, want 400", code, raw)
	}
	if code, _, raw := migrateCall(t, ts, "/api/v1/instances/999999/migrate/plan",
		fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2)); code != http.StatusNotFound {
		t.Errorf("plan for an unknown instance = %d %s, want 404", code, raw)
	}

	// A target key that is not deployed at all: the plan says so rather than 404-ing on
	// the instance, because the instance is fine — the request is not.
	code, plan, raw := migrateCall(t, ts, planPath, `{"targetProcessDefKey":424242}`)
	if code != http.StatusOK || plan.Migratable || len(plan.Problems) == 0 ||
		!strings.Contains(plan.Problems[0].Reason, "no deployed definition") {
		t.Errorf("plan onto an undeployed target = %d %s, want a plan naming the missing definition", code, raw)
	}

	// An override whose *target* id does not exist is as much a typo as a bad source.
	code, plan, raw = migrateCall(t, ts, planPath,
		fmt.Sprintf(`{"targetProcessDefKey":%d,"mapping":[{"from":"review","to":"nope"}]}`, v2))
	if code != http.StatusOK || plan.Migratable || len(plan.Problems) == 0 ||
		!strings.Contains(plan.Problems[0].Reason, `no element "nope"`) {
		t.Errorf("plan with an unknown override target = %d %s, want it named", code, raw)
	}
}

// TestMigrateInstancesReportsRefusals covers the batch form's other half: an instance it
// cannot move comes back as a full plan rather than a count, so an operator sees which
// ones were left behind and why — the reason each instance is its own command.
func TestMigrateInstancesReportsRefusals(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	renamed := deployXML(t, ts, migrateV2RenamedBPMN)
	for i := 0; i < 2; i++ {
		startInstance(t, ts, v1)
	}

	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"onto a renamed element"}`, renamed), "application/json")
	if code != http.StatusOK {
		t.Fatalf("batch migrate: status=%d body=%s", code, body)
	}
	var resp struct {
		Migrated int `json:"migrated"`
		Refused  []struct {
			InstanceKey uint64 `json:"instanceKey"`
			Problems    []struct {
				ElementID string `json:"elementId"`
				Reason    string `json:"reason"`
			} `json:"problems"`
		} `json:"refused"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Migrated != 0 || len(resp.Refused) != 2 {
		t.Fatalf("batch = %+v, want nothing migrated and both refused", resp)
	}
	for _, r := range resp.Refused {
		if r.InstanceKey == 0 || len(r.Problems) == 0 {
			t.Errorf("refusal = %+v, want it to name the instance and why", r)
		}
		if r.Problems[0].ElementID != "review" {
			t.Errorf("refusal names element %q, want the parked one", r.Problems[0].ElementID)
		}
	}
}

// batchStep is one call of the batch form as a caller repeating it makes one: the
// cursor the previous call handed back goes into the next.
type batchStep struct {
	Migrated   int    `json:"migrated"`
	Remaining  bool   `json:"remaining"`
	NextCursor string `json:"nextCursor"`
	Refused    []struct {
		InstanceKey uint64 `json:"instanceKey"`
	} `json:"refused"`
}

func migrateBatchStep(t *testing.T, ts *httptest.Server, from, to uint64, limit int, after string) batchStep {
	t.Helper()
	path := fmt.Sprintf("/api/v1/processes/%d/migrate-instances?limit=%d", from, limit)
	if after != "" {
		path += "&after=" + after
	}
	code, body := doReq(t, ts, http.MethodPost, path,
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"draining"}`, to), "application/json")
	if code != http.StatusOK {
		t.Fatalf("batch step after %q: status=%d body=%s", after, code, body)
	}
	var step batchStep
	if err := json.Unmarshal(body, &step); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return step
}

// A refused instance stays on the version it was on, so a batch that starts from the
// front every time selects it again on every call. With one refusal per page that
// repeated it in every later page's report; with a whole page refused it was the only
// thing any call ever selected, `remaining` stayed true, and a caller doing exactly
// what the contract says — repeat while remaining — never reached the instances behind
// it and never stopped. The cursor is what makes "repeat" a walk: each call continues
// past the last instance the previous one looked at, whatever became of it.
func TestMigrateInstancesCursorNeverRevisitsARefusal(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	renamed := deployXML(t, ts, migrateV2RenamedBPMN) // every instance's token is stranded there
	started := map[uint64]bool{}
	for i := 0; i < 4; i++ {
		started[startInstance(t, ts, v1)] = true
	}

	seen := map[uint64]int{}
	cursor, calls := "", 0
	for {
		calls++
		if calls > 4 {
			t.Fatalf("still remaining after %d calls — the batch is not advancing (seen %v)", calls-1, seen)
		}
		step := migrateBatchStep(t, ts, v1, renamed, 2, cursor)
		if step.Migrated != 0 {
			t.Fatalf("call %d migrated %d onto a version that strands every token", calls, step.Migrated)
		}
		for _, r := range step.Refused {
			seen[r.InstanceKey]++
		}
		if !step.Remaining {
			if step.NextCursor != "" {
				t.Errorf("last call hands back cursor %q; nothing is left to continue to", step.NextCursor)
			}
			break
		}
		if step.NextCursor == "" || step.NextCursor == cursor {
			t.Fatalf("call %d says more remain but hands back cursor %q (was %q)", calls, step.NextCursor, cursor)
		}
		cursor = step.NextCursor
	}

	// Four instances at two a call is two calls: `remaining` is exact, so a page that
	// happens to end on the last instance does not send the caller round once more.
	if calls != 2 {
		t.Errorf("walk took %d calls, want 2", calls)
	}
	if len(seen) != len(started) {
		t.Errorf("reported %d distinct instances, want all %d", len(seen), len(started))
	}
	for k, n := range seen {
		if !started[k] {
			t.Errorf("reported instance %d, which this test never started", k)
		}
		if n != 1 {
			t.Errorf("instance %d reported %d times, want once", k, n)
		}
	}
}

// An instance started on the source version while a drain is under way has a key above
// every cursor handed out so far, so the walk reaches it rather than leaving it behind.
func TestMigrateInstancesCursorReachesInstancesStartedDuringTheDrain(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2BPMN)
	keys := []uint64{startInstance(t, ts, v1), startInstance(t, ts, v1)}

	step := migrateBatchStep(t, ts, v1, v2, 1, "")
	if step.Migrated != 1 || !step.Remaining || step.NextCursor == "" {
		t.Fatalf("first step = %+v, want one migrated and a cursor to continue from", step)
	}
	keys = append(keys, startInstance(t, ts, v1)) // arrives mid-drain

	cursor := step.NextCursor
	for calls := 0; step.Remaining; calls++ {
		if calls > 4 {
			t.Fatalf("drain is not advancing (cursor %q)", cursor)
		}
		step = migrateBatchStep(t, ts, v1, v2, 1, cursor)
		cursor = step.NextCursor
	}
	for _, k := range keys {
		if got := instanceDefKey(t, ts, k); got != v2 {
			t.Errorf("instance %d is on def %d, want %d", k, got, v2)
		}
	}
}

// migrateV3BPMN is a third version, so a test can walk a *chain* of migrations: the
// definition in force between two hops is named by neither the instance record nor the
// first migration, only by the second one's source.
const migrateV3BPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="migrating" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="triage" name="Triage"/>
    <userTask id="verify" name="Verify"/>
    <userTask id="review" name="Review"/>
    <endEvent id="done"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="triage"/>
    <sequenceFlow id="fa" sourceRef="triage" targetRef="verify"/>
    <sequenceFlow id="f1" sourceRef="verify" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="done"/>
  </process>
</definitions>`
