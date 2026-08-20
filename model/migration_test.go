package model

import (
	"errors"
	"reflect"
	"testing"
)

// TestProcessMigrationRoundTrip covers the migration payload end to end: both
// definition keys and the whole element mapping survive an encode/decode, which is the
// only thing standing between a migrated instance and a replay that lands somewhere
// else (ADR-0162).
func TestProcessMigrationRoundTrip(t *testing.T) {
	want := NewProcessMigration(NewKey(1, 7), 100, 200, map[int32]int32{4: 9, 1: 2, 12: 12})
	raw := AppendValue(nil, &want)

	v, err := DecodeValue(VTProcessMigration, raw)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	got, ok := v.(*ProcessMigrationValue)
	if !ok {
		t.Fatalf("DecodeValue returned %T, want *ProcessMigrationValue", v)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round trip = %+v, want %+v", *got, want)
	}
	if got.ValueType() != VTProcessMigration {
		t.Errorf("ValueType() = %v, want VTProcessMigration", got.ValueType())
	}
}

// TestProcessMigrationMappingIsOrdered is the reproducibility property: the mapping
// comes from a map, and a map has no order, so an event whose bytes depended on Go's
// iteration order would be an event whose log is not byte-reproducible. Building the
// same mapping repeatedly must produce the same bytes.
func TestProcessMigrationMappingIsOrdered(t *testing.T) {
	m := map[int32]int32{9: 1, 3: 4, 7: 7, 1: 0, 5: 6}
	first := AppendValue(nil, ptr(NewProcessMigration(1, 2, 3, m)))
	for i := 0; i < 50; i++ {
		again := AppendValue(nil, ptr(NewProcessMigration(1, 2, 3, m)))
		if string(again) != string(first) {
			t.Fatalf("encoding is not stable across builds of the same mapping")
		}
	}
	v := NewProcessMigration(1, 2, 3, m)
	for i := 1; i < len(v.Mapping); i++ {
		if v.Mapping[i-1].From >= v.Mapping[i].From {
			t.Fatalf("mapping is not ordered by source index: %+v", v.Mapping)
		}
	}
}

// TestProcessMigrationTarget covers the lookup the fold uses per record: a covered
// index resolves, an uncovered one reports so rather than answering 0 — which is a
// real element index, and silently rebinding a token to it is exactly the corruption
// this design refuses.
func TestProcessMigrationTarget(t *testing.T) {
	v := NewProcessMigration(1, 2, 3, map[int32]int32{2: 20, 5: 50, 9: 90})
	for from, want := range map[int32]int32{2: 20, 5: 50, 9: 90} {
		got, ok := v.Target(from)
		if !ok || got != want {
			t.Errorf("Target(%d) = %d, %v; want %d, true", from, got, ok, want)
		}
	}
	for _, from := range []int32{-1, 0, 1, 3, 6, 10, 1000} {
		if got, ok := v.Target(from); ok {
			t.Errorf("Target(%d) = %d, true; want not covered", from, got)
		}
	}
	// The empty mapping covers nothing, and must not panic looking.
	empty := NewProcessMigration(1, 2, 3, nil)
	if _, ok := empty.Target(0); ok {
		t.Error("an empty mapping should cover no index")
	}
}

// TestProcessMigrationDecodeErrors covers the truncation and bound guards. The
// mapping is the only variable-length payload the engine folds in bulk, so a
// half-written or hostile count must be refused rather than sized into an allocation.
func TestProcessMigrationDecodeErrors(t *testing.T) {
	full := AppendValue(nil, ptr(NewProcessMigration(NewKey(1, 1), 10, 20, map[int32]int32{1: 2, 3: 4})))
	for _, n := range []int{0, 8, 27, 28, 32, len(full) - 1} {
		if n >= len(full) {
			continue
		}
		var v ProcessMigrationValue
		if err := v.decode(full[:n]); !errors.Is(err, ErrShortBuffer) {
			t.Errorf("decode(truncated to %d) err = %v, want ErrShortBuffer", n, err)
		}
	}
	// A count past the bound is refused before it is believed.
	hostile := append([]byte{}, full[:24]...)
	hostile = append(hostile, byte(0xFF), byte(0xFF), byte(0xFF), byte(0x7F)) // ~2.1 billion pairs
	var v ProcessMigrationValue
	if err := v.decode(hostile); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("decode(absurd mapping count) err = %v, want ErrShortBuffer", err)
	}
}

// TestOperatorActionFromDefKeyIsAppendCompatible covers the field ADR-0162 adds to
// ADR-0159's audit record. A record written before it must still decode — to 0, which
// is what every non-migration action means by it — or every audit record already on
// disk becomes unreadable.
func TestOperatorActionFromDefKeyIsAppendCompatible(t *testing.T) {
	withKey := OperatorActionValue{
		ProcessInstanceKey: NewKey(1, 1), Kind: OperatorActionMigrate,
		Actor: "admin", Reason: "fixed the mail template", FromProcessDefKey: 4242,
	}
	v, err := DecodeValue(VTOperatorAction, AppendValue(nil, &withKey))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if got := v.(*OperatorActionValue); *got != withKey {
		t.Fatalf("round trip = %+v, want %+v", *got, withKey)
	}

	// The old layout, byte for byte: the fixed prefix and the two strings, and nothing
	// after them. This is what is on disk in every audit record written before ADR-0162.
	old := AppendValue(nil, &OperatorActionValue{
		ProcessInstanceKey: NewKey(1, 2), ElementInstanceKey: NewKey(1, 3), JobKey: NewKey(1, 4),
		Kind: OperatorActionCompleteJob, Actor: "admin", Reason: "done by hand",
	})
	old = old[:len(old)-8] // drop the appended field, as a pre-0162 writer would have
	v, err = DecodeValue(VTOperatorAction, old)
	if err != nil {
		t.Fatalf("DecodeValue(pre-0162 record): %v", err)
	}
	got := v.(*OperatorActionValue)
	if got.FromProcessDefKey != 0 {
		t.Errorf("FromProcessDefKey on a pre-0162 record = %d, want 0", got.FromProcessDefKey)
	}
	if got.Reason != "done by hand" || got.Actor != "admin" || got.Kind != OperatorActionCompleteJob {
		t.Errorf("pre-0162 record decoded to %+v, want its original fields", *got)
	}
}

// TestMigrationLabels covers the labels the log and the operator surfaces read.
func TestMigrationLabels(t *testing.T) {
	if got := VTProcessMigration.String(); got != "ProcessMigration" {
		t.Errorf("VTProcessMigration.String() = %q", got)
	}
	if got := IntentMigrating.String(); got != "Migrating" {
		t.Errorf("IntentMigrating.String() = %q", got)
	}
	if got := IntentMigrated.String(); got != "Migrated" {
		t.Errorf("IntentMigrated.String() = %q", got)
	}
	if got := OperatorActionMigrate.String(); got != "migrate" {
		t.Errorf("OperatorActionMigrate.String() = %q", got)
	}
}

func ptr(v ProcessMigrationValue) *ProcessMigrationValue { return &v }
