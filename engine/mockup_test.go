package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// mockupProcess builds Start → MockupTask(cfg) → End. exprText, when non-empty, is
// compiled as the FEEL result expression; the caller sets ResultVar on cfg to match.
func mockupProcess(t testing.TB, cfg compiler.MockupConfig, exprText string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "mockup", 1)
	start := b.AddStartEvent()
	if exprText != "" {
		cfg.Expr = mustCompile(t, exprText)
	}
	task := b.AddMockupTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestMockupTaskSimulatesDurationAndResult drives the happy path: on activation the
// engine writes the FEEL result (an input→output "script") and arms a timer for the
// simulated duration, parking the token; when the timer is due the task completes and
// the instance finishes — no external worker or job involved.
func TestMockupTaskSimulatesDurationAndResult(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(10e9)
	// A fixed duration (min == max) so the due date is exactly Now()+dur.
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, ResultVar: "doubled"}, "amount * 2")
	clk := &fixedClock{t: 5_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "21"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked on the mockup task, no jobs, and the result already written.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after activation: process=%d element=%d, want 1 and 1 (parked on the timer)", pi, ei)
	}
	scope := model.NewKey(1, 1)
	if got := readVar(t, h.store, scope, "doubled"); got == nil || got.Kind != model.VarNumber || got.Text != "42" {
		t.Fatalf("result variable = %+v, want number 42", got)
	}
	// Not yet due: nothing fires.
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (not due): %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("before due the instance already finished (process=%d)", pi)
	}
	// Past due → the simulated task completes and the instance finishes.
	clk.t = 5_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (due): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after due: process=%d element=%d, want 0 and 0 (finished)", pi, ei)
	}
}

// TestMockupTaskWithoutResultCompletes proves a mockup task with no result
// expression is valid: it simply waits its duration and completes, writing nothing.
func TestMockupTaskWithoutResultCompletes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(2e9)
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur}, "")
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after due: process=%d element=%d, want 0 and 0 (finished)", pi, ei)
	}
}

// TestMockupTaskResultEvalErrorIsNull proves a result expression that fails at
// evaluation (a type error) yields a null variable rather than halting the
// processor — FEEL's null-propagating contract, matching the inline script task.
func TestMockupTaskResultEvalErrorIsNull(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(1e9)
	// A string + number is a FEEL type error at evaluation time.
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, ResultVar: "bad"}, `"x" + 1`)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readVar(t, h.store, model.NewKey(1, 1), "bad")
	if got == nil || got.Kind != model.VarNull {
		t.Fatalf("result of a failed expression = %+v, want a null variable", got)
	}
}

// TestMockupTaskFailureRaisesIncidentAndReArms proves the failure simulation:
// failRate=1 (always fail) parks the token with a job-less incident when the timer
// fires, and resolving the incident re-arms a fresh attempt (a new timer) — which,
// still at failRate=1, fails again. This exercises the incident + rearmTimerElement
// path a partial failRate reaches probabilistically.
func TestMockupTaskFailureRaisesIncidentAndReArms(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(1e9)
	cp := mockupProcess(t, compiler.MockupConfig{
		MinNanos: dur, MaxNanos: dur, FailPerMillion: 1_000_000, FailMessage: "umsystem down",
	}, "")
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Fire the first attempt's timer → a simulated failure raises an incident.
	clk.t = 1_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (attempt 1): %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("simulated failure raised %d incidents, want 1", len(inc))
	}
	var elKey uint64
	for k, v := range inc {
		elKey = k
		if v.JobKey != 0 {
			t.Fatalf("mockup incident JobKey = %d, want 0 (job-less)", v.JobKey)
		}
		if v.Message != "umsystem down" {
			t.Fatalf("mockup incident message = %q, want %q", v.Message, "umsystem down")
		}
	}
	// The token is parked, not finished.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked on incident: process=%d element=%d, want 1 and 1", pi, ei)
	}

	// Resolve → a fresh timer is armed (a new attempt). Its due date is Now()+dur,
	// past the current clock, so it does not fire until the clock advances again.
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("after resolve: %d incidents remain, want 0 (re-armed)", n)
	}
	// Advance past the new timer's due date → the retry fails again (failRate still 1).
	clk.t = clk.t + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (attempt 2): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 1 {
		t.Fatalf("after the re-armed attempt: %d incidents, want 1 (failed again)", n)
	}
}

// TestMockupTaskFailureDefaultMessage proves a simulated failure without an
// authored message still raises an incident, with a sensible default.
func TestMockupTaskFailureDefaultMessage(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(1e9)
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, FailPerMillion: 1_000_000}, "")
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("simulated failure raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if v.Message != "mockup task simulated failure" {
			t.Fatalf("default incident message = %q, want the fallback", v.Message)
		}
	}
}

// TestMockupTaskThrowsBpmnErrorCaughtByBoundary proves a simulated failure with an
// error code throws a BPMN error that a matching error boundary on the mockup task
// catches (ADR-0089): the task is torn down and the recovery flow runs, rather than
// an incident being raised.
func TestMockupTaskThrowsBpmnErrorCaughtByBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(1e9)
	b := compiler.NewBuilder(defKey, "mockuperr", 1)
	start := b.AddStartEvent()
	task := b.AddMockupTask(compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, FailPerMillion: 1_000_000, ErrorCode: "UMSYS"})
	boundary := b.AddBoundaryErrorEvent(task, "UMSYS")
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	// The error aborted the task and the boundary routed to recovery; no incident.
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("a caught BPMN error raised %d incidents, want 0", n)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after caught error: process=%d element=%d, want 0 and 0 (finished)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (error boundary fired)", v[recovered])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0 (the task was aborted by the error)", v[done])
	}
}

// TestMockupTaskUncaughtErrorRaisesIncident proves a simulated BPMN error with no
// matching handler surfaces as an incident on the throwing element (ADR-0089), the
// same as any other unhandled error.
func TestMockupTaskUncaughtErrorRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(1e9)
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, FailPerMillion: 1_000_000, ErrorCode: "UMSYS"}, "")
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("uncaught error raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if v.Message != "unhandled error 'UMSYS'" {
			t.Fatalf("incident message = %q, want the unhandled-error message", v.Message)
		}
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked on incident: process=%d element=%d, want 1 and 1", pi, ei)
	}
}

// TestMockupTaskRecovers is the recovery property (invariants I4/I6): after a
// mockup task arms its timer and writes its result, replaying the log into a fresh
// store restores the pending timer and the result — the random due date is read back
// from the event, never re-drawn — and firing the restored timer completes the task.
func TestMockupTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	const dur = int64(10e9)
	cp := mockupProcess(t, compiler.MockupConfig{MinNanos: dur, MaxNanos: dur, ResultVar: "answer"}, `"ok"`)
	clk := &fixedClock{t: 5_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	scope := model.NewKey(1, 1)
	live := readVar(t, h1.store, scope, "answer")
	if live == nil || live.Kind != model.VarString || live.Text != "ok" {
		t.Fatalf("live result = %+v, want string \"ok\"", live)
	}
	h1.close(t)

	// Replay the same log into a fresh store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	// The timer and the result variable are restored; the task is still pending.
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1", pi, ei)
	}
	if got := readVar(t, store2, scope, "answer"); got == nil || got.Text != live.Text {
		t.Fatalf("replayed result = %+v, want %+v", got, live)
	}
	// Past due → the restored timer fires and the instance finishes.
	clk.t = 5_000 + dur
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after due: process=%d element=%d, want 0 and 0 (finished)", pi, ei)
	}
}
