package playground_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/playground"
)

// passing is a report that satisfies everything the tests below ask of it, so a
// case can change one thing and say what that one thing did.
func passing() playground.Report {
	return playground.Report{
		Cases: 100, Completed: 100, Incidents: 0,
		Duration: playground.Durations{
			Count: 100, Min: time.Minute, P50: time.Hour, P90: 2 * time.Hour, Max: 3 * time.Hour,
		},
		Visits: map[string]int64{"start": 100, "approve": 100, "reject": 4, "end": 100},
		Pools:  map[string]playground.PoolStat{"clerks": {Capacity: 3, MaxQueue: 7}},
	}
}

// checkFor finds one check by the name it reports itself under, and fails if the
// verdict does not carry it: a check that quietly went missing would look like a
// pass.
func checkFor(t *testing.T, v playground.Verdict, contains string) playground.Check {
	t.Helper()
	for _, c := range v.Checks {
		if strings.Contains(c.Name, contains) {
			return c
		}
	}
	t.Fatalf("the verdict carries no check about %q; it has %+v", contains, v.Checks)
	return playground.Check{}
}

// An empty expectation asks nothing, so it judges nothing — and a verdict with no
// checks is a pass, not a failure. Anything else would make "I have not said what
// I expect yet" read as "the run is broken".
func TestNoExpectationsIsAPass(t *testing.T) {
	v := playground.Expectations{}.Judge(passing())
	if !v.Passed || len(v.Checks) != 0 {
		t.Errorf("verdict = %+v, want a pass with nothing checked", v)
	}
}

// Each expectation is one check, and the verdict fails if any of them does. The
// checks are all evaluated either way: a run that misses three targets should say
// so once, not three times over three runs.
func TestEveryFailureIsReportedAtOnce(t *testing.T) {
	rep := passing()
	rep.Completed = 90
	rep.Incidents = 3
	rep.Duration.P90 = 5 * time.Hour

	incidents := 0
	v := playground.Expectations{
		MinCompleted: 100,
		MaxIncidents: &incidents,
		MaxP90:       2 * time.Hour,
	}.Judge(rep)

	if v.Passed {
		t.Error("a run that missed three targets passed")
	}
	if len(v.Checks) != 3 {
		t.Fatalf("checks = %d, want all three evaluated: %+v", len(v.Checks), v.Checks)
	}
	for _, c := range v.Checks {
		if c.Passed {
			t.Errorf("check %q passed, want all three failed", c.Name)
		}
	}
}

// Completion is counted against the cases that reached an end event, not against
// the ones that were started: a run whose cases are all still parked has not
// completed them.
func TestCompletionIsJudged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		completed int
		min       int
		want      bool
	}{
		{"all of them", 100, 100, true},
		{"more than asked", 100, 90, true},
		{"one short", 99, 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := passing()
			rep.Completed = tc.completed
			v := playground.Expectations{MinCompleted: tc.min}.Judge(rep)
			if v.Passed != tc.want {
				t.Errorf("passed = %v, want %v (%+v)", v.Passed, tc.want, v.Checks)
			}
		})
	}
}

// Zero incidents is a thing somebody means, so it has to be sayable. An omitted
// maximum is not the same as a maximum of zero, which is why the field is a
// pointer rather than a number whose zero value would silently assert something.
func TestZeroIncidentsIsSayableAndOmittingItIsNot(t *testing.T) {
	rep := passing()
	rep.Incidents = 1

	none := 0
	if v := (playground.Expectations{MaxIncidents: &none}).Judge(rep); v.Passed {
		t.Error("one incident passed a run that demanded none")
	}
	if v := (playground.Expectations{}).Judge(rep); !v.Passed {
		t.Error("a run with an incident failed an expectation that never mentioned incidents")
	}
}

// The duration bounds are the SLA: the median for the everyday case, p90 for the
// tail somebody actually waits through, and the maximum for the promise that
// nothing takes longer than a day.
func TestDurationBoundsAreJudgedSeparately(t *testing.T) {
	rep := passing()
	rep.Duration.P50 = 90 * time.Minute

	v := playground.Expectations{MaxP50: time.Hour, MaxP90: 4 * time.Hour}.Judge(rep)
	if v.Passed {
		t.Error("a median over its bound passed")
	}
	if c := checkFor(t, v, "median"); c.Passed {
		t.Errorf("the median check passed: %+v", c)
	}
	if c := checkFor(t, v, "p90"); !c.Passed {
		t.Errorf("the p90 check failed, but p90 was inside its bound: %+v", c)
	}
}

