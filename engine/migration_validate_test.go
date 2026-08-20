package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The validator is a pure function of two compiled graphs, the live element instances,
// and a mapping — so these tests hand it exactly that, rather than driving an engine to
// produce the shapes. It is what makes the awkward cases (a boundary event detached in
// the target, a token in a subprocess whose scope moves) reachable at all: the engine
// would refuse to *create* most of them, and the validator's job is to refuse to
// *migrate into* them.

// simpleVersions builds Start → ServiceTask → End twice, with a task inserted in v2, so
// indices differ between the two — the ordinary case a default mapping covers.
func simpleVersions(t testing.TB) (v1, v2 *compiler.CompiledProcess) {
	t.Helper()
	b1 := compiler.NewBuilder(101, "v", 1)
	s1 := b1.AddStartEvent()
	t1 := b1.AddServiceTask(jobName, 3)
	e1 := b1.AddEndEvent()
	b1.Connect(s1, t1)
	b1.Connect(t1, e1)
	cp1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	b2 := compiler.NewBuilder(102, "v", 2)
	s2 := b2.AddStartEvent()
	a2 := b2.AddServiceTask("audit", 3)
	t2 := b2.AddServiceTask(jobName, 3)
	e2 := b2.AddEndEvent()
	b2.Connect(s2, a2)
	b2.Connect(a2, t2)
	b2.Connect(t2, e2)
	cp2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	return cp1, cp2
}

// only asserts there is exactly one problem and that it says what it should.
func only(t *testing.T, problems []engine.MigrationProblem, want string) {
	t.Helper()
	if len(problems) != 1 {
		t.Fatalf("problems = %+v, want exactly one", problems)
	}
	if !strings.Contains(problems[0].Reason, want) {
		t.Errorf("reason = %q, want it to mention %q", problems[0].Reason, want)
	}
}

func TestValidateMigrationAcceptsAWellMappedToken(t *testing.T) {
	v1, v2 := simpleVersions(t)
	live := []engine.MigrationElement{{Key: 1, ElementId: 1, BpmnElementType: uint8(compiler.TypeServiceTask)}}
	if problems := engine.ValidateMigration(v1, v2, live, map[int32]int32{0: 0, 1: 2, 2: 3}); len(problems) != 0 {
		t.Fatalf("a complete, type-preserving mapping was refused: %+v", problems)
	}
}

func TestValidateMigrationRefusesAnUnmappedToken(t *testing.T) {
	v1, v2 := simpleVersions(t)
	live := []engine.MigrationElement{{Key: 1, ElementId: 1}}
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{0: 0}), "stranded")
}

func TestValidateMigrationRefusesAnIndexTheTargetDoesNotHave(t *testing.T) {
	v1, v2 := simpleVersions(t)
	live := []engine.MigrationElement{{Key: 1, ElementId: 1}}
	// Out of range in both directions: Node would index the slice directly and panic.
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{1: 999}), "does not have")
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{1: -1}), "does not have")
}

func TestValidateMigrationRefusesAChangeOfElementType(t *testing.T) {
	v1, v2 := simpleVersions(t)
	// v1's service task mapped onto v2's start event: the element instance's stored
	// type is what the engine dispatches on, so this token would run the wrong behavior.
	live := []engine.MigrationElement{{Key: 1, ElementId: 1}}
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{1: 0}), "which is a")
}

// TestValidateMigrationRefusesAScopeChange covers the rule that keeps a token inside
// the subprocess it entered: a token whose parent scope maps somewhere the target does
// not nest it under has moved between scopes, which is a different instance rather
// than a rebinding of this one.
func TestValidateMigrationRefusesAScopeChange(t *testing.T) {
	// v1: Start → SubProcess[ Task ] ; v2 flattens the task out of the subprocess.
	b1 := compiler.NewBuilder(201, "scoped", 1)
	s1 := b1.AddStartEvent()
	sub := b1.AddSubProcess()
	b1.PushScope(sub)
	innerStart := b1.AddStartEvent()
	inner := b1.AddServiceTask(jobName, 3)
	b1.Connect(innerStart, inner)
	b1.PopScope()
	b1.Connect(s1, sub)
	v1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	b2 := compiler.NewBuilder(202, "scoped", 2)
	s2 := b2.AddStartEvent()
	sub2 := b2.AddSubProcess()
	flat := b2.AddServiceTask(jobName, 3)
	b2.Connect(s2, sub2)
	b2.Connect(sub2, flat)
	v2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}

	live := []engine.MigrationElement{
		{Key: 10, ElementId: sub, BpmnElementType: uint8(compiler.TypeSubProcess)},
		{Key: 11, ElementId: inner, FlowScopeKey: 10, BpmnElementType: uint8(compiler.TypeServiceTask)},
	}
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{s1: s2, sub: sub2, inner: flat}),
		"different scope")
}

