package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
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

// TestValidateMigrationRefusesABrokenRaceGroup covers the event-based gateway rule: the
// first armed catch to fire cancels the others by their shared gateway key (ADR-0110),
// so a member mapped without its gateway would leave losers nothing can cancel.
func TestValidateMigrationRefusesABrokenRaceGroup(t *testing.T) {
	v1, v2 := simpleVersions(t)
	live := []engine.MigrationElement{
		{Key: 40, ElementId: 0, BpmnElementType: uint8(compiler.TypeStartEvent)},
		{Key: 41, ElementId: 1, EventGatewayKey: 40, BpmnElementType: uint8(compiler.TypeServiceTask)},
	}
	// The catch is mapped; the gateway that arms it is not. The gateway reports its own
	// unmapped token too, so the assertion is about the *second* problem — the one this
	// rule exists for.
	problems := engine.ValidateMigration(v1, v2, live, map[int32]int32{1: 2})
	if len(problems) != 2 {
		t.Fatalf("problems = %+v, want one for the unmapped gateway and one for the broken group", problems)
	}
	if !strings.Contains(problems[1].Reason, "race group") {
		t.Errorf("reason = %q, want it to name the race group", problems[1].Reason)
	}
}

// TestValidateMigrationRefusesAnUnmappedParentOrHost covers the two "the thing I hang
// off is not coming with me" arms — a scope and a boundary host — which are distinct
// from the scope/attachment *changing*, and are what an operator hits when they map one
// element by hand and forget its container.
func TestValidateMigrationRefusesAnUnmappedParentOrHost(t *testing.T) {
	v1, v2 := simpleVersions(t)

	// Parent scope present as a live element instance, but absent from the mapping.
	scoped := []engine.MigrationElement{
		{Key: 50, ElementId: 1, BpmnElementType: uint8(compiler.TypeServiceTask)},
		{Key: 51, ElementId: 2, FlowScopeKey: 50, BpmnElementType: uint8(compiler.TypeEndEvent)},
	}
	problems := engine.ValidateMigration(v1, v2, scoped, map[int32]int32{2: 3})
	if len(problems) != 2 {
		t.Fatalf("problems = %+v, want one for the unmapped scope and one for the scope itself", problems)
	}
	if !strings.Contains(problems[1].Reason, "sits inside") {
		t.Errorf("reason = %q, want it to name the unmapped container", problems[1].Reason)
	}

	// Boundary host present, but absent from the mapping.
	attached := []engine.MigrationElement{
		{Key: 60, ElementId: 1, BpmnElementType: uint8(compiler.TypeServiceTask)},
		{Key: 61, ElementId: 2, AttachedToKey: 60, BpmnElementType: uint8(compiler.TypeBoundaryEvent)},
	}
	problems = engine.ValidateMigration(v1, v2, attached, map[int32]int32{2: 3})
	if len(problems) != 2 || !strings.Contains(problems[1].Reason, "is attached to") {
		t.Fatalf("problems = %+v, want one naming the unmapped host", problems)
	}
}

// TestValidateMigrationRefusesAMultiInstanceChange covers the ADR-0077 rule from both
// sides: a token running as a multi-instance activity cannot land on a plain one, and a
// plain activity cannot become multi-instance under a live token either — the scope's
// child counts are keyed on which it is.
func TestValidateMigrationRefusesAMultiInstanceChange(t *testing.T) {
	build := func(key uint64, version int32, multi bool) (*compiler.CompiledProcess, int32) {
		b := compiler.NewBuilder(key, "mi", version)
		s := b.AddStartEvent()
		task := b.AddServiceTask(jobName, 3)
		if multi {
			coll, err := expr.CompileAuto("items")
			if err != nil {
				t.Fatalf("CompileAuto: %v", err)
			}
			b.SetMultiInstance(task, false, "item", "", coll, nil, nil, nil)
		}
		b.Connect(s, task)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return cp, task
	}
	multi, multiTask := build(501, 1, true)
	plain, plainTask := build(502, 2, false)

	// An iteration of a multi-instance activity, mapped onto a plain one.
	iterating := []engine.MigrationElement{
		{Key: 70, ElementId: multiTask, MultiInstance: 2, BpmnElementType: uint8(compiler.TypeServiceTask)},
	}
	only(t, engine.ValidateMigration(multi, plain, iterating, map[int32]int32{0: 0, multiTask: plainTask}),
		"multi-instance activity but maps to")

	// And the shape change on its own, with no token running as an iteration: still a
	// refusal, because the target's scope accounting differs.
	single := []engine.MigrationElement{
		{Key: 71, ElementId: plainTask, BpmnElementType: uint8(compiler.TypeServiceTask)},
	}
	only(t, engine.ValidateMigration(plain, multi, single, map[int32]int32{0: 0, plainTask: multiTask}),
		"changes between a multi-instance and a plain activity")
}

// TestValidateMigrationAcceptsABoundaryThatStaysAttached is the positive half of the
// attachment rule — without it, the loop that searches the host's boundary list is only
// ever exercised failing, and a bug that never found a match would pass unnoticed.
func TestValidateMigrationAcceptsABoundaryThatStaysAttached(t *testing.T) {
	build := func(key uint64, version int32) (*compiler.CompiledProcess, int32, int32) {
		b := compiler.NewBuilder(key, "bnd", version)
		s := b.AddStartEvent()
		host := b.AddServiceTask(jobName, 3)
		bnd := b.AddBoundaryTimerEvent(host, true, 1000)
		b.Connect(s, host)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return cp, host, bnd
	}
	v1, host1, bnd1 := build(601, 1)
	v2, host2, bnd2 := build(602, 2)

	live := []engine.MigrationElement{
		{Key: 80, ElementId: host1, BpmnElementType: uint8(compiler.TypeServiceTask)},
		{Key: 81, ElementId: bnd1, AttachedToKey: 80, BpmnElementType: uint8(compiler.TypeBoundaryEvent)},
	}
	mapping := map[int32]int32{0: 0, host1: host2, bnd1: bnd2}
	if p := engine.ValidateMigration(v1, v2, live, mapping); len(p) != 0 {
		t.Fatalf("a boundary event that stays on its host was refused: %+v", p)
	}
}
