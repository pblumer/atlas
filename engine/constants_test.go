package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// Process constants (ADR-draft-process-constants): seeded into every instance however it
// was started, before its start variables, and written by nothing in the run afterwards
// but an operator's audited correction.

// tenantConstant is a string constant named TenantId with the given value, in the form
// the compiler hands the engine.
func tenantConstant(value string) compiler.Constant {
	return compiler.Constant{Name: "TenantId", Type: "string", Value: value, Kind: expr.KindString, Text: value}
}

// constantsProcess builds Start → PowerShell job script (result "result") → message catch
// "consts.more" → message catch "consts.end" → End, declaring the constants TenantId
// ("tenant-dev") and MaxAgents (20). The two catches keep the instance alive, so a test
// can read its variables after each write it makes.
func constantsProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(9, "consts", 1)
	b.SetConstants([]compiler.Constant{
		tenantConstant("tenant-dev"),
		{Name: "MaxAgents", Type: "number", Value: "20", Kind: expr.KindNumber, Text: "20"},
	})
	start := b.AddStartEvent()
	sj := b.AddScriptJobTask(compiler.PwshJobType, "powershell", `Write-Output 1`, "result", 3)
	more := b.AddMessageCatchEvent("consts.more", mustCompile(t, `"k"`))
	end := b.AddMessageCatchEvent("consts.end", mustCompile(t, `"k"`))
	b.Connect(start, sj)
	b.Connect(sj, more)
	b.Connect(more, end)
	b.Connect(end, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ScriptJobTask(cp.Node(sj).Detail).JobType
}

func wantVar(t *testing.T, h *harness, scope uint64, name string, kind model.VarKind, text string) {
	t.Helper()
	got := readVar(t, h.store, scope, name)
	if got == nil || got.Kind != kind || got.Text != text {
		t.Errorf("%s = %+v, want kind %v text %q", name, got, kind, text)
	}
}

func run(t *testing.T, p *engine.Processor) {
	t.Helper()
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
}

// An instance carries its process's constants, and a start variable naming one does not
// replace it: the caller's value is dropped, its other start variables are kept.
func TestConstantsAreSeededAndOutrankStartVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := constantsProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("TenantId", "from-the-caller"), strVar("Other", "x"))
	run(t, p)

	pi := onlyInstance(t, h.store)
	wantVar(t, h, pi, "TenantId", model.VarString, "tenant-dev")
	wantVar(t, h, pi, "MaxAgents", model.VarNumber, "20")
	wantVar(t, h, pi, "Other", model.VarString, "x")
}

// What arrives later — a worker's result, a correlated message's payload — cannot replace
// a constant either, and it does not park the instance: the write is dropped and the rest
// of the result is written. An operator's correction does go through. And all of it
// survives a restart, because the constant is in the log like any variable.
func TestConstantsHoldAgainstLaterWritesAndRecover(t *testing.T) {
	dir := t.TempDir()
	clock := &manualClock{}
	cp, jobType := constantsProcess(t)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	run(t, p1)
	pi := onlyInstance(t, h1.store)

	// The worker returns its result and, as some do, a field named like the constant.
	p1.CompleteJob(singleActivatableJob(t, h1.store, jobType), strVar("TenantId", "from-the-worker"), strVar("result", "ok"))
	run(t, p1)
	if n := len(incidents(t, h1.store)); n != 0 {
		t.Fatalf("incidents after the worker's result = %d, want 0 (a dropped write parks nothing)", n)
	}
	if procs, els := counts(t, h1.store); procs != 1 || els != 1 {
		t.Fatalf("after the worker's result: process=%d element=%d, want 1 and 1 (waiting at the first catch)", procs, els)
	}
	wantVar(t, h1, pi, "TenantId", model.VarString, "tenant-dev")
	wantVar(t, h1, pi, "result", model.VarString, "ok")

	// Crash and recover: the constant is rebuilt from the log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	wantVar(t, h2, pi, "TenantId", model.VarString, "tenant-dev")
	wantVar(t, h2, pi, "MaxAgents", model.VarNumber, "20")

	// An operator's correction is the one later write that goes through (ADR-0095).
	p2.SetVariables(pi, 0, "admin", strVar("TenantId", "corrected"))
	run(t, p2)
	wantVar(t, h2, pi, "TenantId", model.VarString, "corrected")

	// A message payload is dropped like a worker's result, and the correction stands.
	p2.PublishMessage("consts.more", "k", strVar("TenantId", "from-a-message"), strVar("note", "n"))
	run(t, p2)
	if procs, els := counts(t, h2.store); procs != 1 || els != 1 {
		t.Fatalf("after the message: process=%d element=%d, want 1 and 1 (waiting at the second catch)", procs, els)
	}
	wantVar(t, h2, pi, "TenantId", model.VarString, "corrected")
	wantVar(t, h2, pi, "note", model.VarString, "n")
}

