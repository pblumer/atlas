package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// The rules a commissioning load decides by
// (ADR-draft-inventory-commissioning-load).
//
// Every one of these is a way the load could quietly destroy or invent evidence
// that is then kept for years, so each is checked by doing the thing that would
// expose it rather than by asserting a field.

// vpn is a product claiming one group in one system.
func vpn() catalog.Item {
	return catalog.Item{
		ID: "vpn", HomeCatalog: "cat", State: catalog.StateActive,
		Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN-Users"}},
	}
}

// ada is an account the directory mirror wrote.
func ada() User {
	return User{ID: "usr_ada", Username: "ada", Email: "Ada@example.org", DirectoryID: "oid-ada"}
}

// nothingHeld is an inventory with nobody in it.
func nothingHeld(string, string) (*model.EntitlementValue, bool) { return nil, false }

// holding is an inventory holding exactly one thing.
func holding(v model.EntitlementValue) func(string, string) (*model.EntitlementValue, bool) {
	return func(principal, itemID string) (*model.EntitlementValue, bool) {
		if principal == v.Principal && itemID == v.ItemID {
			return &v, true
		}
		return nil, false
	}
}

func load(system string, obs ...rightObservation) inventoryLoadMessage {
	return inventoryLoadMessage{System: system, Observations: obs}
}

// actionsOf returns the decided action per subject+ref, so a test names the rule it
// is checking rather than an index into a slice.
func actionsOf(plan inventoryPlan) map[string]string {
	out := map[string]string{}
	for _, d := range append(append([]inventoryDecision{}, plan.Grants...), plan.Notes...) {
		out[d.Subject+"|"+d.Ref] = d.Action
	}
	return out
}

// TestALegacyLoadNeverOverwritesWhatAnOrderGranted is the destructive case, and the
// reason the load reads the inventory at all.
//
// GrantEntitlement replaces. So a load that decided to write `legacy` over a right
// an order produced would discard the order id and the approval behind it — with
// nothing failing, nothing logging, and the evidence gone. The only sign afterwards
// would be a right that used to be traceable and now says nobody granted it.
func TestALegacyLoadNeverOverwritesWhatAnOrderGranted(t *testing.T) {
	ordered := model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", OrderID: "ord-7",
		Since: 1_600_000_000, Origin: model.OriginOrdered,
	}
	plan := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn()}, holding(ordered), 9_000)

	if len(plan.Grants) != 0 {
		t.Fatalf("the load would write %d grant(s) over an ordered right; the order behind "+
			"it would be discarded and nothing would say so", len(plan.Grants))
	}
	if plan.Counts.Keep != 1 {
		t.Errorf("counts.keep = %d, want the ordered right reported as better knowledge", plan.Counts.Keep)
	}
	if why := plan.Notes[0].Why; !strings.Contains(why, "better") {
		t.Errorf("the note says %q; it has to say why the load stood down, or a reader "+
			"concludes the load failed", why)
	}
}

// TestASecondLoadDoesNotAgeTheEstateBackwards: re-running a load must not move the
// start date of what it already recorded.
//
// Since is the only thing that says how long somebody has had something. A load that
// rewrote it would make the whole estate look freshly acquired every time anybody
// checked — the act of verifying would destroy the thing being verified.
func TestASecondLoadDoesNotAgeTheEstateBackwards(t *testing.T) {
	first := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn()}, nothingHeld, 1_000)
	if len(first.Grants) != 1 || first.Grants[0].Value.Since != 1_000 {
		t.Fatalf("the first load did not record the right at its own moment: %+v", first.Grants)
	}

	already := first.Grants[0].Value
	second := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn()}, holding(already), 5_000)

	if len(second.Grants) != 0 {
		t.Errorf("the second load would write again, moving the start date from %d to 5000",
			already.Since)
	}
	if second.Counts.Held != 1 {
		t.Errorf("counts.held = %d, want the second load to report the right as already recorded",
			second.Counts.Held)
	}
}

// TestAReportPredictsTheLoadExactly: the reporting mode is the decision without the
// write, not a second implementation of it.
//
// A preview computed by its own code agrees with the real run until the day it
// stops, and the day it stops is invisible — the report is read, believed and
// applied. Here there is one decision, and this holds the two runs against the same
// input to say so.
func TestAReportPredictsTheLoadExactly(t *testing.T) {
	obs := []rightObservation{
		{Subject: "oid-ada", Ref: "CN=VPN-Users"},
		{Subject: "nobody@example.org", Ref: "CN=VPN-Users"},
		{Subject: "oid-ada", Ref: "CN=Unmodelled"},
	}
	users, items := []User{ada()}, []catalog.Item{vpn()}

	report := decideInventoryLoad(inventoryLoadMessage{System: "ad", Observations: obs},
		users, items, nothingHeld, 1_000)
	real := decideInventoryLoad(inventoryLoadMessage{System: "ad", Apply: true, Observations: obs},
		users, items, nothingHeld, 1_000)

	if report.Counts != real.Counts {
		t.Errorf("the report decided %+v and the real run %+v; a preview that can differ "+
			"from the run is worse than no preview", report.Counts, real.Counts)
	}
	if len(report.Grants) != len(real.Grants) {
		t.Fatalf("report grants %d, real run grants %d", len(report.Grants), len(real.Grants))
	}
	for i := range report.Grants {
		if report.Grants[i].Value != real.Grants[i].Value {
			t.Errorf("grant %d differs: report %+v, real %+v",
				i, report.Grants[i].Value, real.Grants[i].Value)
		}
	}
}

