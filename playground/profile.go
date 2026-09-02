package playground

import (
	"errors"
	"time"
)

// profileSlices is how many slices an arrival profile is drawn in. It matches the
// timeline's own resolution, so the shape of the plan and the shape of the run it
// produced are read at the same grain.
const profileSlices = timelineBuckets

// ArrivalProfile is the shape of a planned arrival stream: how many cases land in
// each slice of the time it spans.
//
// It exists so a panel can show the timing profile *before* the run rather than
// after it — and show it from the code that plans the run, not from a second
// implementation of the same arithmetic in a browser. A profile drawn by anything
// but the planner is a picture of a stream nobody is going to get.
type ArrivalProfile struct {
	// Scheduled is false for a sequential plan, which has no schedule of its own:
	// its next arrival depends on the run, so there is no shape to draw and saying
	// so is better than drawing a flat line that means something else.
	Scheduled bool
	// Cases is how many the profile is of, and Start and End bound them.
	Cases      int
	Start, End time.Time
	// Buckets holds the arrivals per slice, oldest first. Empty when not Scheduled.
	Buckets []int
}

// Span is how long the stream takes end to end.
func (p ArrivalProfile) Span() time.Duration { return p.End.Sub(p.Start) }

// Peak is the fullest slice — what a drawn profile is scaled against.
func (p ArrivalProfile) Peak() int {
	max := 0
	for _, n := range p.Buckets {
		if n > max {
			max = n
		}
	}
	return max
}

// ArrivalProfileOf lays out n cases the way [Sandbox.StartPlan] would and reports
// their shape, without seeding a plan or running anything.
//
// It takes a count rather than the cases themselves because the timing does not
// depend on them: building fifty thousand rows to find out when they would arrive
// would be paying the dataset's cost to answer a question it has no part in.
func (s *Sandbox) ArrivalProfileOf(n int, a Arrival) (ArrivalProfile, error) {
	if n < 1 {
		return ArrivalProfile{}, errors.New("playground: an arrival profile needs at least one case")
	}
	if err := a.Calendar.validate("the arrival calendar"); err != nil {
		return ArrivalProfile{}, err
	}
	at, err := s.arrivalTimes(n, a)
	if err != nil {
		return ArrivalProfile{}, err
	}
	if at == nil {
		return ArrivalProfile{Cases: n}, nil
	}

	// The instants come out in order, so the first and the last bound them.
	first, last := at[0], at[len(at)-1]
	out := ArrivalProfile{
		Scheduled: true, Cases: n,
		Start:   time.Unix(0, first).UTC(),
		End:     time.Unix(0, last).UTC(),
		Buckets: make([]int, profileSlices),
	}
	span := last - first
	for _, t := range at {
		// A stream that all lands at once has no span to divide by; it is one slice
		// tall, which is exactly what "all at once" looks like.
		i := 0
		if span > 0 {
			i = int((t - first) * int64(profileSlices) / span)
			if i >= profileSlices {
				i = profileSlices - 1
			}
		}
		out.Buckets[i]++
	}
	return out, nil
}
