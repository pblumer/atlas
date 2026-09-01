package playground

// Unit says what a delta's numbers are, so the thing rendering them does not have
// to guess whether 7200000 is a count or two hours.
type Unit int

const (
	// UnitCount is a plain number of things: cases, incidents, a queue length.
	UnitCount Unit = iota
	// UnitDuration is milliseconds.
	UnitDuration
	// UnitPercent is a whole-number percentage.
	UnitPercent
)

// Delta is one measure of a run set beside the same measure of another.
//
// It carries the raw numbers rather than rendered text, so the caller formats
// them the way the rest of its screen does — and it carries which direction is
// good, which is the half that makes a comparison readable. A reader should not
// have to know that more completions is progress and a longer p90 is not; that is
// what the report is for.
type Delta struct {
	Name          string
	Unit          Unit
	Before, After int64
	// HigherIsBetter is the polarity of this measure, not a judgement about this
	// particular pair of runs — Better and Worse apply it to the numbers.
	HigherIsBetter bool
	// Neutral marks a measure that moved without that being good or bad, so it is
	// shown and not coloured. Not every number has a direction, and inventing one
	// for the sake of a green arrow is how a report starts misleading people.
	Neutral bool
}

// Better reports whether the measure moved the good way. A measure that did not
// move is neither better nor worse: reading "the same" as an improvement is how a
// comparison starts congratulating a change that did nothing.
func (d Delta) Better() bool {
	if d.Neutral || d.After == d.Before {
		return false
	}
	return (d.After > d.Before) == d.HigherIsBetter
}

// Worse reports whether the measure moved the bad way.
func (d Delta) Worse() bool { return !d.Neutral && d.After != d.Before && !d.Better() }

// Comparison is one run set beside another — the answer to "did that change
// help?", which a single report cannot give however complete it is.
type Comparison struct{ Deltas []Delta }

// Compare measures one report against another. Like [Expectations.Judge] it is a
// pure function of the two reports, so a stored run can be compared with a fresh
// one without either sandbox still existing.
//
// Anything present in only one of the runs is still listed, with zero on the side
// that lacks it: a branch the new data reached for the first time, or a pool that
// was removed, is exactly the change worth seeing.
func Compare(before, after Report) Comparison {
	c := Comparison{}
	add := func(name string, u Unit, b, a int64, higherIsBetter bool) {
		c.Deltas = append(c.Deltas, Delta{
			Name: name, Unit: u, Before: b, After: a, HigherIsBetter: higherIsBetter,
		})
	}
	neutral := func(name string, u Unit, b, a int64) {
		c.Deltas = append(c.Deltas, Delta{Name: name, Unit: u, Before: b, After: a, Neutral: true})
	}

	add("cases completed", UnitCount, int64(before.Completed), int64(after.Completed), true)
	add("incidents", UnitCount, int64(before.Incidents), int64(after.Incidents), false)
	add("median duration", UnitDuration, before.Duration.P50.Milliseconds(), after.Duration.P50.Milliseconds(), false)
	add("p90 duration", UnitDuration, before.Duration.P90.Milliseconds(), after.Duration.P90.Milliseconds(), false)
	add("slowest case", UnitDuration, before.Duration.Max.Milliseconds(), after.Duration.Max.Milliseconds(), false)
	add("peak in flight", UnitCount, int64(before.MaxInFlight), int64(after.MaxInFlight), false)

	// Sorted, so two comparisons of one pair read the same way and a CI log diffs
	// cleanly. Go randomises map iteration on purpose.
	for _, id := range unionKeys(before.Elements, after.Elements) {
		add("waiting at "+id, UnitDuration,
			before.Elements[id].Wait.Milliseconds(), after.Elements[id].Wait.Milliseconds(), false)
	}
	for _, name := range unionKeys(before.Pools, after.Pools) {
		add("queue at "+name, UnitCount,
			int64(before.Pools[name].MaxQueue), int64(after.Pools[name].MaxQueue), false)
		// Utilisation has no good direction. A pool that fell from 99 % to 62 % has
		// room now, which is what somebody adding a seat wanted; one that fell
		// because the work stopped arriving is the same number and not a win. So it
		// is reported and left unjudged, rather than given a colour it cannot earn.
		neutral("utilisation at "+name, UnitPercent,
			utilisationPercent(before.Pools[name]), utilisationPercent(after.Pools[name]))
	}
	return c
}

// unionKeys is every key of either map, in sorted order.
func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	return sortedKeys(seen)
}

// utilisationPercent is a pool's seat time over the seat time its calendar
// offered, as a whole number — the same fraction the report's own pool table
// shows, computed here so a comparison of two runs does not have to be handed it.
func utilisationPercent(p PoolStat) int64 {
	if p.Available <= 0 {
		return 0
	}
	return int64(100 * p.BusyTime / p.Available)
}
