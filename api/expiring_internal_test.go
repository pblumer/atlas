package api

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
)

// The arithmetic of an end, and what an empty answer means
// (ADR-0344).

// TestTheDistanceToAnEndIsOneNumberFromEitherSide.
//
// "In three days" and "eleven days ago" are the same question asked from either
// side of a date, so they are one signed number rather than two fields nobody can
// keep in step. Truncated towards zero, so today reads as zero from both
// directions: a right ending in eleven hours and one that ended eleven hours ago
// are both today's work, and rounding either into a whole day would move it to
// another day's list.
func TestTheDistanceToAnEndIsOneNumberFromEitherSide(t *testing.T) {
	const day = int64(24 * time.Hour)
	now := int64(1_000 * day)

	for _, tc := range []struct {
		name  string
		until int64
		want  int
	}{
		{"three days ahead", now + 3*day, -3},
		{"eleven days past", now - 11*day, 11},
		{"eleven hours ahead", now + 11*int64(time.Hour), 0},
		{"eleven hours past", now - 11*int64(time.Hour), 0},
		{"exactly now", now, 0},
	} {
		if got := daysBetween(now, tc.until); got != tc.want {
			t.Errorf("%s: days = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestAnEmptyAnswerSaysWhichEmptyItIs.
//
// An estate with no temporary access and an estate where no product declares a
// ceiling produce the same empty list, and the second is by far the more likely —
// every product has no ceiling until somebody sets one. An operator reading a bare
// empty list would conclude the first.
func TestAnEmptyAnswerSaysWhichEmptyItIs(t *testing.T) {
	nothingHasAnEnd := expiringReport{Within: 30}
	note := expiringNote(nothingHasAnEnd)
	if !strings.Contains(note, "no right in the inventory carries an end") {
		t.Errorf("note = %q, want it to say that nothing has an end at all", note)
	}
	if !strings.Contains(note, "maxDays") {
		t.Error("the note does not name the field that would give a right an end, so a reader " +
			"is told what is wrong and not what to do about it")
	}

	allFurtherOut := expiringReport{Within: 30}
	allFurtherOut.Counts.WithAnEnd = 7
	note = expiringNote(allFurtherOut)
	if !strings.Contains(note, "7 right(s) carry an end") {
		t.Errorf("note = %q, want the denominator — \"none are overdue\" means something "+
			"different when the answer is none out of none", note)
	}

	somethingFound := expiringReport{Within: 30, Rights: []expiringRight{{ItemID: "vpn"}}}
	if note := expiringNote(somethingFound); note != "" {
		t.Errorf("note = %q on an answer that found something; the note explains an empty "+
			"answer and only an empty one", note)
	}
}

// TestAnOverdueRightIsStillHeldAndSaysWhatCouldEndIt.
//
// The whole record in one rule. A right past its end is **not** removed and not
// hidden: the target system still has it, nothing has run, and what is true is that
// Atlas said the access should have ended and it has not. Dropping the record would
// make the inventory assert that somebody does not have access they demonstrably
// do, which is the direction of wrongness ADR-0334 calls the one that corrupts the
// evidence.
func TestAnOverdueRightIsStillHeldAndSaysWhatCouldEndIt(t *testing.T) {
	const day = int64(24 * time.Hour)
	now := int64(1_000 * day)

	past := model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_7",
		Since: now - 100*day, Until: now - 11*day, Origin: model.OriginOrdered,
	}
	row := classifyExpiring(past, now, true)
	switch {
	case !row.Overdue:
		t.Error("a right eleven days past its end does not read as overdue")
	case row.Days != 11:
		t.Errorf("days = %d, want 11 days of debt", row.Days)
	case row.Until != past.Until:
		t.Error("the row lost the end it is overdue against")
	case !row.Returnable:
		t.Error("the row does not say its order line is still there to return, which is what " +
			"actually ends it")
	case row.Origin != "ordered":
		t.Errorf("origin = %q, want the origin to travel — only an ordered right ever "+
			"carries an end", row.Origin)
	}

	// Ahead of its end: due, not overdue, and the distance is negative.
	ahead := past
	ahead.Until = now + 3*day
	row = classifyExpiring(ahead, now, true)
	if row.Overdue || row.Days != -3 {
		t.Errorf("a right due in three days reads overdue=%v days=%d", row.Overdue, row.Days)
	}
}

// TestARightNothingCanEndIsReportedRatherThanDropped.
//
// An entitlement deliberately outlives the order that produced it, and the instance
// behind that order is eligible for retention deletion long before a multi-year
// right ends. Such a right cannot be returned, so nothing will ever clear it —
// which makes hiding it the worst possible choice: the one kind that never goes
// away would also be the one kind nobody sees.
func TestARightNothingCanEndIsReportedRatherThanDropped(t *testing.T) {
	const day = int64(24 * time.Hour)
	now := int64(1_000 * day)

	orphan := model.EntitlementValue{
		Principal: "usr_ada", ItemID: "gone", OrderID: "ord_deleted",
		Since: now - 100*day, Until: now - day, Origin: model.OriginOrdered,
	}
	row := classifyExpiring(orphan, now, false)
	if !row.Overdue {
		t.Fatal("an overdue right with no process does not read as overdue")
	}
	if row.Returnable {
		t.Error("a right whose order is gone reads as returnable")
	}
	if row.ItemID != "gone" {
		t.Error("the row lost the product nothing can end")
	}
}
