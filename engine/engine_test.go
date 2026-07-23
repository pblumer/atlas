package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// manualClock is a deterministic, monotonically increasing clock.
type manualClock struct{ t int64 }

func (c *manualClock) Now() int64 { c.t++; return c.t }

// fixedClock reads a settable time without advancing, so a test can control when
// a timer becomes due.
type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { return c.t }

const (
	defKey  = 7
	jobName = "work"
)

// linearProcess builds Start → ServiceTask → End and returns it with the
// interned job type.
func linearProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "linear", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType
	return cp, jobType
}

// scriptProcess builds Start → ScriptTask(exprText → resultVar) → End.
func scriptProcess(t testing.TB, exprText, resultVar string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "scripted", 1)
	start := b.AddStartEvent()
	e, err := expr.CompileAuto(exprText)
	if err != nil {
		t.Fatalf("expr.CompileAuto: %v", err)
	}
	task := b.AddScriptTask(e, resultVar)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// routerProcess builds Start → XOR gateway → (cond "amount > 100" → high) or
// (default → low); each branch is a script task writing "path" then an End.
func routerProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "router", 1)
	start := b.AddStartEvent()
	gw := b.AddExclusiveGateway()

	high := b.AddScriptTask(mustCompile(t, `"high"`), "path")
	low := b.AddScriptTask(mustCompile(t, `"low"`), "path")
	endHigh := b.AddEndEvent()
	endLow := b.AddEndEvent()

	b.Connect(start, gw)
	fHigh := b.Connect(gw, high)
	b.SetFlowCondition(fHigh, mustCompile(t, "amount > 100"))
	fLow := b.Connect(gw, low)
	b.SetFlowDefault(fLow)
	b.Connect(high, endHigh)
	b.Connect(low, endLow)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func mustCompile(t testing.TB, src string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("CompileAuto(%q): %v", src, err)
	}
	return c
}

// timerProcess builds Start → timer catch (durNanos) → script("yes"→done) → End.
func timerProcess(t testing.TB, durNanos int64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "timed", 1)
	start := b.AddStartEvent()
	timer := b.AddTimerCatchEvent(durNanos)
	done := b.AddScriptTask(mustCompile(t, `"yes"`), "done")
	end := b.AddEndEvent()
	b.Connect(start, timer)
	b.Connect(timer, done)
	b.Connect(done, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// readVar returns a scope's variable by name, or nil.
func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var out *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			c := *v
			out = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}

// harness bundles an open wal+store and lets us reopen them to simulate a crash.
type harness struct {
	dir   string
	log   *wal.Log
	store *state.Store
}

func openHarness(t *testing.T, dir string) *harness {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return &harness{dir: dir, log: log, store: store}
}

func (h *harness) close(t *testing.T) {
	t.Helper()
	if err := h.store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := h.log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
}

func activatableJobs(t *testing.T, s *state.Store, jobType int32) []uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActivatableJobs(jobType, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	return keys
}

// completedInstances reads the process-instance history index into a map keyed
// by instance key.
func completedInstances(t *testing.T, s *state.Store) map[uint64]model.ProcessInstanceValue {
	t.Helper()
	out := map[uint64]model.ProcessInstanceValue{}
	if err := s.CompletedProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		out[k] = *v
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	return out
}

// elementVisits reads the element-visit history for a definition into a map of
// element index → total tokens that have passed through it (ADR-0022).
func elementVisits(t *testing.T, s *state.Store, defKey uint64) map[int32]int64 {
	t.Helper()
	out := map[int32]int64{}
	if err := s.ElementVisitHistory(defKey, 0, func(elementId int32, count int64) error {
		out[elementId] += count
		return nil
	}); err != nil {
		t.Fatalf("ElementVisitHistory: %v", err)
	}
	return out
}

func counts(t *testing.T, s *state.Store) (procInstances, elementInstances int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err := s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

// TestExecuteStartServiceTaskEnd runs the Milestone 0 vertical slice end to end
// without a restart: start an instance, it waits at the service task, a worker
// completes the job, and the instance finishes.
func TestExecuteStartServiceTaskEnd(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)

	notified := 0
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	p.SetJobNotifier(func(int32) { notified++ })
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Waiting at the service task: one instance, one element (the task), one job.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}
	if notified != 1 {
		t.Errorf("job notifications = %d, want 1 (side effect after fsync)", notified)
	}

	// A worker completes the job; the instance runs to completion.
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if jobs := activatableJobs(t, h.store, jobType); len(jobs) != 0 {
		t.Errorf("leftover activatable jobs = %d, want 0", len(jobs))
	}
}

