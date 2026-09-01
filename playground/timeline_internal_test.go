package playground

import (
	"testing"
	"time"
)

// The timeline's layout is arithmetic nobody will check by eye, and it is asked
// for once per report with numbers a reader will quote. These are its edges: a
// span that is not a span, a bucket count that is not a count, and an instant
// outside the run — each of which would otherwise index past the end of a slice
// or divide by zero in the middle of building a report.
func TestTimelineLayoutEdges(t *testing.T) {
	start := time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC).UnixNano()
	hour := int64(time.Hour)

	t.Run("a run of no duration is one bucket", func(t *testing.T) {
		tl := newTimeline(start, start, 60)
		if len(tl.Buckets) != 1 || tl.Width != 0 {
			t.Fatalf("buckets = %d, width = %s; want one bucket of no width", len(tl.Buckets), tl.Width)
		}
		tl.add(start, true, start)
		tl.finish()
		if b := tl.Buckets[0]; b.Started != 1 || b.Completed != 1 || b.InFlight != 0 {
			t.Errorf("bucket = %+v, want the case counted in and out of the single slice", b)
		}
	})

	t.Run("a backwards span is not a run", func(t *testing.T) {
		if tl := newTimeline(start, start-hour, 60); len(tl.Buckets) != 1 {
			t.Errorf("buckets = %d, want the single bucket an empty span gets", len(tl.Buckets))
		}
	})

	t.Run("fewer than one bucket is one bucket", func(t *testing.T) {
		tl := newTimeline(start, start+hour, 0)
		if len(tl.Buckets) != 1 {
			t.Fatalf("buckets = %d, want one: a run cannot be cut into no parts", len(tl.Buckets))
		}
		if tl.Width != time.Hour {
			t.Errorf("width = %s, want the whole hour", tl.Width)
		}
	})

	t.Run("instants outside the run land in the nearest bucket", func(t *testing.T) {
		tl := newTimeline(start, start+4*hour, 4)
		if got := tl.index(start - hour); got != 0 {
			t.Errorf("an instant before the run indexed to %d, want the first bucket", got)
		}
		// The run's very last instant belongs to the last bucket, not to one past
		// the end: a case that finishes exactly when the run does finished *in* it.
		if got, want := tl.index(start+4*hour), len(tl.Buckets)-1; got != want {
			t.Errorf("the run's end indexed to %d, want %d", got, want)
		}
	})
}
