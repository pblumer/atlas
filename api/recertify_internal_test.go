package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// What a campaign asks, before anything answers it
// (ADR-draft-access-recertification).
//
// Every one of these is a way a campaign could ask the wrong question, or fail to
// ask one at all — and an unasked question is the failure that hides, because a
// campaign missing rows looks exactly like a campaign somebody finished.

func aHolding(principal, itemID string, origin model.EntitlementOrigin) model.EntitlementValue {
	return model.EntitlementValue{Principal: principal, ItemID: itemID, Since: 1_000, Origin: origin}
}

func anEstate(held ...model.EntitlementValue) recertifyInput {
	return recertifyInput{
		Users: []User{
			{ID: "usr_ada", Email: "ada@example.org", DirectoryID: "oid-ada"},
			{ID: "usr_bo", Email: "bo@example.org", DirectoryID: "oid-bo"},
		},
		Held:     held,
		Disputes: map[string]discrepancyRecord{},
	}
}

// TestAnEmptyScopeIsEverything.
//
// The opposite of a reconciliation's refusal, and deliberately so. There, an
// unstated scope would turn a truncated read into a report that the estate lost its
// access — here nothing is concluded from absence at all, so a campaign over
// everything is a big campaign and not a wrong one.
func TestAnEmptyScopeIsEverything(t *testing.T) {
	in := anEstate(
		aHolding("usr_ada", "vpn", model.OriginOrdered),
		aHolding("usr_bo", "sap", model.OriginLegacy),
	)
	cmp := buildCampaign(recertifyOpen{Name: "all"}, in, "usr_root", 10)

	if len(cmp.Rows) != 2 {
		t.Fatalf("%d row(s), want both rights in the estate: %+v", len(cmp.Rows), cmp.Rows)
	}
	if c := countRows(cmp.Rows); c.Undecided != 2 || c.Kept != 0 || c.Revoked != 0 {
		t.Errorf("counts = %+v, want both rows unanswered", c)
	}
}

// TestAScopeNarrowsByProductAndByPerson, and the two compose.
func TestAScopeNarrowsByProductAndByPerson(t *testing.T) {
	in := anEstate(
		aHolding("usr_ada", "vpn", model.OriginOrdered),
		aHolding("usr_ada", "sap", model.OriginOrdered),
		aHolding("usr_bo", "vpn", model.OriginOrdered),
	)
	cmp := buildCampaign(recertifyOpen{
		Name: "vpn for ada", Items: []string{"vpn"}, Principals: []string{"ada@example.org"},
	}, in, "usr_root", 10)

	if len(cmp.Rows) != 1 {
		t.Fatalf("%d row(s), want the one pair both halves of the scope name: %+v",
			len(cmp.Rows), cmp.Rows)
	}
	if cmp.Rows[0].Principal != "usr_ada" || cmp.Rows[0].ItemID != "vpn" {
		t.Errorf("row = %+v, want Ada's VPN", cmp.Rows[0])
	}
}

// TestAPersonIsNamedHoweverTheCallerKnowsThem.
//
// A principal id, a directory object id or a mail address. A process that knows
// people by their directory id must not have to learn Atlas's ids to ask a question
// about them — and a caller that used the wrong one would silently get a campaign
// with no rows, which reads as an estate with nothing to certify.
func TestAPersonIsNamedHoweverTheCallerKnowsThem(t *testing.T) {
	in := anEstate(aHolding("usr_ada", "vpn", model.OriginOrdered))

	for _, named := range []string{"usr_ada", "oid-ada", "ada@example.org", "ADA@example.org"} {
		cmp := buildCampaign(recertifyOpen{Name: "n", Principals: []string{named}}, in, "usr_root", 10)
		if len(cmp.Rows) != 1 {
			t.Errorf("naming Ada as %q produced %d row(s), want 1", named, len(cmp.Rows))
		}
	}
}

// TestANameNothingResolvesIsKeptRatherThanDropped.
//
// A misspelled subject that quietly matched nobody would produce a campaign with no
// rows for that person — indistinguishable from a person who holds nothing, which
// is the answer an offboarding or an audit would most like to believe.
func TestANameNothingResolvesIsKeptRatherThanDropped(t *testing.T) {
	in := anEstate(aHolding("usr_ada", "vpn", model.OriginOrdered))
	cmp := buildCampaign(recertifyOpen{
		Name: "ghosts", Principals: []string{"nobody@example.org"},
	}, in, "usr_root", 10)

	if len(cmp.Rows) != 0 {
		t.Fatalf("%d row(s) for somebody Atlas has never heard of", len(cmp.Rows))
	}
	if reason := recertifyReason(cmp); !strings.Contains(reason, "Check the product ids") {
		t.Errorf("reason = %q, want the campaign to say it may have been asked about nothing",
			reason)
	}
}

