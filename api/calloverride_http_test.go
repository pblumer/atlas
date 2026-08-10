package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

const childAltBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="childAlt" name="Child Alt" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// rowFor returns the call-activity row for a given caller element, failing if absent.
func rowFor(t *testing.T, rows []callActivityRow, elementID string) callActivityRow {
	t.Helper()
	for _, r := range rows {
		if r.ElementID == elementID {
			return r
		}
	}
	t.Fatalf("no call-activity row for element %q", elementID)
	return callActivityRow{}
}

// TestCallActivityOverrides exercises the per-server override lifecycle over HTTP
// (ADR-0105): redirect, pin, disable, and clear, each observed through the
// override-aware inventory, plus the validation errors.
func TestCallActivityOverrides(t *testing.T) {
	ts := newTestServer(t)

	// caller calls "child" (latest) and "missing"; deploy caller + child v1 + childAlt.
	mustDeploy := func(xml string) (uint64, int32) {
		t.Helper()
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy status = %d, body = %s", code, body)
		}
		var d struct {
			Key     uint64 `json:"key"`
			Version int32  `json:"version"`
		}
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatalf("decode deploy: %v (%s)", err, body)
		}
		return d.Key, d.Version
	}
	mustDeploy(callerBPMN)
	childV1Key, childV1Ver := mustDeploy(childBPMN)
	altKey, _ := mustDeploy(childAltBPMN)

	setOverride := func(pid, bodyJSON string) (int, []byte) {
		return doReq(t, ts, http.MethodPut, "/api/v1/call-activities/overrides/"+pid, bodyJSON, "application/json")
	}

	// Baseline: callChild resolves to child (latest = v1), no override.
	base := rowFor(t, getCallActivities(t, ts), "callChild")
	if !base.Resolved || base.CalledProcessID != "child" || base.Override != nil {
		t.Fatalf("baseline callChild = %+v, want resolved to child, no override", base)
	}

	// Redirect child → childAlt.
	if code, body := setOverride("child", `{"action":"redirect","targetProcessId":"childAlt"}`); code != http.StatusOK {
		t.Fatalf("set redirect status = %d, body = %s", code, body)
	}
	r := rowFor(t, getCallActivities(t, ts), "callChild")
	if r.Override == nil || r.Override.Action != "redirect" {
		t.Fatalf("after redirect, override = %+v, want action=redirect", r.Override)
	}
	if !r.Resolved || r.TargetKey != altKey || r.TargetName != "Child Alt" {
		t.Errorf("after redirect, callChild target = {%d %q}, want childAlt key %d", r.TargetKey, r.TargetName, altKey)
	}

	// Deploy child v2 so latest floats to v2; then pin child → v1.
	_, childV2Ver := mustDeploy(childBPMN)
	if childV2Ver != childV1Ver+1 {
		t.Fatalf("child redeploy version = %d, want %d", childV2Ver, childV1Ver+1)
	}
	if code, body := setOverride("child", `{"action":"pin","targetVersion":1}`); code != http.StatusOK {
		t.Fatalf("set pin status = %d, body = %s", code, body)
	}
	r = rowFor(t, getCallActivities(t, ts), "callChild")
	if r.Override == nil || r.Override.Action != "pin" || r.Override.TargetVersion != 1 {
		t.Fatalf("after pin, override = %+v, want action=pin version=1", r.Override)
	}
	if !r.Resolved || r.TargetKey != childV1Key || r.TargetVersion != 1 {
		t.Errorf("after pin, callChild target = {key %d v%d}, want child v1 key %d", r.TargetKey, r.TargetVersion, childV1Key)
	}

	// Disable: the call would park; row reports the override and no resolution.
	if code, body := setOverride("child", `{"action":"disable"}`); code != http.StatusOK {
		t.Fatalf("set disable status = %d, body = %s", code, body)
	}
	r = rowFor(t, getCallActivities(t, ts), "callChild")
	if r.Override == nil || r.Override.Action != "disable" || r.Resolved {
		t.Errorf("after disable, row = %+v, want override=disable and resolved=false", r)
	}

	// Clear: back to default latest (v2 now).
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/call-activities/overrides/child", "", ""); code != http.StatusNoContent {
		t.Fatalf("delete override status = %d, body = %s", code, body)
	}
	r = rowFor(t, getCallActivities(t, ts), "callChild")
	if r.Override != nil || !r.Resolved || r.TargetVersion != childV2Ver {
		t.Errorf("after clear, callChild = %+v, want no override, resolved to v%d", r, childV2Ver)
	}

	// Validation: redirect needs a target; pin needs an existing version; action must be known.
	for _, tc := range []struct {
		name, body string
	}{
		{"malformed json", `{not json`},
		{"redirect without target", `{"action":"redirect"}`},
		{"pin to missing version", `{"action":"pin","targetVersion":99}`},
		{"unknown action", `{"action":"teleport"}`},
	} {
		if code, _ := setOverride("child", tc.body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, code)
		}
	}
}
