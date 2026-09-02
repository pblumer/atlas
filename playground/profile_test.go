package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/playground"
)

// profileOf is the profile of n cases arriving as a says, or a fatal error.
func profileOf(t *testing.T, sb *playground.Sandbox, n int, a playground.Arrival) playground.ArrivalProfile {
	t.Helper()
	p, err := sb.ArrivalProfileOf(n, a)
	if err != nil {
		t.Fatalf("arrival profile: %v", err)
	}
	return p
}

// sum is how many cases a profile drew.
func sum(b []int) int {
	total := 0
	for _, n := range b {
		total += n
	}
	return total
}

// The point of the profile is that it is the planner's own arithmetic rather than
// a second implementation of it, so what it draws has to be what the plan does.
func TestAnArrivalProfileIsTheStreamThePlanGets(t *testing.T) {
	modes := map[string]playground.Arrival{
		"all at once": {Mode: playground.ArrivalAllAtOnce},
		"a fixed takt": {
			Mode: playground.ArrivalEvery, Interval: 20 * time.Minute,
		},
		"a Poisson stream": {Mode: playground.ArrivalPoisson, PerHour: 6},
		"business hours": {
			Mode: playground.ArrivalEvery, Interval: 90 * time.Minute,
			Calendar: playground.Calendar{Open: []playground.Window{{From: 8 * time.Hour, To: 17 * time.Hour}}},
		},
	}
	for name, a := range modes {
		t.Run(name, func(t *testing.T) {
			// One sandbox for the profile and a second, identical one for the plan:
			// the profile is drawn before anything runs, so it must not need a run.
			profile := profileOf(t, openSandbox(t, "sequence.bpmn", playground.StubSet{}), 24, a)

			planned := openSandbox(t, "sequence.bpmn", playground.StubSet{})
			if err := planned.StartPlan(playground.Plan{Cases: rows(24), Arrival: a}); err != nil {
				t.Fatalf("start plan: %v", err)
			}
			at := planned.Arrivals()
			if len(at) != 24 {
				t.Fatalf("the plan scheduled %d arrivals, want 24", len(at))
			}
			if got, want := profile.Start, at[0]; !got.Equal(want) {
				t.Errorf("profile starts at %s, the plan at %s", got, want)
			}
			if got, want := profile.End, at[len(at)-1]; !got.Equal(want) {
				t.Errorf("profile ends at %s, the plan at %s", got, want)
			}
			if got := sum(profile.Buckets); got != 24 {
				t.Errorf("the profile draws %d cases, want 24", got)
			}
			if !profile.Scheduled || profile.Cases != 24 {
				t.Errorf("profile = %+v, want 24 scheduled cases", profile)
			}
		})
	}
}

// Everything at once has no span to spread over, so the whole batch is one slice
// tall — which is what "all at once" should look like.
func TestAProfileOfEverythingAtOnceIsASingleSlice(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	p := profileOf(t, sb, 40, playground.Arrival{Mode: playground.ArrivalAllAtOnce})

	if p.Span() != 0 {
		t.Errorf("span = %s, want none", p.Span())
	}
	if p.Buckets[0] != 40 || p.Peak() != 40 {
		t.Errorf("buckets = %v, want all 40 in the first", p.Buckets)
	}
	if sum(p.Buckets) != 40 {
		t.Errorf("the profile draws %d cases, want 40", sum(p.Buckets))
	}
}

// A fixed takt is flat: one case per slice when there are as many cases as slices.
func TestAProfileOfAFixedTaktIsFlat(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	p := profileOf(t, sb, 60, playground.Arrival{Mode: playground.ArrivalEvery, Interval: 30 * time.Minute})

	if got, want := p.Span(), 59*30*time.Minute; got != want {
		t.Errorf("span = %s, want %s", got, want)
	}
	if p.Peak() != 1 {
		t.Errorf("peak = %d, want 1; buckets = %v", p.Peak(), p.Buckets)
	}
	for i, n := range p.Buckets {
		if n != 1 {
			t.Fatalf("slice %d holds %d cases, want 1; buckets = %v", i, n, p.Buckets)
		}
	}
}

