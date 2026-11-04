package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestMigrationPlanSaysWhatTheInstanceMeets: before moving an instance, an operator
// needs to know what the target version changes, and above all where the instance
// stands — a changed task it waits on is the change it meets first. The plan names the
// instance's positions and carries the comparison of the two versions
// (ADR-draft-compare-process-versions).
func TestMigrationPlanSaysWhatTheInstanceMeets(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	// v2 inserts triage before review, as in the other migration tests, and renames the
	// task the instance waits on.
	v2 := deployXML(t, ts, strings.Replace(migrateV2BPMN, `<userTask id="review" name="Review"/>`,
		`<userTask id="review" name="Review carefully"/>`, 1))
	piKey := startInstance(t, ts, v1)

	code, raw := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/migrate/plan", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2), "application/json")
	if code != http.StatusOK {
		t.Fatalf("plan: status=%d body=%s", code, raw)
	}
	var plan struct {
		Migratable bool             `json:"migratable"`
		At         []string         `json:"at"`
		Changes    []comparedChange `json:"changes"`
		Counts     *struct {
			Added, Removed, Changed int
		} `json:"counts"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode plan: %v (%s)", err, raw)
	}
	if !plan.Migratable {
		t.Fatalf("plan = %s, want a migratable instance", raw)
	}
	if !reflect.DeepEqual(plan.At, []string{"review"}) {
		t.Fatalf("at = %v, want the task the instance waits on and not the process", plan.At)
	}
	// f1 now leaves triage rather than start.
	want := strings.Join([]string{
		`added userTask triage`,
		`changed userTask review [changed name: "Review" -> "Review carefully"]`,
		`added sequenceFlow f0`,
		`changed sequenceFlow f1 [changed sourceRef: "start" -> "triage"]`,
	}, "\n")
	if got := summarize(plan.Changes); got != want {
		t.Fatalf("changes:\n%s\nwant\n%s", got, want)
	}
	if plan.Counts == nil || plan.Counts.Added != 2 || plan.Counts.Changed != 2 {
		t.Fatalf("counts = %+v, want 2 added, 2 changed", plan.Counts)
	}
}

// TestABatchRefusalSaysWhereTheInstanceStands: the batch outcome lists the instances
// that stayed behind, and each refusal now says where that instance is — the plan's
// positions — without the comparison, which a batch would repeat for every instance.
func TestABatchRefusalSaysWhereTheInstanceStands(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2RenamedBPMN)
	startInstance(t, ts, v1)

	code, raw := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", v1),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"test"}`, v2), "application/json")
	if code != http.StatusOK {
		t.Fatalf("batch: status=%d body=%s", code, raw)
	}
	var resp struct {
		Refused []struct {
			At      []string          `json:"at"`
			Changes []json.RawMessage `json:"changes"`
		} `json:"refused"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(resp.Refused) != 1 || !reflect.DeepEqual(resp.Refused[0].At, []string{"review"}) || resp.Refused[0].Changes != nil {
		t.Fatalf("refused = %s, want one refusal at review and no comparison", raw)
	}
}