// TestNothingIsDroppedInSilence: the observations that resolve to nothing are the
// most valuable output of a first load, so none of them may vanish.
//
// A subject with no account is a person the directory mirror has not reached. A ref
// no product claims is a right the estate grants that the catalogue does not model —
// which nothing in the estate could list before, and which is the actual inventory
// gap the exercise exists to find.
func TestNothingIsDroppedInSilence(t *testing.T) {
	plan := decideInventoryLoad(load("ad",
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"},
		rightObservation{Subject: "ghost@example.org", Ref: "CN=VPN-Users"},
		rightObservation{Subject: "oid-ada", Ref: "CN=AllStaff"},
		rightObservation{Subject: "ghost@example.org", Ref: "CN=AllStaff"},
		rightObservation{Subject: "", Ref: "CN=VPN-Users"},
	), []User{ada()}, []catalog.Item{vpn()}, nothingHeld, 1_000)

	got := actionsOf(plan)
	for k, want := range map[string]string{
		"oid-ada|CN=VPN-Users":           invGrant,
		"ghost@example.org|CN=VPN-Users": invNoSubject,
		"oid-ada|CN=AllStaff":            invNoItem,
		"|CN=VPN-Users":                  invMalformed,
	} {
		if got[k] != want {
			t.Errorf("%s decided %q, want %q", k, got[k], want)
		}
	}
	if len(plan.Grants)+len(plan.Notes) != 5 {
		t.Errorf("%d observations in, %d decisions out; something was dropped",
			5, len(plan.Grants)+len(plan.Notes))
	}

	// And the unmodelled right is rolled up rather than repeated per person: two
	// people hold CN=AllStaff, and that is one fact about the catalogue.
	if len(plan.Unmapped) != 1 || plan.Unmapped[0].Ref != "CN=AllStaff" || plan.Unmapped[0].Subjects != 2 {
		t.Errorf("unmapped = %+v, want CN=AllStaff held by 2", plan.Unmapped)
	}
}

// TestARightIsAttributedOnlyWhereItIsUnambiguous: two products claiming one
// reference is attributed to neither.
//
// Publishing refuses this within one release; two catalogues can each publish
// cleanly and still collide, and this is the only place that is visible. Guessing
// between them would write evidence Atlas invented, against a product nobody chose.
func TestARightIsAttributedOnlyWhereItIsUnambiguous(t *testing.T) {
	other := catalog.Item{
		ID: "remote-work", HomeCatalog: "cat2", State: catalog.StateActive,
		Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN-Users"}},
	}
	plan := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn(), other}, nothingHeld, 1_000)

	if len(plan.Grants) != 0 || plan.Counts.Ambiguous != 1 {
		t.Fatalf("an ambiguous reference produced %d grant(s): %+v", len(plan.Grants), plan)
	}
	if why := plan.Notes[0].Why; !strings.Contains(why, "remote-work") || !strings.Contains(why, "vpn") {
		t.Errorf("the note says %q; it has to name both products, or fixing it means "+
			"searching the catalogue by hand", why)
	}
}

// TestADraftProductIsNeverAttributedAndAWithdrawnOneStillIs.
//
// The two look symmetrical and are opposites. A draft is not a decision — it cannot
// be published, nothing can be ordered against it — so years of evidence must not
// point at it. A withdrawal stops a product being ordered and changes nothing about
// the people still holding it, which is exactly why ADR-0312 has no deleted state.
func TestADraftProductIsNeverAttributedAndAWithdrawnOneStillIs(t *testing.T) {
	draft := vpn()
	draft.ID, draft.State = "draft-thing", catalog.StateDraft
	draft.Targets = []catalog.TargetRef{{System: "ad", Ref: "CN=Draft"}}

	withdrawn := vpn()
	withdrawn.ID, withdrawn.State = "old-thing", catalog.StateWithdrawn
	withdrawn.Targets = []catalog.TargetRef{{System: "ad", Ref: "CN=Old"}}

	plan := decideInventoryLoad(load("ad",
		rightObservation{Subject: "oid-ada", Ref: "CN=Draft"},
		rightObservation{Subject: "oid-ada", Ref: "CN=Old"},
	), []User{ada()}, []catalog.Item{draft, withdrawn}, nothingHeld, 1_000)

	got := actionsOf(plan)
	if got["oid-ada|CN=Draft"] != invNoItem {
		t.Errorf("a draft product was attributed a right (%q); a draft may never be "+
			"published, so the evidence would point at something that need never exist",
			got["oid-ada|CN=Draft"])
	}
	if got["oid-ada|CN=Old"] != invGrant {
		t.Errorf("a withdrawn product was not attributed (%q); withdrawal stops ordering "+
			"and says nothing about who already holds it", got["oid-ada|CN=Old"])
	}
}

