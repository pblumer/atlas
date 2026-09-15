package model

import (
	"testing"
	"time"
)

// What a closed hold says, before anything reads one
// (ADR-0346).

// TestACorrectedHoldIsNotEvidenceOfAccess.
//
// The distinction the whole family is built on. A returned hold is evidence the
// person had the access; a corrected one is evidence Atlas claimed they did and
// reconciliation found the target system disagreed. A reader that could not tell
// them apart would report, in a record kept for years, a period of access nobody
// can show existed.
func TestACorrectedHoldIsNotEvidenceOfAccess(t *testing.T) {
	if !EndReturned.Held() {
		t.Error("a returned hold is not evidence of access; then nothing is, and the " +
			"family records nothing an audit can use")
	}
	if EndCorrected.Held() {
		t.Error("a corrected hold reads as evidence of access — which asserts on Atlas's " +
			"behalf exactly what ADR-0334 had it decline to decide")
	}
	if EndReturned.String() != "returned" || EndCorrected.String() != "corrected" {
		t.Errorf("the words are %q/%q; a reader deciding whether to trust a row should "+
			"not have to look up a byte", EndReturned, EndCorrected)
	}
}

// TestAHoldCoversTheTimeItWasHeldAndNotTheMomentItEnded.
//
// Half-open on purpose. A right returned and re-granted in the same nanosecond
// would otherwise be counted twice at that moment, and a report that says
// somebody held one product twice at one instant is a report nobody can subtract.
func TestAHoldCoversTheTimeItWasHeldAndNotTheMomentItEnded(t *testing.T) {
	v := &EntitlementHistoryValue{Since: 100, EndedAt: 200}
	for _, tc := range []struct {
		at   int64
		want bool
		why  string
	}{
		{99, false, "before it began"},
		{100, true, "the moment it began is covered"},
		{199, true, "inside"},
		{200, false, "the moment it ended is not covered — the interval is half-open"},
		{201, false, "after"},
	} {
		if got := v.CoveredAt(tc.at); got != tc.want {
			t.Errorf("CoveredAt(%d) = %v, want %v: %s", tc.at, got, tc.want, tc.why)
		}
	}
}

// TestHowLateARightWasStillRecordedSurvivesTheReturn.
//
// The expiry report reads the live inventory, so the moment an overdue right is
// returned the report can no longer see it. After that this row is the only place
// the lateness exists at all.
func TestHowLateARightWasStillRecordedSurvivesTheReturn(t *testing.T) {
	day := int64(24 * time.Hour)
	late := &EntitlementHistoryValue{Until: 1000, EndedAt: 1000 + 40*day}
	if got := late.Overdue(); got != 40*day {
		t.Errorf("overdue = %d, want forty days — the only surviving trace that the "+
			"right outstayed the end it was granted with", got)
	}
	onTime := &EntitlementHistoryValue{Until: 1000, EndedAt: 900}
	if got := onTime.Overdue(); got != 0 {
		t.Errorf("a right returned early is %d overdue", got)
	}
	noEnd := &EntitlementHistoryValue{Until: 0, EndedAt: 1 << 40}
	if got := noEnd.Overdue(); got != 0 {
		t.Errorf("a right with no end is %d overdue; it was never due", got)
	}
}

// TestAClosedHoldSurvivesARoundTrip, every field of it.
//
// This family is written once and read years later, by which time nothing else
// about the hold exists. A field lost in the encoding is a fact lost for good.
func TestAClosedHoldSurvivesARoundTrip(t *testing.T) {
	want := EntitlementHistoryValue{
		Principal: "usr_ada", ItemID: "approve-payment", VariantID: "gold",
		OrderID: "ord_7", Since: 100, EndedAt: 900, Until: 500,
		Origin: OriginAdopted, EndedReason: EndCorrected, EndedBy: "usr_ops",
	}
	var got EntitlementHistoryValue
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Errorf("round trip lost something:\n got %+v\nwant %+v", got, want)
	}
}

// TestAClosedHoldKnowsItsOwnValueType.
//
// The fold dispatches on it, so a row that reported the inventory's type would
// be decoded as a live hold — silently, because the two share their first
// fields.
func TestAClosedHoldKnowsItsOwnValueType(t *testing.T) {
	v := &EntitlementHistoryValue{Principal: "usr_ada", ItemID: "vpn"}
	if v.ValueType() != VTEntitlementHistory {
		t.Errorf("ValueType = %v, want VTEntitlementHistory", v.ValueType())
	}
	if VTEntitlementHistory.String() != "EntitlementHistory" {
		t.Errorf("name = %q; a log somebody reads years later should say what the "+
			"record is", VTEntitlementHistory.String())
	}
	if newValue(VTEntitlementHistory) == nil {
		t.Error("the decoder has no value to decode into, so a replayed revocation " +
			"would be dropped")
	}
}

// TestARowThatNamesNobodyOrNothingIsRefused.
func TestARowThatNamesNobodyOrNothingIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    EntitlementHistoryValue
	}{
		{"nobody", EntitlementHistoryValue{ItemID: "vpn"}},
		{"nothing", EntitlementHistoryValue{Principal: "usr_ada"}},
		{"blank", EntitlementHistoryValue{Principal: "  ", ItemID: "vpn"}},
	} {
		if tc.v.Valid() {
			t.Errorf("%s: accepted a row every reader would count and none could subtract", tc.name)
		}
	}
}
