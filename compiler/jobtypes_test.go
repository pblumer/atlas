package compiler

import (
	"errors"
	"testing"
)

// TestReservedJobTypesMatchTheirIndices is the drift guard for the one table two
// packages must agree on: the builder reserves these names in this order so each
// lands on its documented index, and the engine-wide job-type registry seeds
// itself from the same list. A reorder here silently re-points every job already
// written with the old index, so the constants are asserted against the list's
// positions rather than trusted.
func TestReservedJobTypesMatchTheirIndices(t *testing.T) {
	want := map[string]int32{
		DMNJobType:           DMNJobTypeIndex,
		UserTaskJobType:      UserTaskJobTypeIndex,
		PwshJobType:          PwshJobTypeIndex,
		TemisDecisionJobType: TemisDecisionJobTypeIndex,
		RestJobType:          RestJobTypeIndex,
		PythonJobType:        PythonJobTypeIndex,
		JsJobType:            JsJobTypeIndex,
		ClioWriteJobType:     ClioWriteJobTypeIndex,
		ClioQueryJobType:     ClioQueryJobTypeIndex,
		ClioReadJobType:      ClioReadJobTypeIndex,
		MailJobType:          MailJobTypeIndex,
		CsvImportJobType:     CsvImportJobTypeIndex,
		SharePointJobType:    SharePointJobTypeIndex,
		RemedyJobType:        RemedyJobTypeIndex,
		WebScrapeJobType:     WebScrapeJobTypeIndex,
		UserConnectorJobType: UserConnectorJobTypeIndex,
		ScimJobType:          ScimJobTypeIndex,
		LdapJobType:          LdapJobTypeIndex,
		SoapJobType:          SoapJobTypeIndex,
		AdJobType:            AdJobTypeIndex,
	}

	reserved := ReservedJobTypes()
	if len(reserved) != len(want) {
		t.Fatalf("ReservedJobTypes() has %d entries, want %d", len(reserved), len(want))
	}
	for i, name := range reserved {
		idx, ok := want[name]
		if !ok {
			t.Errorf("ReservedJobTypes()[%d] = %q, which has no documented index constant", i, name)
			continue
		}
		if idx != int32(i) {
			t.Errorf("%q sits at position %d but its constant says %d", name, i, idx)
		}
	}
	if got := FirstDynamicJobTypeIndex(); got != int32(len(reserved)) {
		t.Errorf("FirstDynamicJobTypeIndex() = %d, want %d", got, len(reserved))
	}
}

// TestReservedJobTypesAreInternedByEveryBuilder pins the property the indices
// depend on: a builder reserves all of them up front, so the index a reserved
// name interns to is the same in every compiled process, whatever the model
// contains. A model-authored type lands after them.
func TestReservedJobTypesAreInternedByEveryBuilder(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	for i, name := range ReservedJobTypes() {
		if got := b.intern(name); got != int32(i) {
			t.Errorf("intern(%q) = %d in a fresh builder, want %d", name, got, i)
		}
	}
	if got := b.intern("send-email"); got < FirstDynamicJobTypeIndex() {
		t.Errorf("a model-authored job type interned to %d, which collides with the reserved range below %d",
			got, FirstDynamicJobTypeIndex())
	}
}

// ReservedJobTypes must hand out a copy: a caller that sorts or truncates the
// result would otherwise re-point every reserved index in the process.
func TestReservedJobTypesIsACopy(t *testing.T) {
	first := ReservedJobTypes()
	first[0] = "clobbered"
	if again := ReservedJobTypes(); again[0] != DMNJobType {
		t.Errorf("ReservedJobTypes()[0] = %q after a caller wrote to an earlier result, want %q", again[0], DMNJobType)
	}
}

// TestResolveJobTypesAssignsGlobalIndices covers the seam an engine-wide registry
// plugs into: the compiled process hands each model-authored job-type *name* to an
// interner and keeps the index it gets back, so the job a service task creates
// carries an identity that means the same thing in every process.
func TestResolveJobTypesAssignsGlobalIndices(t *testing.T) {
	b := NewBuilder(1, "orders", 1)
	pay := b.AddServiceTask("charge-card", 3)
	mail := b.AddSendTask("send-email", 3)
	cp := mustBuild(t, b)

	global := map[string]int32{"charge-card": 41, "send-email": 42}
	if err := cp.ResolveJobTypes(func(name string) (int32, error) { return global[name], nil }); err != nil {
		t.Fatalf("ResolveJobTypes: %v", err)
	}

	for _, tc := range []struct {
		node int32
		want int32
	}{{pay, 41}, {mail, 42}} {
		d := cp.ServiceTask(cp.Node(tc.node).Detail)
		if got := d.GlobalJobType(); got != tc.want {
			t.Errorf("GlobalJobType() = %d, want %d", got, tc.want)
		}
	}
}

