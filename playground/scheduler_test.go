package playground_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// The author stands in for the outside world: publishing the message the case is
// waiting for carries it on.
func TestPublishedMessageCarriesAWaitingCaseOn(t *testing.T) {
	sb := openSandbox(t, "message-catch.bpmn", playground.DefaultStubs())
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State == model.PICompleted {
		t.Fatal("the case finished without its message")
	}

	if err := sb.PublishMessage("payment-received", "K",
		model.VariableValue{Name: "amount", Kind: model.VarString, Text: "42"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if c, err = sb.Case(key); err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Errorf("state = %v, want completed once the message arrived", c.State)
	}
	if got := c.Variables["amount"]; got != "42" {
		t.Errorf("variable amount = %q, want %q — the message's payload", got, "42")
	}
}

// A message nobody is waiting for is not an error: it is simply not correlated.
func TestPublishingNeedsAName(t *testing.T) {
	sb := openSandbox(t, "message-catch.bpmn", playground.DefaultStubs())
	if err := sb.PublishMessage("", "K"); err == nil {
		t.Error("publishing a nameless message should be refused")
	}
}

// Jumping the clock by hand fires whatever came due — the control an author uses
// when they do not want to wait for the scheduler to get there.
func TestAdvanceFiresWhatCameDue(t *testing.T) {
	sb := openSandbox(t, "timer-catch.bpmn", playground.StubSet{})
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if err := sb.Advance(72 * time.Hour); err != nil {
		t.Fatalf("advance: %v", err)
	}
	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Errorf("state = %v, want completed after the clock was jumped past the timer", c.State)
	}
	if got, want := sb.Now(), simStart.Add(72*time.Hour); !got.Equal(want) {
		t.Errorf("simulated time = %s, want %s", got, want)
	}
}

// Simulated time is a timeline, not a dial: it never runs backwards.
func TestAdvanceRefusesToGoBackwards(t *testing.T) {
	sb := openSandbox(t, "timer-catch.bpmn", playground.StubSet{})
	if err := sb.Advance(-time.Hour); err == nil {
		t.Error("moving simulated time backwards should be refused")
	}
	if got := sb.Now(); !got.Equal(simStart) {
		t.Errorf("simulated time = %s, want it left at %s", got, simStart)
	}
}

// The horizon stops a run before it steps past it, so a model that would run for
// simulated years cannot be walked there by accident.
func TestHorizonStopsTheRunShortOfIt(t *testing.T) {
	sb := openSandbox(t, "timer-catch.bpmn", playground.StubSet{})
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	prog, err := sb.Run(playground.Budget{Horizon: time.Hour})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if prog.Quiescent {
		t.Error("run reported quiescence although a timer was still pending beyond the horizon")
	}
	if got := sb.Now(); !got.Equal(simStart) {
		t.Errorf("simulated time = %s; the three-day timer was jumped despite a one-hour horizon", got)
	}
	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State == model.PICompleted {
		t.Error("the case completed past the horizon")
	}
}

// The occurrence cap is the other half of the bound: it stops a run that keeps
// finding work without ever coming to rest.
func TestOccurrenceCapStopsTheRun(t *testing.T) {
	sb := openSandbox(t, "two-tasks.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	if _, err := sb.StartCase(); err != nil {
		t.Fatalf("start case: %v", err)
	}
	prog, err := sb.Run(playground.Budget{MaxOccurrences: 1})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if prog.Occurrences != 1 || prog.Quiescent {
		t.Errorf("progress = %+v, want exactly one occurrence and no quiescence", prog)
	}
}

// A model with more than one executable process has to say which one to run,
// rather than the sandbox picking for the author.
func TestAmbiguousRootIsRefused(t *testing.T) {
	_, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "two-pools.bpmn"), BaseDir: t.TempDir(), StartTime: simStart,
	})
	if err == nil {
		t.Fatal("a model with two executable processes should refuse to run without a named root")
	}
	if !strings.Contains(err.Error(), "name the one to run") {
		t.Errorf("error %q should say what the caller has to do about it", err)
	}
}

// Naming the root picks it out of a multi-process model.
func TestNamedRootPicksItsProcess(t *testing.T) {
	sb, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "two-pools.bpmn"), Root: "second", BaseDir: t.TempDir(), StartTime: simStart,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	if got := sb.ProcessID(); got != "second" {
		t.Errorf("process = %q, want %q", got, "second")
	}
	if _, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "two-pools.bpmn"), Root: "nope", BaseDir: t.TempDir(),
	}); err == nil {
		t.Error("naming a process the model does not have should be refused")
	}
}

// Bad input fails at Open, before anything is created.
func TestOpenRefusesWhatItCannotRun(t *testing.T) {
	if _, err := playground.Open(playground.Options{BaseDir: t.TempDir()}); err == nil {
		t.Error("opening a sandbox with no model should be refused")
	}
	if _, err := playground.Open(playground.Options{
		ModelXML: []byte("<definitions/>"), BaseDir: t.TempDir(),
	}); err == nil {
		t.Error("opening a sandbox on a model with no executable process should be refused")
	}
}

// Completing something that is not waiting is a mistake worth reporting, not a
// silent no-op.
func TestCompletingAJobThatIsNotWaitingIsRefused(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.DefaultStubs())
	runOneCase(t, sb)
	if err := sb.CompleteTask(12345); err == nil {
		t.Error("completing an unknown job should be refused")
	}
}

// A stub can throw a modelled business error instead of completing, so the branch
// behind an error boundary event is reachable without a real system declining
// anything.
func TestStubCanThrowAModelledBusinessError(t *testing.T) {
	sb := openSandbox(t, "boundary-error.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Second, Max: time.Second,
			FailPerMillion: 1_000_000, ErrorCode: "DECLINED"},
	})
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Fatalf("state = %v, want completed down the error branch", c.State)
	}
	if c.Incidents != 0 {
		t.Errorf("incidents = %d; a thrown business error is caught, not an incident", c.Incidents)
	}
	if last := c.Path[len(c.Path)-1]; last != "handled" {
		t.Errorf("case ended at %q, want \"handled\" — the boundary event's branch", last)
	}
}

// An interrupting boundary event can take a token away while the sandbox is still
// holding a committed answer for its job. The answer has to be dropped, not
// applied to a job that no longer exists.
func TestAnswerIsDroppedWhenAnInterruptTakesTheJob(t *testing.T) {
	sb := openSandbox(t, "boundary-timer.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: 4 * time.Hour, Max: 4 * time.Hour},
	})
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Fatalf("state = %v, want completed", c.State)
	}
	if last := c.Path[len(c.Path)-1]; last != "escalated" {
		t.Errorf("case ended at %q, want \"escalated\" — the ten-minute timer beat the four-hour stub", last)
	}
	if got, want := sb.Now(), simStart.Add(10*time.Minute); !got.Equal(want) {
		t.Errorf("simulated time = %s, want %s: the run must not wait out an answer it dropped", got, want)
	}
}

// Reading a case that does not exist is an error, not an empty result.
func TestUnknownCaseIsAnError(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	if _, err := sb.Case(999); err == nil {
		t.Error("reading an unknown case should be refused")
	}
}
