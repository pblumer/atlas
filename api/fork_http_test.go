package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// forkPlanView is the fork half of a migration plan: where the successor would start,
// what could not be paired, and every reason the fork would be refused.
type forkPlanView struct {
	ToProcessDefKey uint64   `json:"toProcessDefKey"`
	ToVersion       int32    `json:"toVersion"`
	Resume          []string `json:"resume"`
	Candidates      []string `json:"candidates"`
	Parked          []struct {
		ElementID string `json:"elementId"`
		ResumeAt  string `json:"resumeAt"`
	} `json:"parked"`
	Variables   int  `json:"variables"`
	DataObjects int  `json:"dataObjects"`
	Jobs        int  `json:"jobs"`
	Forkable    bool `json:"forkable"`
	Problems    []struct {
		ElementID string `json:"elementId"`
		Reason    string `json:"reason"`
	} `json:"problems"`
	SuccessorInstanceKey uint64 `json:"successorInstanceKey"`
}

type planWithForkResp struct {
	Migratable bool          `json:"migratable"`
	Fork       *forkPlanView `json:"fork"`
	Problems   []struct {
		ElementID string `json:"elementId"`
		Reason    string `json:"reason"`
	} `json:"problems"`
}

func forkCall(t *testing.T, ts *httptest.Server, path, body string) (int, forkPlanView, []byte) {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodPost, path, body, "application/json")
	var p forkPlanView
	_ = json.Unmarshal(raw, &p)
	return code, p, raw
}

// linkedInstanceRow is the part of a listing row these assertions read: the lifecycle
// state, and the two ends of a fork link.
type linkedInstanceRow struct {
	Key                    uint64 `json:"key"`
	State                  string `json:"state"`
	ProcessDefKey          uint64 `json:"processDefKey"`
	PredecessorInstanceKey uint64 `json:"predecessorInstanceKey"`
	SuccessorInstanceKey   uint64 `json:"successorInstanceKey"`
}

// linkedRow reads one instance out of the search, whatever state it is in.
func linkedRow(t *testing.T, ts *httptest.Server, key uint64) linkedInstanceRow {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?q=%d", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("search instance %d: status=%d body=%s", key, code, body)
	}
	var rows []linkedInstanceRow
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("instance %d not found in %s", key, body)
	return linkedInstanceRow{}
}

// TestMigrationPlanOffersTheForkWhenRebindingCannotHold is the case the record exists
// for: the token's element is gone from the target version, so an in-place migration is
// refused — and the same plan answers what a fork would do instead, rather than leaving
// the operator with a refusal and no next step.
func TestMigrationPlanOffersTheForkWhenRebindingCannotHold(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2RenamedBPMN)
	piKey := startInstance(t, ts, v1)

	code, raw := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/migrate/plan", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d}`, v2), "application/json")
	if code != http.StatusOK {
		t.Fatalf("plan: status=%d body=%s", code, raw)
	}
	var plan planWithForkResp
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode plan: %v (%s)", err, raw)
	}
	if plan.Migratable {
		t.Fatalf("plan says the instance can be rebound; the fixture renamed the parked task: %s", raw)
	}
	if plan.Fork == nil {
		t.Fatalf("plan carries no fork alternative: %s", raw)
	}
	f := plan.Fork
	if f.ToProcessDefKey != v2 || f.ToVersion != 2 {
		t.Errorf("fork target = %d (v%d), want %d (v2)", f.ToProcessDefKey, f.ToVersion, v2)
	}
	// The parked token has no counterpart by id, so nothing is proposed and the fork is
	// not yet submittable — the operator has to say where the work picks up again.
	if f.Forkable {
		t.Errorf("fork is offered as submittable with no resume point: %+v", f)
	}
	if len(f.Parked) != 1 || f.Parked[0].ElementID != "review" || f.Parked[0].ResumeAt != "" {
		t.Errorf("parked tokens = %+v, want review unmatched", f.Parked)
	}
	// ...and the elements it could legitimately resume at are named, so the choice is
	// one an operator can make from the answer itself.
	if !hasString(f.Candidates, "review_v2") {
		t.Errorf("candidates = %v, want the renamed task among them", f.Candidates)
	}

	// Naming one turns the same plan into a fork that holds.
	code, chosen, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/plan", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"resume":["review_v2"]}`, v2))
	if code != http.StatusOK {
		t.Fatalf("plan with resume: status=%d body=%s", code, raw)
	}
	var withResume planWithForkResp
	if err := json.Unmarshal(raw, &withResume); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	_ = chosen
	if withResume.Fork == nil || !withResume.Fork.Forkable {
		t.Fatalf("fork with a named resume point is still refused: %s", raw)
	}
	if len(withResume.Fork.Resume) != 1 || withResume.Fork.Resume[0] != "review_v2" {
		t.Errorf("resume = %v, want [review_v2]", withResume.Fork.Resume)
	}
	// Planning writes nothing: the instance is still running, on its own version.
	if row := linkedRow(t, ts, piKey); row.State != "active" || row.SuccessorInstanceKey != 0 {
		t.Errorf("planning changed the instance: %+v", row)
	}
}

