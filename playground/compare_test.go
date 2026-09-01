package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/playground"
)

// deltaFor finds one measure in a comparison, failing if it is missing: a measure
// that quietly dropped out would read as "nothing changed there".
func deltaFor(t *testing.T, c playground.Comparison, name string) playground.Delta {
	t.Helper()
	for _, d := range c.Deltas {
		if d.Name == name {
			return d
		}
	}
	names := make([]string, 0, len(c.Deltas))
	for _, d := range c.Deltas {
		names = append(names, d.Name)
	}
	t.Fatalf("the comparison has no %q; it has %v", name, names)
	return playground.Delta{}
}

// A comparison says which way each number moved *and* whether that is progress.
// Without the second half a reader has to know that more completions is good and
// a longer p90 is not, which is exactly the knowledge a report exists to supply.
func TestAComparisonKnowsWhichWayIsBetter(t *testing.T) {
	before, after := passing(), passing()
	after.Completed = 96                                                  // fewer finished: worse
	after.Duration.P90 = 90 * time.Minute                                 // faster tail: better
	after.Incidents = 2                                                   // more incidents: worse
	after.Pools["clerks"] = playground.PoolStat{Capacity: 4, MaxQueue: 3} // shorter queue: better

	c := playground.Compare(before, after)

	completed := deltaFor(t, c, "cases completed")
	if completed.Before != 100 || completed.After != 96 || !completed.Worse() {
		t.Errorf("completions = %+v, want 100 → 96 and worse", completed)
	}
	if p90 := deltaFor(t, c, "p90 duration"); !p90.Better() {
		t.Errorf("p90 fell from 2h to 1h30m and did not read as better: %+v", p90)
	}
	if inc := deltaFor(t, c, "incidents"); !inc.Worse() {
		t.Errorf("incidents rose from 0 to 2 and did not read as worse: %+v", inc)
	}
	if q := deltaFor(t, c, "queue at clerks"); !q.Better() {
		t.Errorf("the queue fell from 7 to 3 and did not read as better: %+v", q)
	}
}

// A measure that did not move is neither better nor worse. Reading "the same" as
// an improvement is how a comparison starts congratulating a change that did
// nothing.
func TestAnUnchangedMeasureIsNeither(t *testing.T) {
	c := playground.Compare(passing(), passing())
	for _, d := range c.Deltas {
		if d.Better() || d.Worse() {
			t.Errorf("%q moved between two identical reports: %+v", d.Name, d)
		}
	}
	if len(c.Deltas) == 0 {
		t.Error("two identical reports produced no measures at all")
	}
}

// Waiting time per element is the measure a capacity change is argued from, so
// the comparison carries it per element rather than only in the totals.
func TestWaitingTimeIsComparedPerElement(t *testing.T) {
	before, after := passing(), passing()
	before.Elements = map[string]playground.ElementStat{
		"approve": {Runs: 100, Wait: 10 * time.Hour, Work: time.Hour},
	}
	after.Elements = map[string]playground.ElementStat{
		"approve": {Runs: 100, Wait: 2 * time.Hour, Work: time.Hour},
	}

	d := deltaFor(t, playground.Compare(before, after), "waiting at approve")
	if !d.Better() {
		t.Errorf("waiting fell from ten hours to two and did not read as better: %+v", d)
	}
	if d.Before != (10*time.Hour).Milliseconds() || d.After != (2*time.Hour).Milliseconds() {
		t.Errorf("delta = %+v, want the two waiting times in milliseconds", d)
	}
	if d.Unit != playground.UnitDuration {
		t.Errorf("unit = %v, want a duration so a reader is not shown milliseconds", d.Unit)
	}
}

// A run that has an element or a pool the other does not is the interesting case:
// a branch the new data reached for the first time, or a pool that was removed.
// Leaving it out would hide exactly the change worth seeing.
func TestSomethingPresentInOnlyOneRunStillAppears(t *testing.T) {
	before, after := passing(), passing()
	before.Elements = map[string]playground.ElementStat{"approve": {Wait: time.Hour}}
	after.Elements = map[string]playground.ElementStat{"escalate": {Wait: 2 * time.Hour}}

	c := playground.Compare(before, after)
	gone := deltaFor(t, c, "waiting at approve")
	if gone.Before != time.Hour.Milliseconds() || gone.After != 0 {
		t.Errorf("an element only the first run had = %+v, want its time and then nothing", gone)
	}
	fresh := deltaFor(t, c, "waiting at escalate")
	if fresh.Before != 0 || fresh.After != (2*time.Hour).Milliseconds() {
		t.Errorf("an element only the second run had = %+v, want nothing and then its time", fresh)
	}
}

// The measures come back in a fixed order, so two comparisons of the same pair of
// runs read the same way and a CI log diffs cleanly.
func TestComparisonIsOrderedTheSameEveryTime(t *testing.T) {
	before, after := passing(), passing()
	before.Elements = map[string]playground.ElementStat{
		"zebra": {Wait: time.Hour}, "alpha": {Wait: time.Hour}, "mango": {Wait: time.Hour},
	}
	after.Elements = before.Elements

	first := playground.Compare(before, after)
	for i := 0; i < 20; i++ {
		got := playground.Compare(before, after)
		for j := range got.Deltas {
			if got.Deltas[j].Name != first.Deltas[j].Name {
				t.Fatalf("measure %d = %q on this pass and %q on the first", j, got.Deltas[j].Name, first.Deltas[j].Name)
			}
		}
	}
}

// Some numbers have no good direction, and a comparison must not invent one. A
// pool that fell from 99 % to 62 % has room now — or the work stopped arriving.
// Same number, opposite news, so it is shown and left uncoloured.
func TestAMeasureWithNoGoodDirectionIsNotJudged(t *testing.T) {
	before, after := passing(), passing()
	before.Pools["clerks"] = playground.PoolStat{Capacity: 3, BusyTime: 99 * time.Hour, Available: 100 * time.Hour}
	after.Pools["clerks"] = playground.PoolStat{Capacity: 5, BusyTime: 62 * time.Hour, Available: 100 * time.Hour}

	d := deltaFor(t, playground.Compare(before, after), "utilisation at clerks")
	if d.Before != 99 || d.After != 62 {
		t.Errorf("utilisation = %d → %d, want 99 → 62", d.Before, d.After)
	}
	if d.Better() || d.Worse() {
		t.Errorf("utilisation was judged: %+v — falling is good news or bad, and this cannot tell which", d)
	}
}
