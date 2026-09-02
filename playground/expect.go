package playground

import (
	"fmt"
	"sort"
	"time"
)

// Expectations are what a run has to show for a scenario to count as passed.
//
// They are the difference between a Playground somebody looks at and one that can
// be run by something that is not a person: a number on a screen needs a reader to
// judge it, a verdict does not. The same expectations that make the panel say
// "passed" make a CI job exit non-zero.
//
// Every field is optional and an omitted one is not checked, so a scenario asserts
// only what it means to assert. The two places where zero is itself a meaningful
// target — "no incidents at all", "this queue must never form" — are a pointer and
// a map entry rather than a plain number, because a zero value that silently
// asserts something is how an expectation ends up failing a run nobody aimed it at.
type Expectations struct {
	// MinCompleted is how many cases must reach an end event. It counts completions,
	// not starts: a run whose cases are all still parked has not completed them.
	MinCompleted int
	// MaxIncidents is how many tokens may be parked behind a simulated failure.
	// Nil is not checked; a pointer to zero is "none at all".
	MaxIncidents *int
	// MaxP50, MaxP90 and MaxDuration bound how long a case may take: the median for
	// the everyday case, p90 for the tail somebody actually waits through, and the
	// maximum for the promise that nothing takes longer than a day. Zero is not
	// checked — a bound of no time is not a thing anybody means.
	MaxP50, MaxP90, MaxDuration time.Duration
	// MinVisits and MaxVisits bound how many tokens passed through an element.
	// Coverage and outcome are the same statement about the same counter: "the
	// approval branch must be exercised" is a minimum of one, "the error branch must
	// stay rare" is a maximum.
	MinVisits, MaxVisits map[string]int64
	// MaxQueue bounds the longest queue a pool ever had — the capacity question
	// stated as a target rather than read off a table.
	MaxQueue map[string]int
	// Rules are the expectations stated per case rather than per run. They are the
	// statement the fields above cannot make: "the median is under four hours" is
	// true of a run, "an application under 50 000 from a grade-A customer is
	// approved" is true of a case, and a run that holds it nine times in ten is not
	// nine tenths right.
	//
	// They are judged from the cases, not from the report, so [Judge] takes their
	// outcomes rather than computing them: see [Sandbox.JudgeRules].
	Rules []Rule
}

// Check is one expectation and what the run did about it. Want and Got are
// sentences rather than numbers: a check is a statement about the run, and the
// thing that computes on it is the verdict's Passed.
type Check struct {
	Name   string
	Want   string
	Got    string
	Passed bool
	// Rule marks a check that came from a per-case rule rather than from a bound on
	// the run. Both decide the verdict alike; a reader is shown them differently,
	// because a rule's own breakdown says more than a check line has room for.
	Rule bool
}

// Verdict is a run judged against its expectations. A verdict with no checks
// passed: "I have not said what I expect yet" must not read as "the run is
// broken".
type Verdict struct {
	Passed bool
	Checks []Check
}

// Judge measures a report against the expectations. It is a pure function of the
// report — no sandbox, no clock — so a scenario's verdict can be recomputed from a
// stored run, and so the judging is testable without running anything.
//
// Every expectation is evaluated even once one has failed: a run that misses three
// targets should say so once, rather than over three runs.
func (e Expectations) Judge(rep Report, rules []RuleOutcome) Verdict {
	v := Verdict{Passed: true}
	addKind := func(name, want, got string, ok, rule bool) {
		v.Checks = append(v.Checks, Check{Name: name, Want: want, Got: got, Passed: ok, Rule: rule})
		if !ok {
			v.Passed = false
		}
	}
	add := func(name, want, got string, ok bool) { addKind(name, want, got, ok, false) }

	if e.MinCompleted > 0 {
		add("cases completed", fmt.Sprintf("at least %d", e.MinCompleted),
			fmt.Sprintf("%d of %d", rep.Completed, rep.Cases), rep.Completed >= e.MinCompleted)
	}
	if e.MaxIncidents != nil {
		add("incidents", fmt.Sprintf("at most %d", *e.MaxIncidents),
			fmt.Sprintf("%d", rep.Incidents), rep.Incidents <= *e.MaxIncidents)
	}
	for _, d := range []struct {
		name  string
		bound time.Duration
		got   time.Duration
	}{
		{"median duration", e.MaxP50, rep.Duration.P50},
		{"p90 duration", e.MaxP90, rep.Duration.P90},
		{"slowest case", e.MaxDuration, rep.Duration.Max},
	} {
		if d.bound > 0 {
			add(d.name, "at most "+readable(d.bound), readable(d.got), d.got <= d.bound)
		}
	}

	// The maps are walked in sorted order so two runs of one scenario read the same
	// way and a CI log diffs cleanly. Go randomises map iteration on purpose, and a
	// report whose lines move about between runs is one nobody can compare.
	for _, id := range sortedKeys(e.MinVisits) {
		// An element the run never reached has no counter at all, and that reads as
		// zero rather than as "not applicable": a coverage assertion is precisely
		// about the elements with no counter.
		got := rep.Visits[id]
		add("visits to "+id, fmt.Sprintf("at least %d", e.MinVisits[id]),
			fmt.Sprintf("%d", got), got >= e.MinVisits[id])
	}
	for _, id := range sortedKeys(e.MaxVisits) {
		got := rep.Visits[id]
		add("visits to "+id, fmt.Sprintf("at most %d", e.MaxVisits[id]),
			fmt.Sprintf("%d", got), got <= e.MaxVisits[id])
	}
	for _, name := range sortedKeys(e.MaxQueue) {
		// A bound on a pool the run does not have is a mistake in the scenario, not a
		// silent pass. It usually means the pool was renamed and the assertion was
		// left behind — exactly when an assertion must not go quiet.
		p, ok := rep.Pools[name]
		got := fmt.Sprintf("%d", p.MaxQueue)
		if !ok {
			got = "no such pool in this run"
		}
		add("queue at "+name, fmt.Sprintf("at most %d", e.MaxQueue[name]),
			got, ok && p.MaxQueue <= e.MaxQueue[name])
	}
	// The per-case rules last, in the order they were written: they read as
	// sentences rather than as bounds, and a reader looking for a number should not
	// have to step over them.
	for _, o := range rules {
		addKind(o.Rule.Label(), "every matching case", o.Got(), o.Passed(), true)
	}
	return v
}

// readable trims a simulated duration to what somebody reads in a build log.
// These come out of a virtual clock, so they carry nanoseconds nobody measured
// and nobody can act on: "8h26m41.615411361s" is a p90 that says less than
// "8h26m42s" does.
func readable(d time.Duration) string {
	if d >= time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Millisecond).String()
}

// sortedKeys is the map order every check list is built in.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