// TestAReviewerIsResolvedOnBothSides.
//
// The map is holder → reviewer, and either side may be written in any of the three
// vocabularies. A reviewer stored as a mail address would never match the principal
// a request arrives with, so the row would be answerable by nobody but an operator
// — a silent failure that looks exactly like a correctly addressed campaign.
func TestAReviewerIsResolvedOnBothSides(t *testing.T) {
	in := anEstate(aHolding("usr_ada", "vpn", model.OriginOrdered))
	cmp := buildCampaign(recertifyOpen{
		Name: "n", Reviewers: map[string]string{"oid-ada": "bo@example.org"},
	}, in, "usr_root", 10)

	if len(cmp.Rows) != 1 {
		t.Fatalf("%d row(s), want 1", len(cmp.Rows))
	}
	if cmp.Rows[0].Reviewer != "usr_bo" {
		t.Errorf("reviewer = %q, want Bo's principal id; a row addressed to a mail address is "+
			"addressed to nobody a request can arrive as", cmp.Rows[0].Reviewer)
	}
}

// TestAHolderNobodyReviewsIsUnassignedRatherThanRefused.
func TestAHolderNobodyReviewsIsUnassignedRatherThanRefused(t *testing.T) {
	in := anEstate(
		aHolding("usr_ada", "vpn", model.OriginOrdered),
		aHolding("usr_bo", "vpn", model.OriginOrdered),
	)
	cmp := buildCampaign(recertifyOpen{
		Name: "n", Reviewers: map[string]string{"ada@example.org": "usr_bo"},
	}, in, "usr_root", 10)

	c := countRows(cmp.Rows)
	if c.Rows != 2 {
		t.Fatalf("%d row(s), want both — one person without a manager must not stop a campaign",
			c.Rows)
	}
	if c.Unassigned != 1 {
		t.Errorf("unassigned = %d, want the one row nobody was asked about counted as such", c.Unassigned)
	}
}

// TestARowCarriesWhatTheReviewerWasShown.
//
// Frozen at build time rather than resolved when the row is rendered. What is being
// attested is what the person saw, and a row that re-read the inventory would show a
// later reader a different question from the one that was answered.
func TestARowCarriesWhatTheReviewerWasShown(t *testing.T) {
	held := aHolding("usr_ada", "vpn", model.OriginOrdered)
	held.OrderID, held.VariantID = "ord_7", "large"
	in := anEstate(held)
	in.Disputes[disputeKey("usr_ada", "vpn")] = discrepancyRecord{
		ID: "dsc_1", Kind: recMissing, Principal: "usr_ada", ItemID: "vpn",
	}

	cmp := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 10)
	row := cmp.Rows[0]

	switch {
	case row.Origin != "ordered":
		t.Errorf("origin = %q, want it in words — an origin whose meaning lives in a Go "+
			"constant is evidence nobody can read", row.Origin)
	case row.OrderID != "ord_7":
		t.Errorf("orderId = %q, want the order the right came from", row.OrderID)
	case row.VariantID != "large":
		t.Errorf("variantId = %q, want the variant held", row.VariantID)
	case row.Since != 1_000:
		t.Errorf("since = %d, want when the hold began", row.Since)
	case !row.Disputed || row.DisputeKind != recMissing || row.DisputeID != "dsc_1":
		t.Errorf("row = %+v, want it to say the right is contested and which finding says so", row)
	}
}

// TestTheSameCampaignAsksEachPairOnce.
//
// A row id is derived from the campaign and the pair, so a duplicate question
// cannot exist and a decision cannot land on a row it was not meant for.
func TestTheSameCampaignAsksEachPairOnce(t *testing.T) {
	in := anEstate(aHolding("usr_ada", "vpn", model.OriginOrdered))
	first := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 10)
	again := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 10)
	later := buildCampaign(recertifyOpen{Name: "n"}, in, "usr_root", 20)

	if first.Rows[0].ID != again.Rows[0].ID {
		t.Error("the same pair in the same campaign has two ids")
	}
	if first.ID == later.ID {
		t.Error("the same campaign name opened at two moments is one campaign; next quarter's " +
			"review would overwrite this quarter's evidence")
	}
	if first.Rows[0].ID == later.Rows[0].ID {
		t.Error("two campaigns share a row id; a decision in one would land in the other")
	}
}

// TestUndecidedIsCountedRatherThanSubtracted.
//
// A progress bar invites the reader to treat the remainder as work in hand. The
// four numbers say what is actually known, and the one that matters is the one
// nobody wants to publish.
func TestUndecidedIsCountedRatherThanSubtracted(t *testing.T) {
	rows := []recertifyRow{
		{Decision: decisionKeep},
		{Decision: decisionRevoke},
		{},
		{Reviewer: "", Disputed: true},
	}
	c := countRows(rows)
	switch {
	case c.Rows != 4:
		t.Errorf("rows = %d, want 4", c.Rows)
	case c.Kept != 1 || c.Revoked != 1:
		t.Errorf("counts = %+v, want one of each decision", c)
	case c.Undecided != 2:
		t.Errorf("undecided = %d, want both unanswered rows — a row nobody answered is not "+
			"certified", c.Undecided)
	case c.Unassigned != 4 || c.Disputed != 1:
		t.Errorf("counts = %+v, want the addressing and the disputes counted independently", c)
	}
}