// TestValidateMigrationRefusesADetachedBoundaryEvent covers the rule that keeps a
// boundary event on its host: an armed boundary whose target is attached elsewhere (or
// nowhere) could no longer interrupt the activity it is watching.
func TestValidateMigrationRefusesADetachedBoundaryEvent(t *testing.T) {
	b1 := compiler.NewBuilder(301, "bnd", 1)
	s1 := b1.AddStartEvent()
	host1 := b1.AddServiceTask(jobName, 3)
	bnd1 := b1.AddBoundaryTimerEvent(host1, true, 1000)
	b1.Connect(s1, host1)
	v1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	// v2 has two tasks, and the boundary is attached to the *other* one.
	b2 := compiler.NewBuilder(302, "bnd", 2)
	s2 := b2.AddStartEvent()
	other := b2.AddServiceTask("other", 3)
	host2 := b2.AddServiceTask(jobName, 3)
	bnd2 := b2.AddBoundaryTimerEvent(other, true, 1000)
	b2.Connect(s2, other)
	b2.Connect(other, host2)
	v2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}

	live := []engine.MigrationElement{
		{Key: 20, ElementId: host1, BpmnElementType: uint8(compiler.TypeServiceTask)},
		{Key: 21, ElementId: bnd1, AttachedToKey: 20, BpmnElementType: uint8(compiler.TypeBoundaryEvent)},
	}
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{s1: s2, host1: host2, bnd1: bnd2}),
		"no longer be attached")
}

// TestValidateMigrationRefusesAChangedMessageName covers the subscription-identity
// rule. A message subscription is keyed by the message *name*, not merely labelled
// with it, so a target waiting for something else leaves the armed subscription stale
// and the instance waiting forever on a name its model no longer mentions.
func TestValidateMigrationRefusesAChangedMessageName(t *testing.T) {
	build := func(key uint64, version int32, message string) (*compiler.CompiledProcess, int32) {
		b := compiler.NewBuilder(key, "msg", version)
		s := b.AddStartEvent()
		catch := b.AddMessageCatchEvent(message, nil)
		b.Connect(s, catch)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return cp, catch
	}
	v1, catch1 := build(401, 1, "order-paid")
	v2, catch2 := build(402, 2, "order-settled")

	live := []engine.MigrationElement{{Key: 30, ElementId: catch1, BpmnElementType: uint8(compiler.TypeMessageCatchEvent)}}
	only(t, engine.ValidateMigration(v1, v2, live, map[int32]int32{0: 0, catch1: catch2}), "stale")

	// The same name is fine — an element that merely moved index still resolves.
	v3, catch3 := build(403, 3, "order-paid")
	if p := engine.ValidateMigration(v1, v3, live, map[int32]int32{0: 0, catch1: catch3}); len(p) != 0 {
		t.Errorf("an unchanged message name was refused: %+v", p)
	}
}

func TestValidateMigrationRefusesAnUndeployedDefinition(t *testing.T) {
	v1, v2 := simpleVersions(t)
	only(t, engine.ValidateMigration(nil, v2, nil, nil), "not deployed")
	only(t, engine.ValidateMigration(v1, nil, nil, nil), "not deployed")
}

// TestMigrationElementOfProjectsWhatTheValidatorReads keeps the projection honest: the
// validator's view of a live element instance must carry every field its rules read,
// or a rule silently stops applying.
func TestMigrationElementOfProjectsWhatTheValidatorReads(t *testing.T) {
	ei := &model.ElementInstanceValue{
		ElementId: 4, FlowScopeKey: 11, BpmnElementType: 9,
		MultiInstance: 2, EventGatewayKey: 12, AttachedToKey: 13,
	}
	got := engine.MigrationElementOf(7, ei)
	want := engine.MigrationElement{
		Key: 7, ElementId: 4, FlowScopeKey: 11, BpmnElementType: 9,
		MultiInstance: 2, EventGatewayKey: 12, AttachedToKey: 13,
	}
	if got != want {
		t.Errorf("MigrationElementOf = %+v, want %+v", got, want)
	}
}