// TestOneProductHeldOnceHoweverManyGroupsSaySo: a product legitimately claims two
// groups, and somebody in both holds it once.
func TestOneProductHeldOnceHoweverManyGroupsSaySo(t *testing.T) {
	both := vpn()
	both.Targets = append(both.Targets, catalog.TargetRef{System: "ad", Ref: "CN=VPN-Legacy"})

	plan := decideInventoryLoad(load("ad",
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"},
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Legacy"},
	), []User{ada()}, []catalog.Item{both}, nothingHeld, 1_000)

	if len(plan.Grants) != 1 {
		t.Errorf("%d grants for one product held through two groups; the second would "+
			"replace the first and the count of what was loaded would be wrong",
			len(plan.Grants))
	}
}

// TestASubjectResolvesByDirectoryIdBeforeMail: the two are not interchangeable.
//
// A directory id is what the mirror wrote and is unique by construction. Mail is a
// human-facing attribute that changes when somebody marries and can be reissued when
// somebody leaves — so it is the fallback, and it is the fallback precisely because
// it is the one that can be wrong.
func TestASubjectResolvesByDirectoryIdBeforeMail(t *testing.T) {
	// Two accounts: one carries the object id, the other has taken over the mail
	// address it used to have. The object id must win.
	holder := User{ID: "usr_holder", Username: "holder", Email: "shared@example.org", DirectoryID: "oid-x"}
	successor := User{ID: "usr_successor", Username: "successor", Email: "oid-x"}

	plan := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-x", Ref: "CN=VPN-Users"}),
		[]User{successor, holder}, []catalog.Item{vpn()}, nothingHeld, 1_000)

	if len(plan.Grants) != 1 || plan.Grants[0].Principal != "usr_holder" {
		t.Errorf("the right went to %+v, want the account whose directory id matches", plan.Grants)
	}

	// And mail still resolves when there is no object id to go on, case-insensitively.
	byMail := decideInventoryLoad(
		load("ad", rightObservation{Subject: "ADA@EXAMPLE.ORG", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn()}, nothingHeld, 1_000)
	if len(byMail.Grants) != 1 {
		t.Errorf("a mail address in a different case resolved to nobody: %+v", byMail)
	}
}

// TestAnObservationFromAnotherSystemIsNotAttributed: the system name is part of the
// match, not decoration.
//
// The same string means different things in two systems — "Administrators" is a
// group in every directory ever built — and a load that ignored the system would
// attribute one system's rights to another system's products.
func TestAnObservationFromAnotherSystemIsNotAttributed(t *testing.T) {
	plan := decideInventoryLoad(
		load("entra", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users"}),
		[]User{ada()}, []catalog.Item{vpn()}, nothingHeld, 1_000)

	if len(plan.Grants) != 0 || plan.Counts.NoItem != 1 {
		t.Errorf("a reference from system %q matched a product claiming it in %q: %+v",
			"entra", "ad", plan)
	}
}

// TestEverythingLoadedIsMarkedAsNotOurs: the origin is the whole point.
//
// An entitlement this load writes must say `legacy` and must name no order. Anything
// else claims Atlas granted something it did not watch happen, and a reconciliation
// reading it would treat an assumption as evidence.
func TestEverythingLoadedIsMarkedAsNotOurs(t *testing.T) {
	plan := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users", VariantID: "gold"}),
		[]User{ada()}, []catalog.Item{vpn()}, nothingHeld, 4_242)

	if len(plan.Grants) != 1 {
		t.Fatalf("want one grant, got %+v", plan.Grants)
	}
	v := plan.Grants[0].Value
	switch {
	case v.Origin != model.OriginLegacy:
		t.Errorf("origin = %v, want legacy: this load did not watch the right being granted", v.Origin)
	case v.OrderID != "":
		t.Errorf("orderId = %q, want empty: nothing here produced this right", v.OrderID)
	case v.VariantID != "gold":
		t.Errorf("variantId = %q, want the one observed", v.VariantID)
	case v.Since != 4_242:
		t.Errorf("since = %d, want the load's own moment where the system reported none", v.Since)
	}

	// And where the system does know, its answer is kept rather than overwritten.
	known := decideInventoryLoad(
		load("ad", rightObservation{Subject: "oid-ada", Ref: "CN=VPN-Users", Since: 99}),
		[]User{ada()}, []catalog.Item{vpn()}, nothingHeld, 4_242)
	if known.Grants[0].Value.Since != 99 {
		t.Errorf("since = %d, want the date the target system reported", known.Grants[0].Value.Since)
	}
}
