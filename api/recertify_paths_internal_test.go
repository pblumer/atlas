package api

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// The corners of how a campaign is built (ADR-0341) that the main tests in
// recertify_internal_test.go do not reach: blank entries in a caller's lists, the
// origins a row is labelled with, and the order a reviewer works through.

// TestRecertifyABlankScopeEntryNarrowsNothing. A trailing comma in a form, or a
// spreadsheet column with an empty cell, must not turn "everybody" into "a person
// called nothing" — which would produce a campaign with no rows.
func TestRecertifyABlankScopeEntryNarrowsNothing(t *testing.T) {
	in := anEstate(
		aHolding("usr_ada", "vpn", model.OriginOrdered),
		aHolding("usr_bo", "vpn", model.OriginOrdered),
	)
	cmp := buildCampaign(recertifyOpen{Name: "n", Principals: []string{"  ", ""}}, in, "usr_root", 10, 10)
	if len(cmp.Rows) != 2 {
		t.Errorf("%d row(s), want both rights: a blank principal is no scope at all", len(cmp.Rows))
	}
}

// TestRecertifyABlankReviewerMappingAddressesNobody. A mapping with either side
// blank is skipped rather than recorded: a row addressed to "" would read as
// addressed and be answerable by nobody.
func TestRecertifyABlankReviewerMappingAddressesNobody(t *testing.T) {
	in := anEstate(aHolding("usr_ada", "vpn", model.OriginOrdered))
	cmp := buildCampaign(recertifyOpen{Name: "n", Reviewers: map[string]string{
		"usr_ada": "   ",
		"":        "usr_bo",
	}}, in, "usr_root", 10, 10)

	if len(cmp.Rows) != 1 {
		t.Fatalf("%d row(s), want 1", len(cmp.Rows))
	}
	if cmp.Rows[0].Reviewer != "" {
		t.Errorf("reviewer = %q, want none", cmp.Rows[0].Reviewer)
	}
	if c := countRows(cmp.Rows); c.Unassigned != 1 {
		t.Errorf("unassigned = %d, want the row counted as addressed to nobody", c.Unassigned)
	}
}

// TestRecertifyOriginsAreWrittenAsWords. A row is read years later, and an origin
// a reader can only decode with the source code is not evidence. An origin this
// version does not know is left blank rather than guessed.
func TestRecertifyOriginsAreWrittenAsWords(t *testing.T) {
	for origin, want := range map[model.EntitlementOrigin]string{
		model.OriginOrdered:         "ordered",
		model.OriginAdopted:         "adopted",
		model.OriginLegacy:          "legacy",
		model.EntitlementOrigin(42): "",
	} {
		if got := originName(origin); got != want {
			t.Errorf("originName(%d) = %q, want %q", origin, got, want)
		}
	}

	in := anEstate(aHolding("usr_ada", "vpn", model.OriginAdopted))
	cmp := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 10, 10)
	if len(cmp.Rows) != 1 || cmp.Rows[0].Origin != "adopted" {
		t.Errorf("rows = %+v, want the adopted right labelled so", cmp.Rows)
	}
}

// TestRecertifyOnePersonsRightsAreInProductOrder. Within one reviewer and one
// holder, the rows follow the product id, so the same campaign reads the same way
// every time it is opened.
func TestRecertifyOnePersonsRightsAreInProductOrder(t *testing.T) {
	in := anEstate(
		aHolding("usr_ada", "vpn", model.OriginOrdered),
		aHolding("usr_ada", "crm", model.OriginOrdered),
		aHolding("usr_ada", "sap", model.OriginOrdered),
	)
	cmp := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 10, 10)

	var got []string
	for _, r := range cmp.Rows {
		got = append(got, r.ItemID)
	}
	if len(got) != 3 || got[0] != "crm" || got[1] != "sap" || got[2] != "vpn" {
		t.Errorf("order = %v, want crm, sap, vpn", got)
	}
}
