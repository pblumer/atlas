package playground

import (
	"net/http"
	"strings"
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

// The seed is published so a caller can write it down and repeat the run. A
// number a JSON client cannot carry back exactly is not a seed anybody can use:
// a clock in nanoseconds is around 1.8e18, past the 2^53 a double holds without
// rounding, so every browser that read one back got a different number — and a
// scenario saved as reproducible came back with different figures.
func TestAGeneratedSeedSurvivesAJSONRoundTrip(t *testing.T) {
	svc := newService(t)
	const maxExact = int64(1)<<53 - 1

	for i := 0; i < 5; i++ {
		var sess sessionResp
		decodeInto(t, call(t, svc.HandleOpen, http.MethodPost,
			`{"source":"xml","xml":`+jsonString(userTaskXML)+`}`, nil), &sess)
		if sess.Seed <= 0 || sess.Seed > maxExact {
			t.Fatalf("seed = %d, want a positive number a JSON client carries exactly (at most %d)", sess.Seed, maxExact)
		}
		// The round trip a browser makes: through a float64 and back.
		if back := int64(float64(sess.Seed)); back != sess.Seed {
			t.Errorf("seed %d came back as %d after a trip through a JSON number", sess.Seed, back)
		}
		// Released before the next one: a registry bounds how many sandboxes may
		// exist at once, and this test is about the seed, not about that bound.
		call(t, svc.HandleClose, http.MethodDelete, "", map[string]string{"id": sess.ID})
	}

	// A seed the caller names is used as given: it is their number to choose, and
	// clamping it would silently run something other than what they asked for.
	var pinned sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost,
		`{"source":"xml","xml":`+jsonString(userTaskXML)+`,"seed":4711}`, nil), &pinned)
	if pinned.Seed != 4711 {
		t.Errorf("seed = %d, want the 4711 the caller asked for", pinned.Seed)
	}
}

// A malformed body is refused rather than read as an empty one. "I sent
// expectations and they were ignored" and "I sent none" must not look alike: the
// first is a scenario that silently checks nothing.
func TestAMalformedJudgementOrComparisonIsRefused(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}
	call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{"n":1}]}`, vals)
	waitForRun(t, svc, vals["id"], "finished")

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"expectations", svc.HandleVerdict},
		{"a baseline", svc.HandleCompare},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := call(t, tc.handler, http.MethodPost, `{not json`, vals); rec.Code != http.StatusBadRequest {
				t.Errorf("code = %d, want 400; body %s", rec.Code, rec.Body)
			}
		})
	}
}

// A rule is the expectation a run-wide bound cannot state: "the median is under
// four hours" is true of a run, "a small application is paid out" is true of a
// case, and a run that holds it nine times in ten is not nine tenths right.
func TestARunIsJudgedCaseByCaseAgainstItsRules(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"betrag":900},{"betrag":900},{"betrag":40000}],"arrival":{"mode":"allAtOnce"}}`, vals)
	waitForRun(t, svc, id, "finished")

	var v verdictResp
	decodeInto(t, call(t, svc.HandleVerdict, http.MethodPost, `{"rules":[
		{"name":"small ones finish","when":"betrag < 1000","then":"end = \"end\""},
		{"when":"betrag > 1000","then":"end = \"nowhere\""}
	]}`, vals), &v)

	if len(v.Rules) != 2 {
		t.Fatalf("rules = %d, want one outcome per rule: %+v", len(v.Rules), v.Rules)
	}
	held := v.Rules[0]
	if held.Name != "small ones finish" || held.Matched != 2 || held.Satisfied != 2 || !held.Passed {
		t.Errorf("the true rule = %+v, want two matched and both holding", held)
	}
	broken := v.Rules[1]
	if broken.Matched != 1 || broken.Violated != 1 || broken.Passed {
		t.Errorf("the false rule = %+v, want one matched and broken", broken)
	}
	// It names the offending case by its place in the dataset, so the panel can mark
	// the row and a reader can go and look at it.
	if len(broken.Examples) != 1 || broken.Examples[0] != 2 {
		t.Errorf("examples = %v, want the third case", broken.Examples)
	}
	// And the verdict as a whole fails, so one thing decides whether a build is red.
	if v.Passed {
		t.Error("the verdict passed with a broken rule in it")
	}
	if v.Rules[0].Examples == nil {
		t.Error("a rule nothing broke sent null instead of an empty list; every client here iterates it")
	}
}

// A rule that will not compile is the caller's mistake, named as such. The
// alternative is a rule that silently matches nothing for the rest of the run.
func TestARuleThatWillNotCompileIsRefusedByName(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}
	call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{"betrag":1}]}`, vals)
	waitForRun(t, svc, vals["id"], "finished")

	rec := call(t, svc.HandleVerdict, http.MethodPost, `{"rules":[{"when":"betrag <","then":"true"}]}`, vals)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not an expression") {
		t.Errorf("body %s does not say what is wrong with the rule", rec.Body)
	}
}

// A verdict with no rules still carries the field, because the panel and the
// runner both read it. Null would be a client crash, not an empty list.
func TestAVerdictWithNoRulesStillCarriesTheList(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}
	call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{"betrag":1}]}`, vals)
	waitForRun(t, svc, vals["id"], "finished")

	rec := call(t, svc.HandleVerdict, http.MethodPost, `{"minCompleted":1}`, vals)
	if !strings.Contains(rec.Body.String(), `"rules":[]`) {
		t.Errorf("body %s does not carry an empty rule list", rec.Body)
	}
}
