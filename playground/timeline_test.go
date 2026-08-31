package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/playground"
)

// The temporal half of the report: when the cases arrived, when they finished,
// and how many were in flight in between. A run reported only as totals cannot
// answer "the queue built up on Tuesday morning", which is the question a
// capacity change is argued from.
func TestTimelineCountsArrivalsAndCompletionsOverSimulatedTime(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: 30 * time.Minute, Max: 30 * time.Minute},
	})
	// One case an hour, half an hour of work each: the run spans 08:00 to 11:30.
	runPlan(t, sb, playground.Plan{
		Cases:   rows(4),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: time.Hour},
	})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	tl := rep.Timeline
	if len(tl.Buckets) == 0 {
		t.Fatal("the timeline has no buckets")
	}
	var started, completed int
	for _, b := range tl.Buckets {
		started += b.Started
		completed += b.Completed
	}
	if started != 4 || completed != 4 {
		t.Errorf("timeline totals = %d started / %d completed, want 4 and 4", started, completed)
	}
	// The buckets tile the run: the first begins where simulated time did, and the
	// run's end falls inside the last one rather than past it. The slack is the
	// nanoseconds the division leaves — a span rarely divides sixty ways exactly.
	if !tl.Buckets[0].At.Equal(rep.SimStart) {
		t.Errorf("the first bucket starts at %s, want the run's start %s", tl.Buckets[0].At, rep.SimStart)
	}
	gap := rep.SimEnd.Sub(tl.Buckets[len(tl.Buckets)-1].At)
	if gap <= 0 || gap > tl.Width+time.Duration(len(tl.Buckets)) {
		t.Errorf("the run ends %s after the last bucket starts, want within its %s width", gap, tl.Width)
	}
	// Nothing is in flight when the last case has finished.
	if got := tl.Buckets[len(tl.Buckets)-1].InFlight; got != 0 {
		t.Errorf("in flight at the end = %d, want 0", got)
	}
}

// In flight is arrivals minus completions, so it is the work in progress a
// reader compares against the arrival rate — and its peak is the same number the
// report already gives as MaxInFlight.
func TestTimelineInFlightPeaksWhereTheReportSaysItDid(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: 2 * time.Hour, Max: 2 * time.Hour},
		Pools: map[string]playground.Pool{"clerks": {Capacity: 1}},
		// One seat, six cases arriving every ten minutes and two hours of work
		// each: the queue can only grow.
		PoolOf: map[string]string{"approve": "clerks"},
	})
	runPlan(t, sb, playground.Plan{
		Cases:   rows(6),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: 10 * time.Minute},
	})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	peak := 0
	for _, b := range rep.Timeline.Buckets {
		if b.InFlight > peak {
			peak = b.InFlight
		}
	}
	if peak != rep.MaxInFlight {
		t.Errorf("the timeline peaks at %d in flight, the report says %d", peak, rep.MaxInFlight)
	}
	if peak < 2 {
		t.Errorf("peak = %d: six cases against one seat should pile up", peak)
	}
}

// A run that takes no simulated time still has a timeline — one bucket holding
// everything. Dividing the run's span into sixty parts is not something a report
// may do before checking the span is not zero.
func TestTimelineOfAnInstantRun(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	runPlan(t, sb, playground.Plan{Cases: rows(3)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !rep.SimEnd.Equal(rep.SimStart) {
		t.Fatalf("this run was meant to take no simulated time, but spans %s", rep.SimEnd.Sub(rep.SimStart))
	}
	if len(rep.Timeline.Buckets) != 1 {
		t.Fatalf("buckets = %d, want the single one an instant run has", len(rep.Timeline.Buckets))
	}
	b := rep.Timeline.Buckets[0]
	if b.Started != 3 || b.Completed != 3 || b.InFlight != 0 {
		t.Errorf("bucket = %+v, want three started, three completed, none left", b)
	}
}