// TestScriptTaskRunsToCompletion executes Start → ScriptTask → End with no
// external worker: the script task evaluates its FEEL expression in-engine,
// writes the result variable, and the instance runs straight to completion.
func TestScriptTaskRunsToCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "6 * 7", "answer")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The instance finished on its own.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	// The first minted key is the process instance; it owns the result variable.
	scope := model.NewKey(1, 1)
	got := readVar(t, h.store, scope, "answer")
	if got == nil {
		t.Fatal(`variable "answer" not written`)
	}
	if got.Kind != model.VarNumber || got.Text != "42" {
		t.Fatalf("answer = {kind:%d text:%q}, want number 42", got.Kind, got.Text)
	}
}

// TestScriptTaskReadsInputVariables seeds a start variable and checks the script
// task computes from it: amount 100 with taxRate 0.19 → gross 119.
func TestScriptTaskReadsInputVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "amount * (1 + taxRate)", "gross")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "100"},
		model.VariableValue{Name: "taxRate", Kind: model.VarNumber, Text: "0.19"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1)
	got := readVar(t, h.store, scope, "gross")
	if got == nil || got.Kind != model.VarNumber || got.Text != "119" {
		t.Fatalf("gross = %+v, want number 119", got)
	}
}

// TestExclusiveGatewayRoutesOnCondition drives an instance through an XOR gateway
// whose branch is chosen by a FEEL condition over an input variable.
func TestExclusiveGatewayRoutesOnCondition(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount string
		want   string
	}{
		{"condition true takes high branch", "200", "high"},
		{"condition false falls to default", "50", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)
			cp := routerProcess(t)

			p := engine.New(1, h.log, h.store, &manualClock{})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: tc.amount})
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}

			if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
				t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
			}
			got := readVar(t, h.store, model.NewKey(1, 1), "path")
			if got == nil || got.Text != tc.want {
				t.Fatalf("path = %+v, want %q", got, tc.want)
			}
		})
	}
}

// TestExclusiveGatewayRecovers confirms the chosen branch survives replay: the
// decision is captured by which element got activated, not re-evaluated.
func TestExclusiveGatewayRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := routerProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "999"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if got := readVar(t, store2, model.NewKey(1, 1), "path"); got == nil || got.Text != "high" {
		t.Fatalf("replayed path = %+v, want \"high\"", got)
	}
}

// TestTimerCatchEventWaitsThenContinues checks a timer intermediate catch event
// holds the token until its due date, then continues.
func TestTimerCatchEventWaitsThenContinues(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9) // PT30S
	cp := timerProcess(t, dur)
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
	// Waiting at the timer: one process, one element (the catch event).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("waiting: process=%d element=%d, want 1 and 1", pi, ei)
	}
	// A tick before the due date fires nothing.
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers early: %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatal("timer fired before it was due")
	}
	// Advance past the due date; the tick now fires it and the instance finishes.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers due: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after fire: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, h.store, model.NewKey(1, 1), "done"); got == nil || got.Text != "yes" {
		t.Fatalf("done = %+v, want \"yes\"", got)
	}
}

// TestTimerCatchEventRecovers proves a pending timer survives a crash: replaying
// into a fresh store restores it in the due-date index, and it fires afterward.
func TestTimerCatchEventRecovers(t *testing.T) {
	dir := t.TempDir()
	const dur = int64(10e9)
	cp := timerProcess(t, dur)
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
	h1.close(t)

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
	// The timer was restored and is still pending.
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1", pi, ei)
	}
	// Past due → fires, the instance finishes.
	clk.t = 5_000 + dur + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := readVar(t, store2, model.NewKey(1, 1), "done"); got == nil || got.Text != "yes" {
		t.Fatalf("done = %+v, want \"yes\"", got)
	}
}

// TestScriptTaskRecovers is the recovery property for variables: replaying the
// log into a fresh store rebuilds exactly the state the live run produced
// (invariant I4) — including the FEEL result, which is read back from the event,
// never re-evaluated (invariant I6).
func TestScriptTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := scriptProcess(t, `if 1 < 2 then "yes" else "no"`, "verdict")
	clock := &manualClock{}

	// Live run to completion.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	scope := model.NewKey(1, 1)
	live := readVar(t, h1.store, scope, "verdict")
	if live == nil || live.Kind != model.VarString || live.Text != "yes" {
		t.Fatalf("live verdict = %+v, want string \"yes\"", live)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	// Rebuilt state matches the live run.
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after replay: process=%d element=%d, want 0 and 0", pi, ei)
	}
	replayed := readVar(t, store2, scope, "verdict")
	if replayed == nil || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed verdict = %+v, want %+v", replayed, live)
	}
}

