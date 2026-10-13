package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// Start-variable defaults (ADR-0455): a declared start variable
// a start did not supply takes the default the process declares, on every path that
// starts an instance — and only those. A fork continues an instance; it does not start one.

// declaredWithDefaults is the declaration the tests share, in the form the compiler hands
// the engine: Region and MaxAgents and DryRun have defaults, Note has none.
func declaredWithDefaults() []compiler.StartVariable {
	return []compiler.StartVariable{
		{Name: "Region", Type: "string", Default: "CH", HasDefault: true, Kind: expr.KindString, Text: "CH"},
		{Name: "MaxAgents", Type: "number", Default: "20", HasDefault: true, Kind: expr.KindNumber, Text: "20"},
		{Name: "DryRun", Type: "boolean", Default: "true", HasDefault: true, Kind: expr.KindBool, Bool: true},
		{Name: "Note", Type: "string"},
	}
}

// defaultsProcess builds start → message catch "defaults.end" → end, declaring
// declaredWithDefaults. With messageStart the start is a message start event named
// "defaults.go" instead of a none start. The catch keeps the instance alive so a test can
// read its variables.
func defaultsProcess(t *testing.T, key uint64, messageStart bool) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "defaults", 1)
	b.SetStartVariables(declaredWithDefaults())
	var start int32
	if messageStart {
		start = b.AddMessageStartEvent("defaults.go", nil, false)
	} else {
		start = b.AddStartEvent()
	}
	wait := b.AddMessageCatchEvent("defaults.end", mustCompile(t, `"k"`))
	b.Connect(start, wait)
	b.Connect(wait, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func startedProcessor(t *testing.T, h *harness, cps ...*compiler.CompiledProcess) *engine.Processor {
	t.Helper()
	p := engine.New(1, h.log, h.store, &manualClock{})
	for _, cp := range cps {
		p.Deploy(cp)
	}
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

// A start that supplies nothing gets every default; one it supplies keeps its value, a
// null it supplies stays null — supplied means named — and a declaration without a
// default leaves the variable out, as before.
func TestStartVariableDefaultsFillWhatAStartDidNotSupply(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := defaultsProcess(t, defKey, false)
	p := startedProcessor(t, h, cp)

	p.CreateInstance(cp.Key, strVar("Region", "DE"), model.VariableValue{Name: "MaxAgents", Kind: model.VarNull})
	run(t, p)

	pi := onlyInstance(t, h.store)
	wantVar(t, h, pi, "Region", model.VarString, "DE")
	if got := readVar(t, h.store, pi, "MaxAgents"); got == nil || got.Kind != model.VarNull {
		t.Errorf("MaxAgents = %+v, want the null the start supplied", got)
	}
	if got := readVar(t, h.store, pi, "DryRun"); got == nil || got.Kind != model.VarBool || !got.Bool {
		t.Errorf("DryRun = %+v, want the default true", got)
	}
	if got := readVar(t, h.store, pi, "Note"); got != nil {
		t.Errorf("Note = %+v, want no variable: it declares no default", got)
	}
}

// A message start has no form and no caller that read the declaration, which is exactly
// the start the default was missing from.
func TestStartVariableDefaultsReachAMessageStart(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := defaultsProcess(t, defKey, true)
	p := startedProcessor(t, h, cp)

	p.PublishMessage("defaults.go", "", strVar("x", "1"))
	run(t, p)

	pi := onlyInstance(t, h.store)
	wantVar(t, h, pi, "Region", model.VarString, "CH")
	wantVar(t, h, pi, "MaxAgents", model.VarNumber, "20")
	wantVar(t, h, pi, "x", model.VarString, "1")
}

// A fork carries the variables its predecessor reached and nothing else: a default the
// target version declares for a variable the predecessor never had is not added.
func TestAForkDoesNotTakeStartVariableDefaults(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b1 := compiler.NewBuilder(defKey, "forking-defaults", 1)
	s1 := b1.AddStartEvent()
	parked := b1.AddServiceTask(jobName, 3)
	b1.Connect(s1, parked)
	b1.Connect(parked, b1.AddEndEvent())
	v1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	b2 := compiler.NewBuilder(defKey+1, "forking-defaults", 2)
	b2.SetStartVariables(declaredWithDefaults())
	s2 := b2.AddStartEvent()
	resume := b2.AddServiceTask(jobName, 3)
	b2.Connect(s2, resume)
	b2.Connect(resume, b2.AddEndEvent())
	v2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}

	p := startedProcessor(t, h, v1, v2)
	p.CreateInstance(v1.Key, strVar("Region", "DE"))
	run(t, p)
	oldKey := model.NewKey(1, 1)
	p.ForkInstance(oldKey, v2.Key, []int32{resume}, "admin", "continue in v2")
	run(t, p)

	newKey, _ := successorOf(t, h.store, oldKey)
	wantVar(t, h, newKey, "Region", model.VarString, "DE")
	if got := readVar(t, h.store, newKey, "MaxAgents"); got != nil {
		t.Errorf("MaxAgents on the successor = %+v, want none: a fork is not a start", got)
	}
}
