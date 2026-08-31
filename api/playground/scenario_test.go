package playground

import (
	"net/http"
	"testing"
)

// A run that judges itself is the difference between a Playground somebody looks
// at and one a CI job can exit on. The same report passes one set of expectations
// and fails a stricter one without being run again.
func TestARunIsJudgedAgainstItsExpectations(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"n":1},{"n":2},{"n":3}],"arrival":{"mode":"allAtOnce"}}`, vals)
	waitForRun(t, svc, id, "finished")

	// One clerk, three one-hour cases: all three finish, the last after three hours.
	var pass verdictResp
	decodeInto(t, call(t, svc.HandleVerdict, http.MethodPost,
		`{"minCompleted":3,"maxIncidents":0,"maxMillis":10800000,"minVisits":{"approve":3}}`, vals), &pass)
	if !pass.Passed {
		t.Errorf("a run that met every target failed: %+v", pass.Checks)
	}
	if len(pass.Checks) != 4 {
		t.Errorf("checks = %d, want one per expectation: %+v", len(pass.Checks), pass.Checks)
	}

	var fail verdictResp
	decodeInto(t, call(t, svc.HandleVerdict, http.MethodPost,
		`{"minCompleted":4,"maxMillis":60000}`, vals), &fail)
	if fail.Passed {
		t.Error("a run of three cases passed a demand for four")
	}
	for _, c := range fail.Checks {
		if c.Passed {
			t.Errorf("check %q passed: %+v", c.Name, c)
		}
		if c.Want == "" || c.Got == "" {
			t.Errorf("check %q says neither what it wanted nor what it got: %+v", c.Name, c)
		}
	}
}

// An empty body asks nothing, and asking nothing is a pass. A Playground where
// the verdict starts out red would train everybody to ignore it.
func TestAVerdictWithNoExpectationsPasses(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}
	call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{"n":1}]}`, vals)
	waitForRun(t, svc, id, "finished")

	var v verdictResp
	decodeInto(t, call(t, svc.HandleVerdict, http.MethodPost, "", vals), &v)
	if !v.Passed || len(v.Checks) != 0 {
		t.Errorf("verdict = %+v, want a pass with nothing checked", v)
	}
}

// The comparison takes the earlier run in the request rather than looking it up:
// this service holds no design-time state, so the client that saved the baseline
// is the one that hands it back.
func TestARunIsComparedAgainstABaselineSentWithTheRequest(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"n":1},{"n":2},{"n":3}],"arrival":{"mode":"allAtOnce"}}`, vals)
	waitForRun(t, svc, vals["id"], "finished")

	// A baseline in which the same three cases were slower and one never finished.
	var cmp comparisonResp
	decodeInto(t, call(t, svc.HandleCompare, http.MethodPost, `{"baseline":{
		"cases":3,"completed":2,"incidents":1,"maxInFlight":3,
		"duration":{"count":2,"minMillis":3600000,"p50Millis":7200000,"p90Millis":14400000,"maxMillis":14400000,"meanMillis":7200000},
		"elements":{"approve":{"runs":3,"workMillis":10800000,"waitMillis":36000000,"maxWaitMillis":14400000}},
		"pools":{"clerks":{"capacity":1,"served":3,"busyMillis":10800000,"availableMillis":10800000,"maxQueue":9,"utilisationPercent":100}}
	}}`, vals), &cmp)

	byName := map[string]deltaResp{}
	for _, d := range cmp.Deltas {
		byName[d.Name] = d
	}
	if d := byName["cases completed"]; d.Before != 2 || d.After != 3 || !d.Better {
		t.Errorf("completions = %+v, want 2 → 3 and better", d)
	}
	if d := byName["incidents"]; d.Before != 1 || d.After != 0 || !d.Better {
		t.Errorf("incidents = %+v, want 1 → 0 and better", d)
	}
	if d := byName["waiting at approve"]; !d.Better || d.Unit != "millis" {
		t.Errorf("waiting = %+v, want less of it, in milliseconds", d)
	}
	if d := byName["queue at clerks"]; d.Before != 9 || d.After != 2 || !d.Better {
		t.Errorf("queue = %+v, want 9 → 2 and better", d)
	}
	// Utilisation moved but has no good direction, so it is shown uncoloured.
	if d := byName["utilisation at clerks"]; d.Better || d.Worse {
		t.Errorf("utilisation was judged: %+v", d)
	}
}

// A scenario is the three requests that make a run. Refusing a malformed one at
// save time is the difference between a stored scenario that fails now, with a
// reason, and one that fails in CI at three in the morning.
func TestAScenarioIsCheckedForShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec Scenario
		ok   bool
	}{
		{"the two required requests", Scenario{Open: []byte(`{"source":"draft"}`), Run: []byte(`{"cases":[]}`)}, true},
		{"with expectations too", Scenario{
			Open: []byte(`{"source":"draft"}`), Run: []byte(`{"cases":[]}`), Expect: []byte(`{"minCompleted":1}`),
		}, true},
		{"no open request", Scenario{Run: []byte(`{"cases":[]}`)}, false},
		{"no run request", Scenario{Open: []byte(`{"source":"draft"}`)}, false},
		{"a request that is not an object", Scenario{
			Open: []byte(`{"source":"draft"}`), Run: []byte(`"a string"`),
		}, false},
		{"expectations that are not an object", Scenario{
			Open: []byte(`{"source":"draft"}`), Run: []byte(`{}`), Expect: []byte(`[1,2,3]`),
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if tc.ok && err != nil {
				t.Errorf("a valid scenario was refused: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("a malformed scenario was accepted")
			}
		})
	}
}