// Visit bounds are how a scenario asserts coverage and outcome at once: "the
// approval branch must be exercised" and "the reject branch must stay rare" are
// the same kind of statement about the same counter.
func TestVisitBoundsAssertCoverageAndOutcome(t *testing.T) {
	rep := passing()

	v := playground.Expectations{
		MinVisits: map[string]int64{"approve": 1, "escalate": 1},
		MaxVisits: map[string]int64{"reject": 10},
	}.Judge(rep)
	if v.Passed {
		t.Error("a run that never reached \"escalate\" passed a scenario that demanded it")
	}
	if c := checkFor(t, v, "approve"); !c.Passed {
		t.Errorf("approve was visited a hundred times and its check failed: %+v", c)
	}
	// An element the run never reached has no counter at all, and that has to read
	// as zero rather than as "not applicable" — a coverage assertion is precisely
	// about the elements with no counter.
	if c := checkFor(t, v, "escalate"); c.Passed || !strings.Contains(c.Got, "0") {
		t.Errorf("the missing element's check = %+v, want a failure reporting zero", c)
	}
	if c := checkFor(t, v, "reject"); !c.Passed {
		t.Errorf("four visits failed a bound of ten: %+v", c)
	}
}

// A queue bound is the capacity question stated as a target: three clerks are
// enough if the queue in front of them never got past ten.
func TestQueueBoundsAreJudgedPerPool(t *testing.T) {
	rep := passing()

	if v := (playground.Expectations{MaxQueue: map[string]int{"clerks": 10}}).Judge(rep); !v.Passed {
		t.Errorf("a queue of seven failed a bound of ten: %+v", v.Checks)
	}
	v := playground.Expectations{MaxQueue: map[string]int{"clerks": 5}}.Judge(rep)
	if v.Passed {
		t.Error("a queue of seven passed a bound of five")
	}
	// A bound on a pool the run does not have is a mistake in the scenario, not a
	// silent pass: it usually means the pool was renamed and the assertion was left
	// behind, which is exactly when an assertion must not go quiet.
	missing := playground.Expectations{MaxQueue: map[string]int{"typists": 5}}.Judge(rep)
	if missing.Passed {
		t.Error("a bound on a pool that does not exist passed")
	}
	if c := checkFor(t, missing, "typists"); !strings.Contains(c.Got, "no such pool") {
		t.Errorf("check = %+v, want it to say the pool is not there", c)
	}
}

// The checks come back in a fixed order however the maps were built, so two runs
// of the same scenario read the same way and a CI log diffs cleanly.
func TestChecksAreOrderedTheSameEveryTime(t *testing.T) {
	e := playground.Expectations{
		MinVisits: map[string]int64{"zebra": 1, "alpha": 1, "mango": 1},
		MaxQueue:  map[string]int{"z-pool": 100, "a-pool": 100},
	}
	rep := passing()
	rep.Visits["zebra"], rep.Visits["alpha"], rep.Visits["mango"] = 1, 1, 1
	rep.Pools["z-pool"] = playground.PoolStat{}
	rep.Pools["a-pool"] = playground.PoolStat{}

	first := e.Judge(rep)
	for i := 0; i < 20; i++ {
		got := e.Judge(rep)
		for j := range got.Checks {
			if got.Checks[j].Name != first.Checks[j].Name {
				t.Fatalf("check %d = %q on this pass and %q on the first", j, got.Checks[j].Name, first.Checks[j].Name)
			}
		}
	}
}

// A simulated duration carries nanoseconds nobody measured, and a build log is
// the wrong place to print them: a p90 of "8h26m41.615411361s" says less than
// "8h26m42s" does.
func TestDurationsAreReportedAtAReadablePrecision(t *testing.T) {
	rep := passing()
	rep.Duration.P90 = 8*time.Hour + 26*time.Minute + 41*time.Second + 615411361*time.Nanosecond

	v := playground.Expectations{MaxP90: 72 * time.Hour}.Judge(rep)
	c := checkFor(t, v, "p90")
	if c.Got != "8h26m42s" {
		t.Errorf("got = %q, want the same duration without the noise", c.Got)
	}
	if c.Want != "at most 72h0m0s" {
		t.Errorf("want = %q, want the bound rendered the same way", c.Want)
	}
	// Under a minute the seconds are the signal, so they stay.
	rep.Duration.P50 = 1500 * time.Millisecond
	fast := checkFor(t, playground.Expectations{MaxP50: time.Minute}.Judge(rep), "median")
	if fast.Got != "1.5s" {
		t.Errorf("a short duration = %q, want its seconds kept", fast.Got)
	}
}
