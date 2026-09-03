package playground_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// simStart is the instant every test's simulated clock starts at. Fixed, so a
// test can assert on absolute simulated times without reading the wall clock.
var simStart = time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	xml, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return xml
}

// openSandbox opens a sandbox on a fixture with the test's temp dir as its base,
// and closes it when the test ends.
func openSandbox(t *testing.T, name string, stubs playground.StubSet) *playground.Sandbox {
	t.Helper()
	sb, err := playground.Open(playground.Options{
		ModelXML:  fixture(t, name),
		BaseDir:   t.TempDir(),
		StartTime: simStart,
		Seed:      1,
		Stubs:     stubs,
	})
	if err != nil {
		t.Fatalf("open sandbox: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	return sb
}

// runOneCase starts a single case and runs the sandbox to quiescence.
func runOneCase(t *testing.T, sb *playground.Sandbox, vars ...model.VariableValue) uint64 {
	t.Helper()
	key, err := sb.StartCase(vars...)
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	return key
}

// A model that needs nothing from the outside runs to its end on the first drain:
// the sandbox is the real processor, so an in-engine script task just works.
func TestSelfCompletingModelRunsToTheEnd(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Errorf("instance state = %v, want completed", c.State)
	}
	if got := c.Variables["a"]; got != "1" {
		t.Errorf("variable a = %q, want %q — the FEEL script ran in the engine", got, "1")
	}
	if want := []string{"start", "set_a", "end"}; !equalStrings(c.Path, want) {
		t.Errorf("path = %v, want %v", c.Path, want)
	}
}

// Every key a sandbox mints carries a partition from the reserved sandbox range,
// so a sandbox key can never be mistaken for one from the durable engine.
func TestSandboxKeysCarryAReservedPartition(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	key := runOneCase(t, sb)

	if p := model.PartitionOf(key); p < playground.PartitionBase {
		t.Errorf("instance key partition = %d, want >= %d (the reserved sandbox range)",
			p, playground.PartitionBase)
	}
}

// Close takes the sandbox's whole footprint with it: nothing durable is left over
// from a playground run.
func TestCloseRemovesEverythingTheSandboxWrote(t *testing.T) {
	base := t.TempDir()
	sb, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "sequence.bpmn"), BaseDir: base, StartTime: simStart,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	dir := sb.Dir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("sandbox dir should exist while open: %v", err)
	}
	if err := sb.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("sandbox dir %s still there after Close (err=%v)", dir, err)
	}
}

// A job task parks forever in a sandbox — there is no worker — until a stub
// answers it. That is the stub policy's whole job.
func TestJobParksWithoutAStubAndCompletesWithOne(t *testing.T) {
	parked := openSandbox(t, "service-task.bpmn", playground.StubSet{})
	key := runOneCase(t, parked)
	c, err := parked.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State == model.PICompleted {
		t.Fatal("instance completed with no stub and no worker — the job should have parked")
	}
	tasks, err := parked.OpenTasks()
	if err != nil {
		t.Fatalf("open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Element != "charge" {
		t.Fatalf("open tasks = %+v, want exactly one on element \"charge\"", tasks)
	}

	stubbed := openSandbox(t, "service-task.bpmn", playground.StubSet{
		ByElement: map[string]playground.Stub{
			"charge": {Min: time.Second, Max: time.Second,
				Outputs: []model.VariableValue{{Name: "status", Kind: model.VarString, Text: "captured"}}},
		},
	})
	key = runOneCase(t, stubbed)
	c, err = stubbed.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Errorf("instance state = %v, want completed once the stub answers", c.State)
	}
	if got := c.Variables["status"]; got != "captured" {
		t.Errorf("variable status = %q, want %q — the stub's output", got, "captured")
	}
}

// A stub's duration is spent in simulated time only. Four hours of "work" costs
// the caller no wall-clock time at all: that is what makes a simulated day cheap.
func TestStubDurationIsSpentInSimulatedTimeOnly(t *testing.T) {
	sb := openSandbox(t, "service-task.bpmn", playground.StubSet{
		ByElement: map[string]playground.Stub{
			"charge": {Min: 4 * time.Hour, Max: 4 * time.Hour},
		},
	})

	wallBefore := time.Now()
	runOneCase(t, sb)
	wall := time.Since(wallBefore)

	if got, want := sb.Now(), simStart.Add(4*time.Hour); !got.Equal(want) {
		t.Errorf("simulated time = %s, want %s", got, want)
	}
	if wall > 2*time.Second {
		t.Errorf("four simulated hours took %s of wall-clock time; the clock is not virtual", wall)
	}
}

// A timer is not waited out, it is jumped: the scheduler moves simulated time to
// the next due timer when nothing else can run.
func TestTimerIsJumpedRatherThanWaitedOut(t *testing.T) {
	sb := openSandbox(t, "timer-catch.bpmn", playground.StubSet{})

	wallBefore := time.Now()
	key := runOneCase(t, sb)
	wall := time.Since(wallBefore)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Fatalf("instance state = %v, want completed after the timer fired", c.State)
	}
	if got, want := sb.Now(), simStart.Add(72*time.Hour); !got.Equal(want) {
		t.Errorf("simulated time = %s, want %s (three days jumped)", got, want)
	}
	if wall > 2*time.Second {
		t.Errorf("three simulated days took %s of wall-clock time", wall)
	}
}

