package script_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// fakeExec is a deterministic stand-in for a real interpreter: it records the
// source and inputs it was handed and returns a preset result (or error).
type fakeExec struct {
	result    any
	err       error
	gotSource string
	gotInput  map[string]any
	calls     int
}

func (f *fakeExec) Run(_ context.Context, source string, input map[string]any) (any, error) {
	f.calls++
	f.gotSource = source
	f.gotInput = input
	return f.result, f.err
}

const scriptDefKey = 71

// scriptProcess: Start → PowerShell script task → End.
func scriptProcess(t *testing.T, source, resultVar string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(scriptDefKey, "greeting", 1)
	start := b.AddStartEvent()
	sj := b.AddScriptJobTask(compiler.PwshJobType, "powershell", source, resultVar, 3)
	end := b.AddEndEvent()
	b.Connect(start, sj)
	b.Connect(sj, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ScriptJobTask(cp.Node(sj).Detail).JobType
}

func openStore(t *testing.T) (*state.Store, *wal.Log) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })
	return store, log
}

func active(t *testing.T, s *state.Store) (pi, ei int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err = s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

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

func lookupOf(cp *compiler.CompiledProcess) script.ProcessLookup {
	return func(k uint64) *compiler.CompiledProcess {
		if k == cp.Key {
			return cp
		}
		return nil
	}
}

// TestScriptTaskRunsAndWritesResult is the vertical slice end to end: a PowerShell
// script task creates a job, the in-process script worker runs the script with the
// instance's variables as input, writes the result back as the task's result
// variable, completes the job, and the instance finishes — proving Atlas drives a
// polyglot script through the normal job path (ADR-0047).
func TestScriptTaskRunsAndWritesResult(t *testing.T) {
	store, log := openStore(t)
	cp, jobType := scriptProcess(t, `"Hallo " + $Vorname`, "Greeting")

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	fx := &fakeExec{result: "Hallo Anna"}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, script.Handler(store, lookupOf(cp), fx))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "Vorname", Kind: model.VarString, Text: "Anna"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// The worker ran the script exactly once, with the authored source and the
	// instance's variables as input.
	if fx.calls != 1 {
		t.Fatalf("exec calls = %d, want 1", fx.calls)
	}
	if fx.gotSource != `"Hallo " + $Vorname` {
		t.Errorf("script source = %q, want the authored body", fx.gotSource)
	}
	if fx.gotInput["Vorname"] != "Anna" {
		t.Errorf("input Vorname = %#v, want Anna", fx.gotInput["Vorname"])
	}

	// The result was written back as the result variable, and the instance finished.
	scope := model.NewKey(1, 1)
	got := readVar(t, store, scope, "Greeting")
	if got == nil || got.Kind != model.VarString || got.Text != "Hallo Anna" {
		t.Fatalf("Greeting = %+v, want string \"Hallo Anna\"", got)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestScriptTaskSeesInputMappedLocal proves the worker reads its activity-local
// scope: an input mapping computes a local from an inherited variable, and the
// script sees both the local and the inherited value; on completion the local is
// dropped, so only the result reaches the process scope (ADR-0068).
func TestScriptTaskSeesInputMappedLocal(t *testing.T) {
	store, log := openStore(t)

	b := compiler.NewBuilder(scriptDefKey, "greeting", 1)
	start := b.AddStartEvent()
	sj := b.AddScriptJobTask(compiler.PwshJobType, "powershell", `$local`, "out", 3)
	src, err := expr.CompileAuto(`name + "!"`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b.AddInputMapping(sj, "local", src)
	end := b.AddEndEvent()
	b.Connect(start, sj)
	b.Connect(sj, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ScriptJobTask(cp.Node(sj).Detail).JobType

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	fx := &fakeExec{result: "done"}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, script.Handler(store, lookupOf(cp), fx))

	p.CreateInstance(cp.Key, model.VariableValue{Name: "name", Kind: model.VarString, Text: "Anna"})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// The worker saw the input-mapped local shadowing/adding to the inherited scope.
	if fx.gotInput["local"] != "Anna!" {
		t.Errorf("input local = %#v, want \"Anna!\" (input-mapped)", fx.gotInput["local"])
	}
	if fx.gotInput["name"] != "Anna" {
		t.Errorf("input name = %#v, want \"Anna\" (inherited from the process scope)", fx.gotInput["name"])
	}

	// The result reached the process scope; the local was dropped, never promoted.
	root := model.NewKey(1, 1)
	if got := readVar(t, store, root, "out"); got == nil || got.Text != "done" {
		t.Fatalf("out = %+v, want \"done\"", got)
	}
	if got := readVar(t, store, root, "local"); got != nil {
		t.Errorf("local = %+v at the process scope, want nil (dropped activity-local)", got)
	}
}

// TestScriptTaskNoProcessLookup: if the compiled process can't be resolved, the
// handler errors and the job stays pending rather than silently completing.
func TestScriptTaskNoProcessLookup(t *testing.T) {
	store, log := openStore(t)
	cp, jobType := scriptProcess(t, `"x"`, "Greeting")

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// A lookup that never resolves the process.
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, script.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &fakeExec{result: "x"}))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v, want nil (failure routed to an incident, ADR-0061)", err)
	}
	if pi, ei := active(t, store); pi != 1 || ei != 1 {
		t.Fatalf("after failed lookup: process=%d element=%d, want 1 and 1 (parked with an incident)", pi, ei)
	}
}

// TestScriptTaskWithoutResultVarCompletes: a script task with no result variable
// runs for its side effects and completes without writing a variable. (The XML
// compiler requires a result variable; the programmatic builder does not, which is
// what lets this branch be exercised.)
func TestScriptTaskWithoutResultVarCompletes(t *testing.T) {
	store, log := openStore(t)
	cp, jobType := scriptProcess(t, `Write-Output "side effect"`, "")

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	fx := &fakeExec{result: "ignored"}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, script.Handler(store, lookupOf(cp), fx))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if fx.calls != 1 {
		t.Fatalf("exec calls = %d, want 1", fx.calls)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestScriptTaskExecErrorRaisesIncident: when the interpreter fails, the handler
// returns an error, so the job is failed rather than completed — it does not abort
// the drive but (retries exhausted) parks the instance on the script task with an
// incident (ADR-0061), the at-least-once contract every worker shares.
func TestScriptTaskExecErrorRaisesIncident(t *testing.T) {
	store, log := openStore(t)
	cp, jobType := scriptProcess(t, `throw "boom"`, "Greeting")

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	fx := &fakeExec{err: context.DeadlineExceeded}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, script.Handler(store, lookupOf(cp), fx))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v, want nil (failure routed to an incident)", err)
	}
	// The job is failed toward an incident: the instance is still parked on the
	// script task rather than completing.
	if pi, ei := active(t, store); pi != 1 || ei != 1 {
		t.Fatalf("after failed run: process=%d element=%d, want 1 and 1 (parked with an incident)", pi, ei)
	}
}
