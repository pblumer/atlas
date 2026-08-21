package jobtype_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
)

// The reserved range must be able to grow without ever reaching an index already
// issued to a model-authored type. That is what a fixed floor buys: adding a
// built-in connector moves the reserved count, never the boundary new assignments
// are made from.

// The floor sits far above the reserved names, so a great many connectors can be
// added before it could matter again.
func TestTheDynamicFloorLeavesRoomForNewBuiltIns(t *testing.T) {
	reserved := int32(len(compiler.ReservedJobTypes()))
	if got := compiler.ReservedJobTypeCount(); got != reserved {
		t.Fatalf("ReservedJobTypeCount() = %d, want %d", got, reserved)
	}
	floor := compiler.FirstDynamicJobTypeIndex()
	if floor <= reserved {
		t.Fatalf("floor %d is not above the %d reserved names; the range could grow into issued indices", floor, reserved)
	}
	if room := floor - reserved; room < 100 {
		t.Errorf("only %d indices of headroom before the floor; the point is that it never runs out in practice", room)
	}
}

// A name interned now lands at or above the floor, so no future built-in can take
// its index.
func TestANewNameIsIssuedAtOrAboveTheFloor(t *testing.T) {
	r, err := jobtype.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	idx, err := r.Intern("send-email")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if idx < compiler.FirstDynamicJobTypeIndex() {
		t.Errorf("index %d is below the floor %d", idx, compiler.FirstDynamicJobTypeIndex())
	}
}

// TestALegacyAssignmentBelowTheFloorIsKept is the trap the floor sets. Stores
// written before it exists hold names between the reserved count and the floor —
// perfectly valid assignments whose jobs are on disk. Treating "below the floor" as
// "reserved" would drop every one of them, which is the very failure the floor is
// meant to prevent, applied to far more names at once.
func TestALegacyAssignmentBelowTheFloorIsKept(t *testing.T) {
	dir := t.TempDir()
	legacy := compiler.ReservedJobTypeCount() + 1 // an ordinary old dynamic index
	seedStored(t, dir, jobtype.Entry{Name: "send-email", Index: legacy})

	r, err := jobtype.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	idx, ok := r.Index("send-email")
	if !ok {
		t.Fatal("a legacy assignment below the floor was dropped; its parked jobs would be orphaned")
	}
	if idx != legacy {
		t.Errorf("index = %d, want the stored %d — an index is never reissued", idx, legacy)
	}
	if len(r.Dropped()) != 0 {
		t.Errorf("dropped = %+v, want none: this store is healthy", r.Dropped())
	}
	// And re-interning it is idempotent rather than moving it up to the floor.
	again, err := r.Intern("send-email")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if again != legacy {
		t.Errorf("Intern moved an existing name from %d to %d", legacy, again)
	}
}

// Only an index inside the reserved range is a collision. The floor is where new
// names start, not what makes an old one invalid.
func TestOnlyTheReservedRangeCounsAsACollision(t *testing.T) {
	dir := t.TempDir()
	seedStored(t, dir,
		jobtype.Entry{Name: "collided", Index: 0},                                 // inside the reserved range
		jobtype.Entry{Name: "legacy", Index: compiler.ReservedJobTypeCount() + 1}, // below the floor, fine
		jobtype.Entry{Name: "modern", Index: compiler.FirstDynamicJobTypeIndex()}, // at the floor, fine
	)
	got, err := jobtype.Collisions(dir)
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "collided" {
		t.Errorf("collisions = %+v, want only the one inside the reserved range", got)
	}
}