// A message start and a call activity create instances without the API, and each instance
// still carries its own process's constants: the start payload naming one is dropped, the
// child keeps its own value although the caller passes it all of its variables, and the
// caller keeps its own when the child hands all of its variables back.
func TestConstantsHoldOnMessageStartsAndAcrossCallActivities(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cb := compiler.NewBuilder(8, "child-proc", 1)
	cb.SetConstants([]compiler.Constant{tenantConstant("child")})
	cs := cb.AddStartEvent()
	ct := cb.AddScriptTask(mustCompile(t, "TenantId"), "seenByChild")
	cb.Connect(cs, ct)
	cb.Connect(ct, cb.AddEndEvent())
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}

	b := compiler.NewBuilder(9, "parent", 1)
	b.SetConstants([]compiler.Constant{tenantConstant("parent")})
	start := b.AddMessageStartEvent("parent.go", nil, false)
	call := b.AddCallActivity("child-proc", compiler.BindingLatest, true /*propParent*/, true /*propChild*/)
	wait := b.AddMessageCatchEvent("parent.end", mustCompile(t, `"k"`))
	b.Connect(start, call)
	b.Connect(call, wait)
	b.Connect(wait, b.AddEndEvent())
	parentCp, err := b.Build()
	if err != nil {
		t.Fatalf("parent Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(parentCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.PublishMessage("parent.go", "", strVar("TenantId", "from-the-payload"), strVar("x", "1"))
	run(t, p)

	pi := onlyInstance(t, h.store) // the child has finished; the parent waits at its catch
	wantVar(t, h, pi, "TenantId", model.VarString, "parent")
	wantVar(t, h, pi, "x", model.VarString, "1")
	// The child read its own constant, not the value the caller passed it.
	wantVar(t, h, pi, "seenByChild", model.VarString, "child")
}

// A process without constants is untouched: a start variable of any name is written.
func TestNoConstantsLeavesStartVariablesAlone(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _, _ := scriptJobProcess(t, 7)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("TenantId", "from-the-caller"))
	run(t, p)
	wantVar(t, h, onlyInstance(t, h.store), "TenantId", model.VarString, "from-the-caller")
}

// appBoundProcess builds Start → message catch "app.more" → End with the constants
// TenantId (bound to the application, no fallback) and Language (bound, fallback "de-CH")
// (ADR-draft-application-values-per-installation).
func appBoundProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(9, "app-bound", 1)
	b.SetConstants([]compiler.Constant{
		{Name: "TenantId", Type: "string", FromApplication: true},
		{Name: "Language", Type: "string", Value: "de-CH", Kind: expr.KindString, Text: "de-CH", FromApplication: true, HasFallback: true},
	})
	start := b.AddStartEvent()
	wait := b.AddMessageCatchEvent("app.more", mustCompile(t, `"k"`))
	b.Connect(start, wait)
	b.Connect(wait, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// A bound constant takes the value its application holds on this server, frozen into the
// instance when it is created: a later change reaches the next instance and not this one,
// and recovery rebuilds the value from the log without the operator config being there.
func TestApplicationValuesAreFrozenIntoTheInstance(t *testing.T) {
	dir := t.TempDir()
	clock := &manualClock{}
	cp := appBoundProcess(t)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.SetDefinitionApplication(cp.Key, "ucc")
	p1.SetApplicationValues("ucc", map[string]engine.ApplicationValue{
		"TenantId": {Kind: model.VarString, Text: "tenant-int"},
		"Language": {Kind: model.VarString, Text: "fr-CH"},
	})
	p1.CreateInstance(cp.Key, strVar("TenantId", "from-the-caller"))
	run(t, p1)
	first := onlyInstance(t, h1.store)
	wantVar(t, h1, first, "TenantId", model.VarString, "tenant-int")
	wantVar(t, h1, first, "Language", model.VarString, "fr-CH")

	// The operator changes the value: the running instance keeps what it started with.
	p1.SetApplicationValues("ucc", map[string]engine.ApplicationValue{"TenantId": {Kind: model.VarString, Text: "tenant-new"}})
	p1.PublishMessage("app.more", "k") // the first instance completes
	run(t, p1)
	p1.CreateInstance(cp.Key)
	run(t, p1)
	second := onlyInstance(t, h1.store)
	wantVar(t, h1, second, "TenantId", model.VarString, "tenant-new")
	// Language is no longer held by the application, so the second instance has the fallback.
	wantVar(t, h1, second, "Language", model.VarString, "de-CH")

	// Crash and recover with no operator config pushed at all: the instance's values are
	// facts in its log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	wantVar(t, h2, second, "TenantId", model.VarString, "tenant-new")
	wantVar(t, h2, second, "Language", model.VarString, "de-CH")
}

// Without a value from its application — the definition filed under none, or the
// application holding none — a bound constant takes its fallback, and one without a
// fallback is not seeded at all.
func TestApplicationBoundConstantsWithoutAValue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := appBoundProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	run(t, p)
	pi := onlyInstance(t, h.store)
	wantVar(t, h, pi, "Language", model.VarString, "de-CH")
	if got := readVar(t, h.store, pi, "TenantId"); got != nil {
		t.Errorf("TenantId = %+v, want it absent: no value and no fallback", got)
	}
}