// A Poisson stream is bursty by construction, so its profile is not flat — and it
// is the same profile every time, because the stream is drawn from the seed.
func TestAProfileOfAPoissonStreamIsBurstyAndRepeatable(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	a := playground.Arrival{Mode: playground.ArrivalPoisson, PerHour: 12}

	p := profileOf(t, sb, 120, a)
	if sum(p.Buckets) != 120 {
		t.Errorf("the profile draws %d cases, want 120", sum(p.Buckets))
	}
	if p.Peak() <= 2 {
		t.Errorf("peak = %d; an unrelated stream of 120 over 60 slices should crowd somewhere", p.Peak())
	}
	again := profileOf(t, sb, 120, a)
	if !again.Start.Equal(p.Start) || !again.End.Equal(p.End) {
		t.Errorf("the same stream drawn twice spans %s–%s then %s–%s", p.Start, p.End, again.Start, again.End)
	}
	for i := range p.Buckets {
		if p.Buckets[i] != again.Buckets[i] {
			t.Fatalf("slice %d = %d then %d; the stream should be the same on a re-draw", i, p.Buckets[i], again.Buckets[i])
		}
	}
}

// A calendar holds the stream inside its windows, and the profile shows that: the
// span outgrows the working day rather than running through the night.
func TestAProfileOnBusinessHoursStaysInsideThem(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	p := profileOf(t, sb, 20, playground.Arrival{
		Mode: playground.ArrivalEvery, Interval: time.Hour,
		Calendar: playground.Calendar{Open: []playground.Window{{From: 8 * time.Hour, To: 17 * time.Hour}}},
	})

	// Twenty hourly cases need three working days, so the stream outlasts two.
	if p.Span() <= 48*time.Hour {
		t.Errorf("span = %s; twenty hourly cases on a nine-hour day should outlast two days", p.Span())
	}
	for _, ts := range []time.Time{p.Start, p.End} {
		if ts.Hour() < 8 || ts.Hour() >= 17 {
			t.Errorf("%s falls outside business hours", ts)
		}
	}
	if sum(p.Buckets) != 20 {
		t.Errorf("the profile draws %d cases, want 20", sum(p.Buckets))
	}
}

// A sequential plan has no schedule of its own — the next case waits on the one
// before it — so the profile says so rather than drawing a line that would mean
// something else.
func TestASequentialPlanHasNoProfile(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	p := profileOf(t, sb, 10, playground.Arrival{Mode: playground.ArrivalSequential})

	if p.Scheduled {
		t.Error("a sequential plan should not report a schedule")
	}
	if p.Cases != 10 {
		t.Errorf("cases = %d, want 10", p.Cases)
	}
	if len(p.Buckets) != 0 || p.Peak() != 0 {
		t.Errorf("buckets = %v, want none", p.Buckets)
	}
	if p.Span() != 0 {
		t.Errorf("span = %s, want none", p.Span())
	}
}

// What a profile refuses. Every refusal a plan makes is one a preview makes too:
// a picture of a stream the run would reject is worse than an error.
func TestArrivalProfileRefusals(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	cases := map[string]struct {
		n int
		a playground.Arrival
	}{
		"no cases":                      {0, playground.Arrival{Mode: playground.ArrivalAllAtOnce}},
		"a negative count":              {-3, playground.Arrival{Mode: playground.ArrivalAllAtOnce}},
		"a takt with no interval":       {5, playground.Arrival{Mode: playground.ArrivalEvery}},
		"a Poisson stream with no rate": {5, playground.Arrival{Mode: playground.ArrivalPoisson}},
		"an arrival mode nobody has":    {5, playground.Arrival{Mode: playground.ArrivalMode(42)}},
		"a window that ends before it starts": {5, playground.Arrival{
			Mode: playground.ArrivalEvery, Interval: time.Minute,
			Calendar: playground.Calendar{Open: []playground.Window{{From: 17 * time.Hour, To: 8 * time.Hour}}},
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := sb.ArrivalProfileOf(c.n, c.a); err == nil {
				t.Error("this profile should be refused")
			}
		})
	}
}