// TestForkEndsTheInstanceAndContinuesItInANewOne is the whole operation over HTTP: the
// predecessor ends, the successor runs the target version from the named element, and
// each row names the other so an operator can follow the work across.
func TestForkEndsTheInstanceAndContinuesItInANewOne(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2RenamedBPMN)
	piKey := startInstance(t, ts, v1)

	code, got, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"resume":["review_v2"],"reason":"v2 replaced the task this token sits on"}`, v2))
	if code != http.StatusOK {
		t.Fatalf("fork: status=%d body=%s", code, raw)
	}
	if got.SuccessorInstanceKey == 0 {
		t.Fatalf("fork answered without naming the new instance: %s", raw)
	}
	newKey := got.SuccessorInstanceKey

	old := linkedRow(t, ts, piKey)
	if old.State != "terminated" {
		t.Errorf("predecessor state = %q, want terminated", old.State)
	}
	if old.SuccessorInstanceKey != newKey {
		t.Errorf("predecessor names successor %d, want %d", old.SuccessorInstanceKey, newKey)
	}
	next := linkedRow(t, ts, newKey)
	if next.State != "active" || next.ProcessDefKey != v2 {
		t.Errorf("successor = %+v, want an active instance of %d", next, v2)
	}
	if next.PredecessorInstanceKey != piKey {
		t.Errorf("successor names predecessor %d, want %d", next.PredecessorInstanceKey, piKey)
	}
	// The successor picked the work up at the named element, not at the start event.
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?instance=%d", newKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "review_v2") {
		t.Errorf("successor has no task on review_v2: %s", body)
	}
}

// TestForkShowsOnBothReplays is the reader's half: an instance that was forked away
// stops mid-diagram, and one that continues another starts mid-diagram. Without the row
// and the link the first reads as a defect and the second as a ghost — which is why the
// record puts a fork on both timelines rather than only in the audit trail.
func TestForkShowsOnBothReplays(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2RenamedBPMN)
	piKey := startInstance(t, ts, v1)

	code, got, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"resume":["review_v2"],"reason":"the task was redrawn"}`, v2))
	if code != http.StatusOK {
		t.Fatalf("fork: status=%d body=%s", code, raw)
	}
	newKey := got.SuccessorInstanceKey

	type timeline struct {
		PredecessorInstanceKey uint64 `json:"predecessorInstanceKey"`
		SuccessorInstanceKey   uint64 `json:"successorInstanceKey"`
		Steps                  []struct {
			Action string `json:"action"`
			Fork   *struct {
				Direction   string `json:"direction"`
				InstanceKey uint64 `json:"instanceKey"`
				Version     int32  `json:"version"`
				Actor       string `json:"actor"`
				Reason      string `json:"reason"`
			} `json:"fork"`
		} `json:"steps"`
	}
	read := func(key uint64) timeline {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
		if code != http.StatusOK {
			t.Fatalf("timeline %d: status=%d body=%s", key, code, body)
		}
		var tl timeline
		if err := json.Unmarshal(body, &tl); err != nil {
			t.Fatalf("decode timeline: %v (%s)", err, body)
		}
		return tl
	}
	forkStep := func(tl timeline) *struct {
		Direction   string `json:"direction"`
		InstanceKey uint64 `json:"instanceKey"`
		Version     int32  `json:"version"`
		Actor       string `json:"actor"`
		Reason      string `json:"reason"`
	} {
		for _, st := range tl.Steps {
			if st.Action == "fork" {
				return st.Fork
			}
		}
		return nil
	}

	out := read(piKey)
	if out.SuccessorInstanceKey != newKey {
		t.Errorf("predecessor timeline names successor %d, want %d", out.SuccessorInstanceKey, newKey)
	}
	f := forkStep(out)
	if f == nil || f.Direction != "out" || f.InstanceKey != newKey || f.Reason != "the task was redrawn" {
		t.Fatalf("predecessor fork step = %+v, want an outgoing fork to %d with its reason", f, newKey)
	}
	if f.Version != 2 {
		t.Errorf("fork step names version %d, want the successor's v2", f.Version)
	}

	in := read(newKey)
	if in.PredecessorInstanceKey != piKey {
		t.Errorf("successor timeline names predecessor %d, want %d", in.PredecessorInstanceKey, piKey)
	}
	g := forkStep(in)
	if g == nil || g.Direction != "in" || g.InstanceKey != piKey {
		t.Fatalf("successor fork step = %+v, want an incoming fork from %d", g, piKey)
	}
}

