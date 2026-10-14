package api

import (
	"strings"
	"testing"
)

// The orders table names people, rather than showing their ids.
//
// An order names people by principal id and by nothing else, and
// ADR-0314 says why: a name copied into an order is
// personal data in a record that outlives the reason for holding it. The same
// decision says what a screen does about it — "names ... are resolved from the
// account when a screen is rendered".
//
// The table was not resolving. It printed `usr_703f410b40336d21476152fb`, which is
// the data model's answer to a question the reader did not ask. Nothing was wrong
// with the record; the screen was simply not doing the half the decision left to
// it.

// TestTheOrdersTableResolvesWhoAnOrderIsFor.
func TestTheOrdersTableResolvesWhoAnOrderIsFor(t *testing.T) {
	src := readWeb(t, "shop.js")
	rows := webRegion(t, src, "function orderRowBodies(", "\n}")
	if strings.Contains(rows, "o.recipient || ''") {
		t.Error("the table prints the principal id of whoever the order is for, which " +
			"is a reference the reader cannot read")
	}
	if !strings.Contains(rows, "personName(") {
		t.Error("the table never resolves a principal id to a name")
	}
	// Resolved from the directory, and from the account records behind it — not
	// from anything the order carries, which is the half ADR-0314 forbids.
	body := webRegion(t, src, "async function load(", "\n}")
	if !strings.Contains(body, "/api/v1/principals") {
		t.Error("nothing reads the directory, so the names have no source")
	}
}

// TestTheDirectoryAndThePickerAreNotTheSameField.
//
// They were, briefly, and nothing here noticed: the recipient picker already kept
// a `state.people` list of user entries, and the directory was written into the
// same name. `loadWhoIAm` runs after the load and resets that field to an array,
// so the map the column reads would have been an array by the time a row was
// drawn, and every row would have thrown on `.get`.
//
// A guard that reads source rather than running it cannot see that. What it can
// see is the shape that caused it — two readers of one field — so it pins the two
// apart, and the load reads the directory once for both.
func TestTheDirectoryAndThePickerAreNotTheSameField(t *testing.T) {
	src := readWeb(t, "shop.js")
	load := webRegion(t, src, "async function load(", "\n}")
	if strings.Contains(load, "state.people") {
		t.Error("the load writes the recipient picker's field, which loadWhoIAm " +
			"overwrites with an array straight afterwards")
	}
	if !strings.Contains(load, "state.directory") {
		t.Error("the load no longer fills the directory the columns read")
	}
	// And the picker is filled from what the load already read, rather than asking
	// for the same list a second time.
	who := webRegion(t, src, "function loadWhoIAm(", "\n}")
	if strings.Contains(who, "await api('/api/v1/principals')") {
		t.Error("the directory is read twice per load, once for each of its readers")
	}
}

// TestAnUnknownPrincipalStillShowsSomething.
//
// A deleted account, a directory that would not load, a recipient from before the
// reader's tenancy: the row must still say who, even when "who" is all that is
// left of them. Showing nothing would make the column look broken; showing the id
// is the honest answer to "we no longer know the name".
func TestAnUnknownPrincipalStillShowsSomething(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function personName(", "\n}")
	if !strings.Contains(body, "|| id") {
		t.Error("a principal the directory does not know renders as nothing, so the " +
			"column reads as broken rather than as unresolved")
	}
}

// TestTheColumnFilterSearchesTheNameAsWell.
//
// A column that shows names and filters ids is a filter that finds nothing for
// everything somebody types.
func TestTheColumnFilterSearchesTheNameAsWell(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function matchesFilters(", "\n}")
	if !strings.Contains(body, "personName(") {
		t.Error("the person filter searches the id the column no longer shows")
	}
}
