package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// Process constants (ADR-0454): seeded into every instance however it
// was started, before its start variables, and written by nothing in the run afterwards
// but an operator's audited correction.

// tenantConstant is a string constant named TENANT_ID with the given value, in the form
// the compiler hands the engine.
func tenantConstant(value string) compiler.Constant {
	return compiler.Constant{Name: "TENANT_ID", Type: "string", Value: value, Kind: expr.KindString, Text: value}
}

// constantsProcess builds Start → PowerShell job script (result "result") → message catch
// "consts.more" → message catch "consts.end" → End, declaring the constants TENANT_ID
// ("tenant-dev") and MAX_AGENTS (20). The two catches keep the instance alive, so a test
// can read its variables after each write it makes.
func constantsProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(9, "consts", 1)
	b.SetConstants([]compiler.Constant{
		tenantConstant("tenant-dev"),
		{Name: "MAX_AGENTS", Type: "number", Value: "20", Kind: expr.KindNumber, Text: "20"},
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
	p.CreateInstance(cp.Key, strVar("TENANT_ID", "from-the-caller"), strVar("Other", "x"))
	run(t, p)

	pi := onlyInstance(t, h.store)
	wantVar(t, h, pi, "TENANT_ID", model.VarString, "tenant-dev")
	wantVar(t, h, pi, "MAX_AGENTS", model.VarNumber, "20")
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
	p1.CompleteJob(singleActivatableJob(t, h1.store, jobType), strVar("TENANT_ID", "from-the-worker"), strVar("result", "ok"))
	run(t, p1)
	if n := len(incidents(t, h1.store)); n != 0 {
		t.Fatalf("incidents after the worker's result = %d, want 0 (a dropped write parks nothing)", n)
	}
	if procs, els := counts(t, h1.store); procs != 1 || els != 1 {
		t.Fatalf("after the worker's result: process=%d element=%d, want 1 and 1 (waiting at the first catch)", procs, els)
	}
	wantVar(t, h1, pi, "TENANT_ID", model.VarString, "tenant-dev")
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
	wantVar(t, h2, pi, "TENANT_ID", model.VarString, "tenant-dev")
	wantVar(t, h2, pi, "MAX_AGENTS", model.VarNumber, "20")

	// An operator's correction is the one later write that goes through (ADR-0095).
	p2.SetVariables(pi, 0, "admin", strVar("TENANT_ID", "corrected"))
	run(t, p2)
	wantVar(t, h2, pi, "TENANT_ID", model.VarString, "corrected")

	// A message payload is dropped like a worker's result, and the correction stands.
	p2.PublishMessage("consts.more", "k", strVar("TENANT_ID", "from-a-message"), strVar("note", "n"))
	run(t, p2)
	if procs, els := counts(t, h2.store); procs != 1 || els != 1 {
		t.Fatalf("after the message: process=%d element=%d, want 1 and 1 (waiting at the second catch)", procs, els)
	}
	wantVar(t, h2, pi, "TENANT_ID", model.VarString, "corrected")
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
	ct := cb.AddScriptTask(mustCompile(t, "TENANT_ID"), "seenByChild")
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
	p.PublishMessage("parent.go", "", strVar("TENANT_ID", "from-the-payload"), strVar("x", "1"))
	run(t, p)

	pi := onlyInstance(t, h.store) // the child has finished; the parent waits at its catch
	wantVar(t, h, pi, "TENANT_ID", model.VarString, "parent")
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
	p.CreateInstance(cp.Key, strVar("TENANT_ID", "from-the-caller"))
	run(t, p)
	wantVar(t, h, onlyInstance(t, h.store), "TENANT_ID", model.VarString, "from-the-caller")
}

// appBoundProcess builds Start → FEEL script task (application.TENANT_ID → seen) →
// message catch "app.more" → End with the constants TENANT_ID (bound to the application,
// no fallback) and LANGUAGE (bound, fallback "de-CH") (ADR-0453).
func appBoundProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(9, "app-bound", 1)
	b.SetConstants([]compiler.Constant{
		{Name: "TENANT_ID", Type: "string", Namespace: compiler.ApplicationNamespace},
		{Name: "LANGUAGE", Type: "string", Value: "de-CH", Kind: expr.KindString, Text: "de-CH", Namespace: compiler.ApplicationNamespace, HasFallback: true},
	})
	start := b.AddStartEvent()
	read := b.AddScriptTask(mustCompile(t, "application.TENANT_ID"), "seen")
	wait := b.AddMessageCatchEvent("app.more", mustCompile(t, `"k"`))
	b.Connect(start, read)
	b.Connect(read, wait)
	b.Connect(wait, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// The constants bound to the application are the members of one variable, the
// application namespace, read as application.NAME (ADR-0453, amended). Each takes the value
// its application holds on this server, frozen into the instance when it is created: a
// later change reaches the next instance and not this one, and recovery rebuilds the value
// from the log without the operator config being there. A start variable naming the
// namespace does not replace it, and one naming a member is the process's own variable.
func TestApplicationConstantsAreFrozenIntoTheNamespace(t *testing.T) {
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
	p1.SetApplicationConstants("ucc", map[string]engine.ConstantValue{
		"TENANT_ID": {Kind: model.VarString, Text: "tenant-int"},
		"LANGUAGE":  {Kind: model.VarString, Text: "fr-CH"},
	})
	p1.CreateInstance(cp.Key, strVar("application", "from-the-caller"), strVar("TENANT_ID", "own"))
	run(t, p1)
	first := onlyInstance(t, h1.store)
	wantVar(t, h1, first, "application", model.VarJSON, `{"LANGUAGE":"fr-CH","TENANT_ID":"tenant-int"}`)
	wantVar(t, h1, first, "seen", model.VarString, "tenant-int")
	wantVar(t, h1, first, "TENANT_ID", model.VarString, "own")

	// The operator changes the constants: the running instance keeps what it started with.
	p1.SetApplicationConstants("ucc", map[string]engine.ConstantValue{"TENANT_ID": {Kind: model.VarString, Text: "tenant-new"}})
	p1.PublishMessage("app.more", "k") // the first instance completes
	run(t, p1)
	p1.CreateInstance(cp.Key)
	run(t, p1)
	second := onlyInstance(t, h1.store)
	// LANGUAGE is no longer held by the application, so the second instance has the fallback.
	wantVar(t, h1, second, "application", model.VarJSON, `{"LANGUAGE":"de-CH","TENANT_ID":"tenant-new"}`)
	wantVar(t, h1, second, "seen", model.VarString, "tenant-new")

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
	wantVar(t, h2, second, "application", model.VarJSON, `{"LANGUAGE":"de-CH","TENANT_ID":"tenant-new"}`)
}

// Without a value from its application — the definition filed under none, or the
// application holding none — a bound constant takes its fallback, and one without a
// fallback is absent from the namespace, so application.NAME reads null. The namespace
// itself is always there, so a script fails on the one member it lacks and not on the
// object.
func TestApplicationConstantsWithoutAValue(t *testing.T) {
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
	wantVar(t, h, pi, "application", model.VarJSON, `{"LANGUAGE":"de-CH"}`)
	if got := readVar(t, h.store, pi, "seen"); got == nil || got.Kind != model.VarNull {
		t.Errorf("seen = %+v, want null: TENANT_ID has no value and no fallback", got)
	}
}

// Moving a definition to another application is a configuration change like saving the
// constants: the cached namespace is dropped, and the next instance reads the new
// application's.
func TestMovingADefinitionRebuildsItsNamespace(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := appBoundProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.SetApplicationConstants("a", map[string]engine.ConstantValue{"TENANT_ID": {Kind: model.VarString, Text: "in-a"}})
	p.SetApplicationConstants("b", map[string]engine.ConstantValue{"TENANT_ID": {Kind: model.VarString, Text: "in-b"}})
	p.SetDefinitionApplication(cp.Key, "a")
	p.CreateInstance(cp.Key)
	run(t, p)
	first := onlyInstance(t, h.store)
	wantVar(t, h, first, "seen", model.VarString, "in-a")
	p.PublishMessage("app.more", "k")
	run(t, p)

	p.SetDefinitionApplication(cp.Key, "b")
	p.CreateInstance(cp.Key)
	run(t, p)
	wantVar(t, h, onlyInstance(t, h.store), "seen", model.VarString, "in-b")
}

// The server namespace carries the installation's constants for every application, and
// both namespaces carry the facts the process declares from them — the node's environment,
// the application's name — in lower case (ADR-draft-constant-namespaces). A fact not set
// on this server falls back like a missing constant; renaming the node or the application
// reaches the next instance.
func TestServerConstantsAndFactsReachTheNamespaces(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(9, "facts", 1)
	b.SetConstants([]compiler.Constant{
		{Name: "TENANT_ID", Type: "string", Namespace: compiler.ServerNamespace},
		{Name: "environment", Type: "string", Namespace: compiler.ServerNamespace, Info: true},
		{Name: "name", Type: "string", Value: "unnamed", Kind: expr.KindString, Text: "unnamed", Namespace: compiler.ServerNamespace, Info: true, HasFallback: true},
		{Name: "name", Type: "string", Namespace: compiler.ApplicationNamespace, Info: true},
		{Name: "id", Type: "string", Namespace: compiler.ApplicationNamespace, Info: true},
	})
	start := b.AddStartEvent()
	wait := b.AddMessageCatchEvent("facts.more", mustCompile(t, `"k"`))
	b.Connect(start, wait)
	b.Connect(wait, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.SetServerConstants(map[string]engine.ConstantValue{"TENANT_ID": {Kind: model.VarString, Text: "contoso-prod"}})
	p.SetServerInfo(engine.ServerInfo{ID: "node-1", Environment: "PROD"})
	p.SetDefinitionApplication(cp.Key, "app-1")
	p.SetApplicationInfo("app-1", engine.ApplicationInfo{Name: "UCC"})
	p.CreateInstance(cp.Key)
	run(t, p)
	first := onlyInstance(t, h.store)
	wantVar(t, h, first, "server", model.VarJSON, `{"TENANT_ID":"contoso-prod","environment":"PROD","name":"unnamed"}`)
	wantVar(t, h, first, "application", model.VarJSON, `{"id":"app-1","name":"UCC"}`)
	p.PublishMessage("facts.more", "k")
	run(t, p)

	p.SetServerInfo(engine.ServerInfo{ID: "node-1", Name: "Zurich primary", Environment: "PROD"})
	p.SetApplicationInfo("app-1", engine.ApplicationInfo{Name: "UCC Sammelanschlüsse"})
	p.CreateInstance(cp.Key)
	run(t, p)
	second := onlyInstance(t, h.store)
	wantVar(t, h, second, "server", model.VarJSON, `{"TENANT_ID":"contoso-prod","environment":"PROD","name":"Zurich primary"}`)
	wantVar(t, h, second, "application", model.VarJSON, `{"id":"app-1","name":"UCC Sammelanschlüsse"}`)
}