// TestForkRefusesWithoutReasonOrAResumePointItCannotRun is the gate in front of the
// command. Each of these leaves the instance exactly as it was — which matters more here
// than anywhere else, because the operation it is refusing would have ended it.
func TestForkRefusesWithoutReasonOrAResumePointItCannotRun(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	v2 := deployXML(t, ts, migrateV2RenamedBPMN)

	t.Run("no reason", func(t *testing.T) {
		piKey := startInstance(t, ts, v1)
		code, _, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
			fmt.Sprintf(`{"targetProcessDefKey":%d,"resume":["review_v2"]}`, v2))
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s, want 400", code, raw)
		}
		if row := linkedRow(t, ts, piKey); row.State != "active" {
			t.Errorf("instance = %+v, want it untouched", row)
		}
	})

	t.Run("an element the target version does not have", func(t *testing.T) {
		piKey := startInstance(t, ts, v1)
		code, plan, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
			fmt.Sprintf(`{"targetProcessDefKey":%d,"resume":["nope"],"reason":"because"}`, v2))
		if code != http.StatusConflict {
			t.Fatalf("status=%d body=%s, want 409", code, raw)
		}
		if plan.Forkable || len(plan.Problems) == 0 {
			t.Errorf("refusal carries no problem: %s", raw)
		}
		if row := linkedRow(t, ts, piKey); row.State != "active" || row.SuccessorInstanceKey != 0 {
			t.Errorf("refused fork changed the instance: %+v", row)
		}
	})

	t.Run("a target that is not deployed", func(t *testing.T) {
		piKey := startInstance(t, ts, v1)
		code, plan, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
			`{"targetProcessDefKey":424242,"resume":["review_v2"],"reason":"because"}`)
		if code != http.StatusConflict {
			t.Fatalf("status=%d body=%s, want 409", code, raw)
		}
		if plan.Forkable || len(plan.Problems) == 0 {
			t.Errorf("refusal carries no problem: %s", raw)
		}
	})

	t.Run("an instance that is not running", func(t *testing.T) {
		code, raw := doReq(t, ts, http.MethodPost, "/api/v1/instances/424242/migrate/fork",
			fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"because"}`, v2), "application/json")
		if code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s, want 404", code, raw)
		}
	})

	t.Run("a key that is not a key", func(t *testing.T) {
		code, raw := doReq(t, ts, http.MethodPost, "/api/v1/instances/not-a-key/migrate/fork",
			fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"because"}`, v2), "application/json")
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s, want 400", code, raw)
		}
	})

	t.Run("no resume point at all", func(t *testing.T) {
		piKey := startInstance(t, ts, v1)
		code, plan, raw := forkCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey),
			fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"because"}`, v2))
		if code != http.StatusConflict {
			t.Fatalf("status=%d body=%s, want 409", code, raw)
		}
		if plan.Forkable {
			t.Errorf("a fork with nowhere to resume was accepted: %s", raw)
		}
		if row := linkedRow(t, ts, piKey); row.State != "active" {
			t.Errorf("refused fork changed the instance: %+v", row)
		}
	})
}

// hasString reports whether the list names s.
func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