// TestStructuredVariableRoundTripAndRecovers drives a structured (JSON) variable
// through the whole cycle: a seeded object/array start variable is read member-wise
// by a script task (FromStored on KindJSON), the script produces an object result
// stored as VarJSON (Classify on the write path), and replaying the log rebuilds
// the identical VarJSON — proving structured variables satisfy the recovery
// property (invariants I4, I6; ADR-0037).
func TestStructuredVariableRoundTripAndRecovers(t *testing.T) {
	dir := t.TempDir()
	// items is a seeded list; the script sums two members and wraps the total in
	// an object, so both the read (list input) and write (object output) traverse
	// the JSON bridge. FEEL lists are 1-indexed.
	cp := scriptProcess(t, `{sum: items[1] + items[2]}`, "agg")
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "items", Kind: model.VarJSON, Text: "[10,32]"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	scope := model.NewKey(1, 1)
	live := readVar(t, h1.store, scope, "agg")
	if live == nil || live.Kind != model.VarJSON || live.Text != `{"sum":42}` {
		t.Fatalf("live agg = %+v, want VarJSON {\"sum\":42}", live)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	replayed := readVar(t, store2, scope, "agg")
	if replayed == nil || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed agg = %+v, want %+v", replayed, live)
	}
}

// TestRecoverAcrossRestart is the Milestone 0 goal: run until the instance waits
// on its job, simulate a crash (close and reopen the log and store), recover
// state by replaying the log, then complete the job and finish the instance.
func TestRecoverAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t)
	clock := &manualClock{}

	// First run: start an instance and stop at the waiting service task.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(jobsBefore))
	}

	// Crash.
	h1.close(t)

	// Restart: reopen and recover from the log.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	// State must be rebuilt: same instance, same waiting job.
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 1 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobsAfter := activatableJobs(t, h2.store, jobType)
	if len(jobsAfter) != 1 || jobsAfter[0] != jobsBefore[0] {
		t.Fatalf("after recovery: jobs = %v, want %v", jobsAfter, jobsBefore)
	}

	// Completing the recovered job must drive the instance to completion — which
	// also exercises that the key generator resumed without colliding (the End
	// event gets fresh keys).
	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestCompletedInstanceHistoryRecovers is the recovery property for the process
// instance history index (ADR-0017): finishing an instance moves it out of the
// active family and into history with a terminal state and completion time, and
// replaying the log rebuilds byte-identical history — the completion timestamp
// comes from the event header, never re-read from the clock (invariant I4).
func TestCompletedInstanceHistoryRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t)
	clock := &manualClock{}

	// Live run to completion.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	jobs := activatableJobs(t, h1.store, jobType)
	p1.CompleteJob(jobs[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}

	// Gone from the active family, present in history as completed.
	if pi, _ := counts(t, h1.store); pi != 0 {
		t.Fatalf("active process instances = %d, want 0", pi)
	}
	live := completedInstances(t, h1.store)
	if len(live) != 1 {
		t.Fatalf("completed instances = %d, want 1", len(live))
	}
	instKey := model.NewKey(1, 1) // the first minted key is the process instance
	got, ok := live[instKey]
	if !ok {
		t.Fatalf("instance %d not in history (have %v)", instKey, live)
	}
	if got.State != model.PICompleted {
		t.Fatalf("history state = %v, want completed", got.State)
	}
	if got.ProcessDefKey != cp.Key {
		t.Fatalf("history defKey = %d, want %d", got.ProcessDefKey, cp.Key)
	}
	if got.CompletedAt == 0 {
		t.Fatal("history CompletedAt = 0, want the event timestamp")
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	// Rebuilt history matches the live run exactly — including the timestamp.
	replayed := completedInstances(t, store2)
	if len(replayed) != 1 || replayed[instKey] != got {
		t.Fatalf("replayed history = %v, want %v", replayed, live)
	}
}

// TestElementVisitHistoryRecovers is the recovery property for the element-visit
// heatmap (ADR-0022): every element a token activates is counted, the count is
// retained after the element instance is deleted on completion, and replaying the
// log rebuilds an identical count — the visit is derived only from the event
// payload, never re-generated (invariant I4).
func TestElementVisitHistoryRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t) // start → service task → end
	clock := &manualClock{}

	// Live run all the way to completion.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	jobs := activatableJobs(t, h1.store, jobType)
	p1.CompleteJob(jobs[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}

	// The instance has finished — no live element instances remain — yet the visit
	// history still records every one of the three elements a token walked through.
	if _, ei := counts(t, h1.store); ei != 0 {
		t.Fatalf("active element instances = %d, want 0", ei)
	}
	live := elementVisits(t, h1.store, cp.Key)
	if len(live) != 3 {
		t.Fatalf("visited elements = %d (%v), want 3 (start, task, end)", len(live), live)
	}
	for el, n := range live {
		if n != 1 {
			t.Fatalf("element %d visited %d times, want 1", el, n)
		}
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	// Rebuilt visit heatmap matches the live run exactly.
	if replayed := elementVisits(t, store2, cp.Key); !reflect.DeepEqual(replayed, live) {
		t.Fatalf("replayed visits = %v, want %v", replayed, live)
	}
}
