package evidence_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/pblumer/atlas/workertype/evidence"
	"github.com/pblumer/atlas/workertype/evidence/suites/calendarbooking"
)

// TestTheDraftedCalendarContractsYieldCurrentRecords derives the record that would bind
// each calendar operation to its drafted contract, exactly as publishing will, and
// verifies it against the module. Nothing is written to workertype/evidence/records:
// the contracts are unpublished until ADR-0483's trust boundary is in force.
func TestTheDraftedCalendarContractsYieldCurrentRecords(t *testing.T) {
	ctx := context.Background()
	for _, b := range []struct {
		contract, operation, suite string
		notExercised               int
	}{
		{"calendar-slot-find.json", "find-free-slots", calendarbooking.NameFind, 0},
		{"calendar-booking-reserve.json", "reserve-slot", calendarbooking.NameReserve, 1},
		{"calendar-booking-confirm.json", "confirm-booking", calendarbooking.NameConfirm, 0},
		{"calendar-booking-cancel.json", "cancel-booking", calendarbooking.NameCancel, 0},
		{"calendar-booking-get.json", "get-booking", calendarbooking.NameGet, 0},
	} {
		rec, err := evidence.Create(ctx, "../..", evidence.Binding{
			Contract:   "workertype/evidence/testdata/contracts/" + b.contract,
			WorkerType: "atlas.calendar",
			Operation:  b.operation,
			Suite:      b.suite,
		})
		if err != nil {
			t.Fatalf("%s: Create: %v", b.operation, err)
		}
		if rec.Operation.WorkerType != "atlas.calendar" || rec.Operation.JobType != "io.atlas.calendar" || rec.Contract.Profile != "free-busy-calendar" {
			t.Fatalf("%s: record binds %+v / %+v", b.operation, rec.Operation, rec.Contract)
		}
		counts := map[string]int{}
		for _, r := range rec.Conformance.Results {
			counts[r.Outcome]++
		}
		if counts[evidence.OutcomeFailed] != 0 || counts[evidence.OutcomeNotExercised] != b.notExercised {
			t.Fatalf("%s: outcomes %v", b.operation, counts)
		}
		raw, err := rec.Render()
		if err != nil {
			t.Fatal(err)
		}
		if err := evidence.Verify(ctx, "../..", raw); err != nil {
			t.Fatalf("%s: the derived record is not current: %v", b.operation, err)
		}
		again, err := evidence.Create(ctx, "../..", evidence.Binding{
			Contract: rec.Contract.Path, WorkerType: rec.Operation.WorkerType, Operation: rec.Operation.ID, Suite: rec.Conformance.Suite,
		})
		if err != nil {
			t.Fatal(err)
		}
		if raw2, _ := again.Render(); !bytes.Equal(raw, raw2) {
			t.Fatalf("%s: deriving the record twice gives different bytes", b.operation)
		}
	}
}
