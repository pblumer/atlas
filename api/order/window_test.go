package order

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
)

// When a product may be ordered (ADR-0397).
//
// The field had been in the record since the catalogue was designed and nothing
// read it. These hold the three things that would let it go back to being a
// promise nobody keeps: the common product with no window, the two sides of a
// window that exists, and the boundary days themselves.

// at is a moment, written as a date so a test reads like the window it checks.
func at(date string) int64 {
	t, err := time.Parse(time.RFC3339, date)
	if err != nil {
		panic(err)
	}
	return t.UnixNano()
}

// windowed is a product orderable between two moments; zero means unbounded.
func windowed(id string, from, until int64) catalog.Item {
	return catalog.Item{ID: id, Lifecycle: catalog.Lifecycle{From: from, Until: until}}
}

// TestAProductWithNoWindowIsOrderableWhenever.
//
// The case that matters most, because getting it wrong refuses every catalogue
// that exists: a window is the exception, and zero on both sides is not "the epoch
// until the epoch", it is "no window".
func TestAProductWithNoWindowIsOrderableWhenever(t *testing.T) {
	rel := aReleaseWith(anItem("vpn"), anItem("laptop"))
	for _, moment := range []string{"1971-01-01T00:00:00Z", "2026-09-18T08:00:00Z",
		"2099-12-31T23:59:59Z"} {
		if got := closedIn(rel, []string{"vpn", "laptop"}, at(moment)); got != nil {
			t.Errorf("refused %+v at %s; a product with no window must stay orderable",
				got, moment)
		}
	}
}

// TestAWindowRefusesBeforeItOpensAndAfterItCloses.
func TestAWindowRefusesBeforeItOpensAndAfterItCloses(t *testing.T) {
	rel := aReleaseWith(windowed("aktion",
		at("2026-10-01T00:00:00Z"), at("2026-10-31T23:59:59Z")))

	early := closedIn(rel, []string{"aktion"}, at("2026-09-30T23:59:59Z"))
	if early == nil {
		t.Fatal("a product was ordered the day before its window opened")
	}
	if !early.opensLater() {
		t.Error("the refusal reads as a window that has ended; before and after are " +
			"opposite advice — wait, or stop looking")
	}
	if reason := early.reason(); !contains(reason, "2026-10-01") {
		t.Errorf("the refusal does not name the day it opens: %q", reason)
	}

	late := closedIn(rel, []string{"aktion"}, at("2026-11-01T00:00:00Z"))
	if late == nil {
		t.Fatal("a product was ordered the day after its window closed")
	}
	if late.opensLater() {
		t.Error("the refusal reads as a window not yet open, on a window that has ended")
	}
	if reason := late.reason(); !contains(reason, "2026-10-31") {
		t.Errorf("the refusal does not name the day it closed: %q", reason)
	}
}

// TestBothBoundaryDaysAreInside.
//
// The off-by-one that would make the feature quietly wrong: a maintainer who
// writes "until the 31st" means the product is orderable on the 31st. The Console
// stores the end of that day, and the comparison has to let the whole of it
// through — the first hour and the last.
func TestBothBoundaryDaysAreInside(t *testing.T) {
	rel := aReleaseWith(windowed("aktion",
		at("2026-10-01T00:00:00Z"), at("2026-10-31T23:59:59Z")))

	for _, moment := range []string{
		"2026-10-01T00:00:00Z", // the instant it opens
		"2026-10-01T09:17:00Z", // the first day
		"2026-10-31T00:00:01Z", // the last day, early
		"2026-10-31T23:59:59Z", // the instant it closes
	} {
		if got := closedIn(rel, []string{"aktion"}, at(moment)); got != nil {
			t.Errorf("refused at %s, which is inside the window: %+v", moment, got)
		}
	}
}

// TestOneOpenSideIsUnbounded.
//
// Zero is not a date. A product orderable from a launch date with no end, and one
// orderable until a cut-off with no start, are both ordinary.
func TestOneOpenSideIsUnbounded(t *testing.T) {
	launch := aReleaseWith(windowed("neu", at("2026-10-01T00:00:00Z"), 0))
	if got := closedIn(launch, []string{"neu"}, at("2099-01-01T00:00:00Z")); got != nil {
		t.Errorf("a window with no end refused a far later moment: %+v", got)
	}
	if got := closedIn(launch, []string{"neu"}, at("2026-09-01T00:00:00Z")); got == nil {
		t.Error("a window with no end was ordered before it opened")
	}

	sunset := aReleaseWith(windowed("alt", 0, at("2026-10-31T23:59:59Z")))
	if got := closedIn(sunset, []string{"alt"}, at("1971-01-01T00:00:00Z")); got != nil {
		t.Errorf("a window with no start refused a far earlier moment: %+v", got)
	}
	if got := closedIn(sunset, []string{"alt"}, at("2026-11-01T00:00:00Z")); got == nil {
		t.Error("a window with no start was ordered after it closed")
	}
}

// TestTheRefusalNamesTheWholeWhenTheClosedPartWasNeverChosen.
//
// The same distinction eligibility draws, for the same reason: an integral part is
// never deselectable, so refusing one by name leaves somebody looking for a
// checkbox that does not exist.
func TestTheRefusalNamesTheWholeWhenTheClosedPartWasNeverChosen(t *testing.T) {
	rel := aReleaseWith(anItem("workplace"),
		windowed("aktions-lizenz", 0, at("2026-10-31T23:59:59Z")))
	rel.Includes = map[string][]string{"workplace": {"aktions-lizenz"}}

	got := closedIn(rel, []string{"workplace", "aktions-lizenz"}, at("2026-12-01T00:00:00Z"))
	if got == nil {
		t.Fatal("a closed integral part was allowed through with its whole")
	}
	if got.IncludedBy != "workplace" {
		t.Errorf("includedBy = %q, want the product that carries it", got.IncludedBy)
	}
	if reason := got.reason(); !contains(reason, "workplace") ||
		!contains(reason, "aktions-lizenz") {
		t.Errorf("the refusal must name both the part and the whole, got %q", reason)
	}

	direct := closedIn(rel, []string{"aktions-lizenz"}, at("2026-12-01T00:00:00Z"))
	if direct == nil || direct.IncludedBy != "" {
		t.Errorf("= %+v, want a refusal that blames nothing but the product asked for", direct)
	}
}

// TestTheSameBasketIsRefusedTheSameWayTwice_Window.
//
// A refusal that moved between two equally-closed products would make the second
// attempt look like a second, different problem.
func TestTheSameBasketIsRefusedTheSameWayTwice_Window(t *testing.T) {
	shut := at("2026-01-01T00:00:00Z")
	rel := aReleaseWith(windowed("zebra", 0, shut), windowed("alpha", 0, shut), anItem("vpn"))

	first := closedIn(rel, []string{"zebra", "alpha", "vpn"}, at("2026-06-01T00:00:00Z"))
	second := closedIn(rel, []string{"vpn", "alpha", "zebra"}, at("2026-06-01T00:00:00Z"))
	if first == nil || second == nil {
		t.Fatal("a basket with two closed products was accepted")
	}
	if first.ItemID != second.ItemID {
		t.Errorf("refused %q and then %q for one basket", first.ItemID, second.ItemID)
	}
}
