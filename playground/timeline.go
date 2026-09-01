package playground

import "time"

// timelineBuckets is how many slices a run's timeline is cut into. It is a fixed
// number rather than a fixed width because a run's span is not known until it has
// happened: sixty buckets draw a legible chart of an afternoon and of a
// simulated quarter alike, and sixty of anything is small enough to send whole.
const timelineBuckets = 60

// Bucket is one slice of simulated time.
type Bucket struct {
	// At is when the slice begins; it is Width long.
	At time.Time
	// Started and Completed are the cases that arrived and finished inside it.
	Started, Completed int
	// InFlight is how many cases were still running at its end — arrivals minus
	// completions up to here, which is the work in progress a reader compares
	// against the arrival rate.
	InFlight int
}

// Timeline is the run over simulated time. Totals say what a run cost; this says
// when — that the queue built up on Tuesday morning and drained by Thursday is
// not a thing a mean and a p90 can tell anybody.
//
// It is derived from the cases' own start and end instants rather than sampled
// during the run, so it costs no run-time accounting and cannot drift from the
// records it is folded out of.
type Timeline struct {
	Width   time.Duration
	Buckets []Bucket

	// start and span are the layout the indexing needs, kept out of the wire shape
	// because a reader of the report has the buckets' own timestamps.
	start, span int64
}

// newTimeline lays out the buckets for a run spanning [start, end] in unix
// nanoseconds. A run that took no simulated time gets a single bucket: an
// instant is a legitimate span, and cutting it into sixty parts is not.
func newTimeline(start, end int64, n int) *Timeline {
	if n < 1 {
		n = 1
	}
	span := end - start
	if span <= 0 {
		return &Timeline{Buckets: []Bucket{{At: time.Unix(0, start).UTC()}}}
	}
	// Width is the nominal slice — what a chart draws each bar as. The boundaries
	// themselves are computed from the span rather than by adding Width up, so the
	// rounding the division leaves is spread across the run instead of piling up
	// at the end, and the last bucket always contains the run's final instant.
	tl := &Timeline{Width: time.Duration(span) / time.Duration(n), Buckets: make([]Bucket, n)}
	for i := range tl.Buckets {
		tl.Buckets[i].At = time.Unix(0, start+int64(i)*span/int64(n)).UTC()
	}
	tl.start, tl.span = start, span
	return tl
}

// add records one case's arrival, and its completion when it had one.
func (t *Timeline) add(created int64, completed bool, completedAt int64) {
	t.Buckets[t.index(created)].Started++
	if completed {
		t.Buckets[t.index(completedAt)].Completed++
	}
}

// index is the bucket an instant falls in. The run's very last instant belongs to
// the last bucket rather than to one past the end: a case that finishes exactly
// when the run does finished *in* the run.
func (t *Timeline) index(at int64) int {
	if t.span <= 0 || len(t.Buckets) == 1 {
		return 0
	}
	i := int((at - t.start) * int64(len(t.Buckets)) / t.span)
	if i < 0 {
		return 0
	}
	if i >= len(t.Buckets) {
		return len(t.Buckets) - 1
	}
	return i
}

// finish folds the per-bucket counts into the running in-flight figure. It is a
// second pass over sixty entries, not over the cases.
func (t *Timeline) finish() {
	live := 0
	for i := range t.Buckets {
		live += t.Buckets[i].Started - t.Buckets[i].Completed
		t.Buckets[i].InFlight = live
	}
}
