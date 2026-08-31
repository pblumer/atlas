package playground

import (
	"testing"
	"time"
)

// Simulated time is a timeline: a due date computed in the past must not drag the
// clock backwards.
func TestClockNeverRunsBackwards(t *testing.T) {
	c := &vclock{nanos: 1_000}
	c.advanceTo(500)
	if got := c.Now(); got != 1_000 {
		t.Errorf("clock = %d after advancing to an earlier instant, want 1000", got)
	}
	c.advanceTo(2_000)
	if got := c.Now(); got != 2_000 {
		t.Errorf("clock = %d, want 2000", got)
	}
}

// Reading the clock must not move it: a report says "this took four hours", so a
// bare read is not an event.
func TestClockDoesNotTickOnRead(t *testing.T) {
	c := &vclock{nanos: 7}
	_, _ = c.Now(), c.Now()
	if got := c.Now(); got != 7 {
		t.Errorf("clock = %d after three reads, want 7", got)
	}
}

// A fixed stub takes exactly its duration; a banded one lands inside its band and
// lands in the same place for the same job.
func TestStubDurationStaysInsideItsBand(t *testing.T) {
	fixed := Stub{Min: 90 * time.Minute, Max: 30 * time.Minute} // Max below Min: Min wins
	if got := fixed.duration(newDraw(1, 5, 1)); got != 90*time.Minute {
		t.Errorf("duration = %s, want 90m when Max is below Min", got)
	}

	band := Stub{Min: time.Minute, Max: time.Hour}
	for seq := uint64(0); seq < 200; seq++ {
		d := band.duration(newDraw(4711, seq, 1))
		if d < time.Minute || d > time.Hour {
			t.Fatalf("duration %s for job %d is outside [1m, 1h]", d, seq)
		}
	}
	if a, b := band.duration(newDraw(4711, 9, 1)), band.duration(newDraw(4711, 9, 1)); a != b {
		t.Errorf("the same seed and job drew %s then %s", a, b)
	}
}

// The failure draw honours its probability at both ends and is separate from the
// duration draw, so a long task is not thereby a failing one.
func TestStubFailureProbability(t *testing.T) {
	never := Stub{}
	always := Stub{FailPerMillion: 1_000_000}
	half := Stub{FailPerMillion: 500_000}

	if never.fails(newDraw(1, 1, 2)) {
		t.Error("a stub with no failure probability failed")
	}
	if !always.fails(newDraw(1, 1, 2)) {
		t.Error("a stub with certain failure completed")
	}
	failures := 0
	for seq := uint64(0); seq < 1_000; seq++ {
		if half.fails(newDraw(99, seq, 2)) {
			failures++
		}
	}
	if failures < 400 || failures > 600 {
		t.Errorf("a 50%% failure stub failed %d times in 1000; the draw is skewed", failures)
	}
}

// A zero span draws zero rather than dividing by it.
func TestDrawBelowZero(t *testing.T) {
	if got := newDraw(1, 2, 3).below(0); got != 0 {
		t.Errorf("below(0) = %d, want 0", got)
	}
}

// The policy resolves in one order: the element's own entry, then the human or
// machine default, then nothing at all — which parks the job.
func TestStubResolutionOrder(t *testing.T) {
	human := Stub{Min: time.Hour}
	machine := Stub{Min: time.Second}
	named := Stub{Min: time.Millisecond}
	ss := StubSet{Default: &machine, Human: &human, ByElement: map[string]Stub{"named": named}}

	if got, ok := ss.forJob("named", true); !ok || got.Min != time.Millisecond {
		t.Errorf("element entry did not win for a human task: %+v ok=%v", got, ok)
	}
	if got, ok := ss.forJob("other", true); !ok || got.Min != time.Hour {
		t.Errorf("human default = %+v ok=%v, want the human stub", got, ok)
	}
	if got, ok := ss.forJob("other", false); !ok || got.Min != time.Second {
		t.Errorf("machine default = %+v ok=%v, want the machine stub", got, ok)
	}

	empty := StubSet{}
	if _, ok := empty.forJob("anything", false); ok {
		t.Error("an empty policy answered a job; it should park")
	}
	if _, ok := empty.forJob("anything", true); ok {
		t.Error("an empty policy answered a human task; it should park")
	}
}