// TestResolveJobTypesSeparatesWhatInterningCollided is the bug this exists to fix.
// Two definitions intern their own strings, so the same local index means different
// job types in each — a worker subscribing by that index would be handed the wrong
// work. After resolution against one shared table they are distinct.
func TestResolveJobTypesSeparatesWhatInterningCollided(t *testing.T) {
	build := func(id, jobType string) (*CompiledProcess, int32) {
		b := NewBuilder(1, id, 1)
		n := b.AddServiceTask(jobType, 1)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return cp, n
	}
	a, aNode := build("orders", "send-email")
	c, cNode := build("billing", "charge-card")

	aLocal := a.ServiceTask(a.Node(aNode).Detail).JobType
	cLocal := c.ServiceTask(c.Node(cNode).Detail).JobType
	if aLocal != cLocal {
		t.Fatalf("precondition changed: the two processes interned to %d and %d, so this no longer reproduces the collision", aLocal, cLocal)
	}

	next := FirstDynamicJobTypeIndex()
	table := map[string]int32{}
	intern := func(name string) (int32, error) {
		if idx, ok := table[name]; ok {
			return idx, nil
		}
		table[name] = next
		next++
		return table[name], nil
	}
	for _, cp := range []*CompiledProcess{a, c} {
		if err := cp.ResolveJobTypes(intern); err != nil {
			t.Fatalf("ResolveJobTypes: %v", err)
		}
	}

	aGlobal := a.ServiceTask(a.Node(aNode).Detail).GlobalJobType()
	cGlobal := c.ServiceTask(c.Node(cNode).Detail).GlobalJobType()
	if aGlobal == cGlobal {
		t.Errorf("send-email and charge-card both resolved to %d — still colliding", aGlobal)
	}
	if aGlobal < FirstDynamicJobTypeIndex() || cGlobal < FirstDynamicJobTypeIndex() {
		t.Errorf("a model-authored type resolved into the reserved range: %d, %d", aGlobal, cGlobal)
	}
}

// An unresolved process must behave exactly as it did before resolution existed:
// the job carries the locally interned index. That keeps a compiled process usable
// on its own (tests, the conformance runner) instead of silently reporting job
// type 0, which is the DMN worker's.
func TestGlobalJobTypeFallsBackToLocalWhenUnresolved(t *testing.T) {
	b := NewBuilder(1, "orders", 1)
	n := b.AddServiceTask("charge-card", 1)
	cp := mustBuild(t, b)

	d := cp.ServiceTask(cp.Node(n).Detail)
	if got, want := d.GlobalJobType(), d.JobType; got != want {
		t.Errorf("GlobalJobType() = %d on an unresolved process, want the local %d", got, want)
	}
	if d.GlobalJobType() == DMNJobTypeIndex {
		t.Error("an unresolved service task reports the DMN job type, which would hand it to the DMN worker")
	}
}

// A registry that cannot assign an index fails the deployment rather than leaving
// a process half-resolved: a partially resolved table is worse than an unresolved
// one, because only some jobs would be findable.
func TestResolveJobTypesPropagatesInternerFailure(t *testing.T) {
	b := NewBuilder(1, "orders", 1)
	b.AddServiceTask("charge-card", 1)
	cp := mustBuild(t, b)

	wantErr := errors.New("registry full")
	if err := cp.ResolveJobTypes(func(string) (int32, error) { return 0, wantErr }); !errors.Is(err, wantErr) {
		t.Errorf("ResolveJobTypes error = %v, want it to wrap %v", err, wantErr)
	}
}

// Resolution is idempotent: reloading a deployment re-resolves every process, and
// the second pass must land on the same indices rather than consuming new ones.
func TestResolveJobTypesIsIdempotent(t *testing.T) {
	b := NewBuilder(1, "orders", 1)
	n := b.AddServiceTask("charge-card", 1)
	cp := mustBuild(t, b)

	calls := 0
	intern := func(string) (int32, error) { calls++; return 99, nil }
	for range 2 {
		if err := cp.ResolveJobTypes(intern); err != nil {
			t.Fatalf("ResolveJobTypes: %v", err)
		}
	}
	if got := cp.ServiceTask(cp.Node(n).Detail).GlobalJobType(); got != 99 {
		t.Errorf("GlobalJobType() = %d after re-resolving, want 99", got)
	}
	if calls != 2 {
		t.Errorf("interner called %d times for 2 resolutions of 1 task, want 2", calls)
	}
}

// mustBuild compiles a builder or fails the test — the tests above are about job
// types, not about compilation errors.
func mustBuild(t *testing.T, b *Builder) *CompiledProcess {
	t.Helper()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// Node ids are the dense range [0, NodeCount), which is what lets a caller outside
// the compiler walk a process — the Workers view does, to find which definitions
// use a job type.
func TestNodeCountCoversEveryNode(t *testing.T) {
	b := NewBuilder(1, "orders", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("send-email", 1)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp := mustBuild(t, b)

	if got := cp.NodeCount(); got != 3 {
		t.Fatalf("NodeCount() = %d, want 3", got)
	}
	seen := map[BpmnType]int{}
	for id := range int32(cp.NodeCount()) {
		seen[cp.Node(id).Type]++
	}
	for _, want := range []BpmnType{TypeStartEvent, TypeServiceTask, TypeEndEvent} {
		if seen[want] != 1 {
			t.Errorf("walking [0,NodeCount) saw %d nodes of type %v, want 1", seen[want], want)
		}
	}
}