// A user task waits for a person by default, and the person is the caller: this is
// the interactive half of the Playground.
func TestUserTaskWaitsForAPersonAndIsCompletedByHand(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.DefaultStubs())
	key := runOneCase(t, sb)

	tasks, err := sb.OpenTasks()
	if err != nil {
		t.Fatalf("open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Element != "approve" || !tasks[0].Human {
		t.Fatalf("open tasks = %+v, want one human task on \"approve\"", tasks)
	}

	if err := sb.CompleteTask(tasks[0].JobKey,
		model.VariableValue{Name: "decision", Kind: model.VarString, Text: "yes"}); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Errorf("instance state = %v, want completed after the human completed the task", c.State)
	}
	if got := c.Variables["decision"]; got != "yes" {
		t.Errorf("variable decision = %q, want %q", got, "yes")
	}
}

// A stub can fail instead of completing, so error and retry paths are reachable
// without a real system misbehaving.
func TestStubCanFailIntoAnIncident(t *testing.T) {
	sb := openSandbox(t, "service-task.bpmn", playground.StubSet{
		ByElement: map[string]playground.Stub{
			"charge": {Min: time.Second, Max: time.Second, FailPerMillion: 1_000_000, FailMessage: "gateway down"},
		},
	})
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State == model.PICompleted {
		t.Error("instance completed although its only task always fails")
	}
	if c.Incidents != 1 {
		t.Errorf("incidents = %d, want 1", c.Incidents)
	}
}

// The isolation property, stated as a test: a REST task names a URL that
// nothing may fetch. The sandbox registers no workers at all, so the stub is the
// only thing that can answer it — the call is not "configured off", it is impossible.
func TestConnectorTaskIsAnsweredByTheStubAndNeverCalled(t *testing.T) {
	sb := openSandbox(t, "rest-connector.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Second, Max: time.Second,
			Outputs: []model.VariableValue{{Name: "score", Kind: model.VarString, Text: "A"}}},
	})
	key := runOneCase(t, sb)

	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	if c.State != model.PICompleted {
		t.Fatalf("instance state = %v, want completed — the stub answers the task", c.State)
	}
	if got := c.Variables["score"]; got != "A" {
		t.Errorf("variable score = %q, want %q from the stub", got, "A")
	}
}

// Step advances exactly one occurrence, which is what makes stepping through a
// case by hand mean something.
func TestStepAdvancesOneOccurrenceAtATime(t *testing.T) {
	sb := openSandbox(t, "two-tasks.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	if _, err := sb.StartCase(); err != nil {
		t.Fatalf("start case: %v", err)
	}

	occ, ok, err := sb.Step()
	if err != nil || !ok {
		t.Fatalf("first step: ok=%v err=%v", ok, err)
	}
	if occ.Element != "first" {
		t.Errorf("first occurrence on %q, want \"first\"", occ.Element)
	}
	if got, want := sb.Now(), simStart.Add(time.Minute); !got.Equal(want) {
		t.Errorf("simulated time after one step = %s, want %s", got, want)
	}

	occ, ok, err = sb.Step()
	if err != nil || !ok {
		t.Fatalf("second step: ok=%v err=%v", ok, err)
	}
	if occ.Element != "second" {
		t.Errorf("second occurrence on %q, want \"second\"", occ.Element)
	}

	if _, ok, err = sb.Step(); err != nil {
		t.Fatalf("third step: %v", err)
	} else if ok {
		t.Error("a third step found work; the case should be finished")
	}
}

// The overlay the Modeler draws is read out of the sandbox's own state, in the
// same shape the runtime view already uses.
func TestOverlayCountsEveryElementATokenPassedThrough(t *testing.T) {
	sb := openSandbox(t, "two-tasks.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runOneCase(t, sb)

	visits, err := sb.ElementVisits()
	if err != nil {
		t.Fatalf("element visits: %v", err)
	}
	for _, id := range []string{"start", "first", "second", "end"} {
		if visits[id] != 1 {
			t.Errorf("visits[%q] = %d, want 1", id, visits[id])
		}
	}
}

// Same dataset, same config, same seed → same run. Without this the report cannot
// be quoted in a review or used as a regression check.
func TestRunIsReproducibleForTheSameSeed(t *testing.T) {
	end := func(seed int64) time.Time {
		sb, err := playground.Open(playground.Options{
			ModelXML: fixture(t, "two-tasks.bpmn"), BaseDir: t.TempDir(), StartTime: simStart, Seed: seed,
			Stubs: playground.StubSet{Default: &playground.Stub{Min: time.Minute, Max: 8 * time.Hour}},
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = sb.Close() }()
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case: %v", err)
		}
		if _, err := sb.Run(playground.DefaultBudget()); err != nil {
			t.Fatalf("run: %v", err)
		}
		return sb.Now()
	}

	a, b := end(4711), end(4711)
	if !a.Equal(b) {
		t.Errorf("same seed produced different runs: %s vs %s", a, b)
	}
	if a.Before(simStart.Add(2*time.Minute)) || a.After(simStart.Add(16*time.Hour)) {
		t.Errorf("two stubs of 1 min..8 h ended at %s, outside the possible range", a)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// One case read on its own shows its variables the way the results table does.
// Reading the text field alone loses a boolean, which keeps its value elsewhere —
// the results table was fixed for that and this path was not, so opening a case
// from the table showed an empty column the table had just filled in.
func TestOneCaseShowsEveryKindOfVariable(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	key := runOneCase(t, sb,
		model.VariableValue{Name: "kunde", Kind: model.VarString, Text: "A"},
		model.VariableValue{Name: "betrag", Kind: model.VarNumber, Text: "1200"},
		model.VariableValue{Name: "express", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "notiz", Kind: model.VarNull},
	)
	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	for name, want := range map[string]string{
		"kunde": "A", "betrag": "1200", "express": "true", "notiz": "null",
	} {
		if got := c.Variables[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
