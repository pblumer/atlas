package api

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// twoNodeProcess builds a tiny compiled process whose nodes carry real BPMN ids, so the
// resolver's id-based comparisons have something to compare. ids[i] names node i.
func twoNodeProcess(t *testing.T, ids ...string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	for _, id := range ids {
		b.SetElementBpmnId(b.AddUserTask("", "", "", "", 0, 0, 1), id)
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return cp
}

// TestVersionAtChainsMigrationsIntoSpans is the resolver's whole contract: no migration
// record says which definition it moved *to*, because the next one's source is that
// definition and the last one's is the instance's own. Getting the chain wrong by one
// mislabels a whole span of history, so it is asserted position by position.
func TestVersionAtChainsMigrationsIntoSpans(t *testing.T) {
	cps := map[uint64]*compiler.CompiledProcess{
		11: twoNodeProcess(t, "a"),
		22: twoNodeProcess(t, "b"),
		33: twoNodeProcess(t, "c"),
	}
	v := newVersionAt(
		// Deliberately out of order: the caller reads them off a scan, and the resolver
		// is what puts them in order.
		[]migrationBoundary{{pos: 200, from: 22}, {pos: 100, from: 11}},
		33,
		func(k uint64) *compiler.CompiledProcess { return cps[k] },
	)
	for _, tc := range []struct {
		pos  uint64
		want uint64
		why  string
	}{
		{0, 11, "before the first migration: the first migration's source"},
		{99, 11, "just before the first migration"},
		{100, 22, "at the first migration: already the version it moved to"},
		{150, 22, "between the two: the second migration's source, named by neither the instance nor the first hop"},
		{199, 22, "just before the second migration"},
		{200, 33, "at the second migration"},
		{1 << 40, 33, "long after: the definition the instance is on now"},
	} {
		if got := v.def(tc.pos); got != tc.want {
			t.Errorf("def(%d) = %d, want %d — %s", tc.pos, got, tc.want, tc.why)
		}
	}
	if !v.migrated() {
		t.Error("migrated() is false for an instance with two migrations")
	}
}

// TestVersionAtWithoutMigrationsIsTheCurrentVersion keeps the ordinary case honest: an
// instance that never moved must resolve every position to the one definition it has.
func TestVersionAtWithoutMigrationsIsTheCurrentVersion(t *testing.T) {
	cp := twoNodeProcess(t, "only")
	v := newVersionAt(nil, 7, func(uint64) *compiler.CompiledProcess { return cp })
	if v.migrated() {
		t.Error("migrated() is true with no migrations")
	}
	for _, pos := range []uint64{0, 1, 1 << 30, ^uint64(0)} {
		if got := v.def(pos); got != 7 {
			t.Errorf("def(%d) = %d, want 7", pos, got)
		}
		if v.cp(pos) != cp {
			t.Errorf("cp(%d) is not the instance's compiled process", pos)
		}
	}
}

// TestVersionAtAnswersEmptyForADeletedDefinition is the case that makes the whole
// resolver safe to use: once an instance migrates away, its old version has no instances
// left and may be deleted. Every read through it must answer "unknown" rather than fall
// back to the current graph, where the same index is a different element.
func TestVersionAtAnswersEmptyForADeletedDefinition(t *testing.T) {
	current := twoNodeProcess(t, "now")
	v := newVersionAt([]migrationBoundary{{pos: 50, from: 11}}, 22,
		func(k uint64) *compiler.CompiledProcess {
			if k == 22 {
				return current
			}
			return nil // 11 was deleted after the instance left it
		})

	if v.cp(10) != nil {
		t.Error("a deleted definition resolves to a compiled process")
	}
	if got := v.elementID(10, 0); got != "" {
		t.Errorf("elementID under a deleted definition = %q, want empty — naming it wrong is worse than not naming it", got)
	}
	if got := v.nodeType(10, 0); got != "" {
		t.Errorf("nodeType under a deleted definition = %q, want empty", got)
	}
	if _, ok := v.node(10, 0); ok {
		t.Error("node() reports success under a deleted definition")
	}
	if v.isType(10, 0, compiler.TypeUserTask) {
		t.Error("isType is true under a deleted definition; an unidentifiable element is not of any type")
	}
	if _, ok := v.flowSource(10, 0); ok {
		t.Error("flowSource resolves under a deleted definition")
	}
	// A leaf answers true so the frame fold drops the token instead of deferring it
	// forever — the ghost ADR-0136 removed.
	if !v.isLeaf(10, 0) {
		t.Error("isLeaf is false under a deleted definition; a token that can never be handed on would be stranded")
	}
	// The surviving side still reads normally.
	if got := v.elementID(60, 0); got != "now" {
		t.Errorf("elementID on the live side = %q, want %q", got, "now")
	}
}

// TestVersionAtComparesElementsByIndexWithinAVersionAndByIdAcross is the rule the frame
// fold's deferral link rests on. Within one definition an index is exact. Across a
// migration two indices come from different graphs and comparing them is meaningless —
// but comparing by BPMN id must not become the default, because a builder-built process
// has no ids at all and every element would compare equal to every other.
func TestVersionAtComparesElementsByIndexWithinAVersionAndByIdAcross(t *testing.T) {
	// v1: index 0 = "review". v2: index 0 = "triage", index 1 = "review".
	v1, v2 := twoNodeProcess(t, "review"), twoNodeProcess(t, "triage", "review")
	cps := map[uint64]*compiler.CompiledProcess{1: v1, 2: v2}
	v := newVersionAt([]migrationBoundary{{pos: 100, from: 1}}, 2,
		func(k uint64) *compiler.CompiledProcess { return cps[k] })

	if !v.sameElement(10, 0, 20, 0) {
		t.Error("two rows in the same version with the same index are not the same element")
	}
	if v.sameElement(10, 0, 20, 1) {
		t.Error("two rows in the same version with different indices compare equal")
	}
	// Across the boundary: v1's index 0 and v2's index 1 are both "review".
	if !v.sameElement(10, 0, 200, 1) {
		t.Error(`across a migration, v1's "review" and v2's "review" are not recognised as the same element`)
	}
	// v1's index 0 ("review") is not v2's index 0 ("triage"), even though the indices match.
	if v.sameElement(10, 0, 200, 0) {
		t.Error("across a migration, equal indices are being treated as the same element")
	}

	// Without BPMN ids there is nothing to compare across a boundary, and answering
	// "equal" would collapse every element into one.
	blank1, blank2 := compiler.NewBuilder(1, "p", 1), compiler.NewBuilder(2, "p", 2)
	blank1.AddUserTask("", "", "", "", 0, 0, 1)
	blank2.AddUserTask("", "", "", "", 0, 0, 1)
	b1, err := blank1.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	b2, err := blank2.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	blanks := map[uint64]*compiler.CompiledProcess{1: b1, 2: b2}
	bv := newVersionAt([]migrationBoundary{{pos: 100, from: 1}}, 2,
		func(k uint64) *compiler.CompiledProcess { return blanks[k] })
	if bv.sameElement(10, 0, 200, 0) {
		t.Error("elements with no BPMN ids compare equal across a migration; every element would match every other")
	}
}

// TestVersionAtFindsCallActivitiesInAnyVersion: asking only the current version would
// miss a call activity that ran before a migration, dropping the drill-in link on
// exactly the step that has one.
func TestVersionAtFindsCallActivitiesInAnyVersion(t *testing.T) {
	plain := twoNodeProcess(t, "task")
	withCall := func() *compiler.CompiledProcess {
		b := compiler.NewBuilder(1, "caller", 1)
		b.AddCallActivity("child", compiler.BindingLatest, false, false)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return cp
	}()

	cps := map[uint64]*compiler.CompiledProcess{1: withCall, 2: plain}
	v := newVersionAt([]migrationBoundary{{pos: 100, from: 1}}, 2,
		func(k uint64) *compiler.CompiledProcess { return cps[k] })
	if !v.anyHasCallActivities() {
		t.Error("a call activity in the version the instance left is not found")
	}

	only := newVersionAt(nil, 2, func(k uint64) *compiler.CompiledProcess { return cps[k] })
	if only.anyHasCallActivities() {
		t.Error("a process with no call activity reports one")
	}
}
