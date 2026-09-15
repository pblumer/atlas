package api

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// What a conflict is, before anything reports it
// (ADR-0342).

func aConflictingHold(itemID string, since int64) model.EntitlementValue {
	return model.EntitlementValue{Principal: "usr_ada", ItemID: itemID, Since: since}
}

// TestAPairIsFoundFromEitherSideAndReportedOnce.
//
// The release stores both directions precisely so a one-sided check is impossible —
// and that makes a naive walk meet every pair twice. A report that listed a conflict
// under both its names would double every number an operator reads, which is the
// same defect the storage choice was made to prevent, arriving from the other end.
func TestAPairIsFoundFromEitherSideAndReportedOnce(t *testing.T) {
	excludes := map[string][]string{
		"create-supplier": {"approve-payment"},
		"approve-payment": {"create-supplier"},
	}
	got := conflictsFor("usr_ada", []model.EntitlementValue{
		aConflictingHold("create-supplier", 100),
		aConflictingHold("approve-payment", 200),
	}, excludes)

	if len(got) != 1 {
		t.Fatalf("%d finding(s), want the one pair reported once: %+v", len(got), got)
	}
	if got[0].A != "approve-payment" || got[0].B != "create-supplier" {
		t.Errorf("pair = %q/%q, want it sorted so the same pair reads the same way "+
			"however the walk met it", got[0].A, got[0].B)
	}
	if got[0].SinceA != 200 || got[0].SinceB != 100 {
		t.Errorf("dates = %d/%d, want each grant's own — they are the only hint at a "+
			"remedy a finding carries", got[0].SinceA, got[0].SinceB)
	}
}

// TestHoldingOneHalfIsNotAConflict.
//
// Neither right is wrong on its own. A check that reported the half would report
// most of an estate.
func TestHoldingOneHalfIsNotAConflict(t *testing.T) {
	excludes := map[string][]string{
		"create-supplier": {"approve-payment"},
		"approve-payment": {"create-supplier"},
	}
	if got := conflictsFor("usr_ada", []model.EntitlementValue{
		aConflictingHold("create-supplier", 100),
		aConflictingHold("vpn", 150),
	}, excludes); len(got) != 0 {
		t.Errorf("%d finding(s) for somebody holding one half: %+v", len(got), got)
	}
}

// TestTheSameProductHeldTwiceDatesFromTheFirstTime.
//
// The question a finding's dates answer is how long the *combination* has existed.
// A second grant of the same product did not start it, and taking the later one
// would report a long-standing conflict as new — which is exactly backwards for a
// list worked from the oldest.
func TestTheSameProductHeldTwiceDatesFromTheFirstTime(t *testing.T) {
	excludes := map[string][]string{"a": {"b"}, "b": {"a"}}
	got := conflictsFor("usr_ada", []model.EntitlementValue{
		aConflictingHold("a", 500),
		aConflictingHold("a", 100),
		aConflictingHold("b", 300),
	}, excludes)

	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].SinceA != 100 {
		t.Errorf("sinceA = %d, want the earliest hold of that product", got[0].SinceA)
	}
}

// TestTheOldestCombinationIsFirst.
//
// A combination begins when its *second* half is granted — it does not exist before
// that — so the age of a finding is the later of its two dates, and the list starts
// where nobody has looked for longest.
func TestTheOldestCombinationIsFirst(t *testing.T) {
	findings := []conflictFinding{
		{Principal: "usr_new", A: "a", B: "b", SinceA: 10, SinceB: 900},
		{Principal: "usr_old", A: "a", B: "b", SinceA: 100, SinceB: 200},
	}
	sortConflicts(findings)

	if findings[0].Principal != "usr_old" {
		t.Errorf("first = %q; the combination that has stood longest is the one nobody has "+
			"looked at, and a list worked from the top should start there", findings[0].Principal)
	}
	if older(findings[0]) != 200 || older(findings[1]) != 900 {
		t.Errorf("ages = %d/%d, want the later grant of each pair — a combination does not "+
			"exist until both halves do", older(findings[0]), older(findings[1]))
	}
}

// TestAPairIsNamedTheSameWayFromEitherSide.
func TestAPairIsNamedTheSameWayFromEitherSide(t *testing.T) {
	if pairKey("b", "a") != pairKey("a", "b") {
		t.Error("a pair has two names, so nothing can count it once")
	}
}
