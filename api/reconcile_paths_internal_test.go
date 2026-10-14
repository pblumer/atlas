package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// Corners of decideReconcile (ADR-0334) the main tests leave out: blank entries
// in a reading, a reference two products claim, the two ways an observation can
// fail to resolve, and the order notes come back in.

// reconcilePathsVPN is a catalogue of one product claiming one AD group.
func reconcilePathsVPN() []catalog.Item {
	return []catalog.Item{{ID: "vpn", State: catalog.StateActive,
		Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}}
}

// TestReconcileBlankEntriesAreNotPromises. A blank reference, subject or half of
// an observation is a gap in a caller's list, not a claim to have read something
// — so it neither enters a scope nor produces a note about a person called "".
func TestReconcileBlankEntriesAreNotPromises(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: reconcilePathsVPN(),
		Held:  []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginOrdered)},
	}
	msg := reconcileMessage{
		System:   "ad",
		Refs:     []string{"  ", "CN=VPN"},
		Subjects: []string{" "},
		Observations: []rightObservation{
			{Subject: "", Ref: "CN=VPN"},
			{Subject: "oid-ada", Ref: "  "},
			{Subject: "oid-ada", Ref: "CN=VPN"},
		},
	}
	plan := decideReconcile(msg, in)

	if plan.Counts.Agrees != 1 || len(plan.Found) != 0 {
		t.Errorf("counts = %+v found = %+v, want Ada's right to agree and nothing else", plan.Counts, plan.Found)
	}
	if plan.Counts.NoSubject != 0 || plan.Counts.OutOfScope != 0 || plan.Counts.NoItem != 0 {
		t.Errorf("counts = %+v; a blank entry produced a note", plan.Counts)
	}
	if len(plan.InScopeSubjects) != 0 || len(plan.InScopeItems) != 1 {
		t.Errorf("scope = items %v subjects %v, want only vpn", plan.InScopeItems, plan.InScopeSubjects)
	}
}

// TestReconcileAReferenceTwoProductsClaimComparesNothing. The reference is noted
// with both claimants named, and the rights recorded under either are not
// examined: with no observations under it, nothing is reported missing.
func TestReconcileAReferenceTwoProductsClaimComparesNothing(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "vpn-a", State: catalog.StateActive, Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
			{ID: "vpn-b", State: catalog.StateActive, Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
		},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn-a", model.OriginOrdered)},
	}
	plan := decideReconcile(reading("ad", []string{"CN=VPN"}), in)

	if plan.Counts.Ambiguous != 1 || len(plan.Notes) != 1 || plan.Notes[0].Kind != recAmbiguous {
		t.Fatalf("counts = %+v notes = %+v, want one ambiguity note", plan.Counts, plan.Notes)
	}
	if why := plan.Notes[0].Why; !strings.Contains(why, "vpn-a, vpn-b") {
		t.Errorf("note = %q, want both claimants named", why)
	}
	if plan.Counts.Missing != 0 || len(plan.Found) != 0 {
		t.Errorf("found = %+v; a right under an ambiguous reference was concluded about", plan.Found)
	}
}

// reconcilePathsTwoClaimants is Ada holding vpn-a, where vpn-a and vpn-b both
// claim AD's CN=VPN.
func reconcilePathsTwoClaimants() reconcileInput {
	return reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "vpn-a", State: catalog.StateActive, Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
			{ID: "vpn-b", State: catalog.StateActive, Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
		},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn-a", model.OriginOrdered)},
	}
}

// TestReconcileAnObservationUnderAContestedReferenceIsAttributedToNobody, in ref
// scope. The reference is noted once as ambiguous, and what was read under it is
// not then credited to whichever claimant sorts first: doing so reported Ada's
// recorded vpn-a as "held and not recorded" — a finding an operator could adopt
// or deprovision, about a right the inventory holds.
func TestReconcileAnObservationUnderAContestedReferenceIsAttributedToNobody(t *testing.T) {
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN"}), reconcilePathsTwoClaimants())

	if plan.Counts.Unmanaged != 0 || plan.Counts.Missing != 0 || len(plan.Found) != 0 {
		t.Errorf("counts = %+v found = %+v; an observation under a contested reference was "+
			"attributed to a product", plan.Counts, plan.Found)
	}
	if plan.Counts.Ambiguous != 1 || len(plan.Notes) != 1 || plan.Notes[0].Kind != recAmbiguous ||
		plan.Notes[0].Principal != "" {
		t.Errorf("counts = %+v notes = %+v, want the reference noted as ambiguous exactly once",
			plan.Counts, plan.Notes)
	}
}

// TestReconcileAContestedReferenceInASubjectsEstateIsNotedAgainstThem. A run that
// read Ada's whole estate found her in a group two products claim: that is said
// against her, naming both products, and neither product is concluded about — not
// as unmanaged, and not as missing for the vpn-a she is recorded as holding.
func TestReconcileAContestedReferenceInASubjectsEstateIsNotedAgainstThem(t *testing.T) {
	plan := decideReconcile(reconcileMessage{
		System:       "ad",
		Subjects:     []string{"oid-ada"},
		Observations: []rightObservation{{Subject: "oid-ada", Ref: "CN=VPN"}},
	}, reconcilePathsTwoClaimants())

	if len(plan.Found) != 0 || plan.Counts.Unmanaged != 0 || plan.Counts.Missing != 0 || plan.Counts.Agrees != 0 {
		t.Errorf("counts = %+v found = %+v, want nothing concluded about either claimant",
			plan.Counts, plan.Found)
	}
	if plan.Counts.Ambiguous != 1 || len(plan.Notes) != 1 {
		t.Fatalf("counts = %+v notes = %+v, want one ambiguity note", plan.Counts, plan.Notes)
	}
	n := plan.Notes[0]
	if n.Kind != recAmbiguous || n.Principal != "oid-ada" || n.Ref != "CN=VPN" ||
		!strings.Contains(n.Why, "vpn-a, vpn-b") {
		t.Errorf("note = %+v, want an ambiguity against oid-ada naming both claimants", n)
	}
}

// TestReconcileASubjectsUnmodelledHoldingIsNotedAgainstThem. A run that read one
// person's whole estate and found a group no product claims has found something
// about that person, and the note carries their name — unlike the same group in
// a reference scope, where the note is about the catalogue.
func TestReconcileASubjectsUnmodelledHoldingIsNotedAgainstThem(t *testing.T) {
	in := reconcileInput{Users: []User{ada()}, Items: reconcilePathsVPN()}
	plan := decideReconcile(reconcileMessage{
		System:       "ad",
		Subjects:     []string{"oid-ada"},
		Observations: []rightObservation{{Subject: "oid-ada", Ref: "CN=Printers"}},
	}, in)

	if plan.Counts.NoItem != 1 || len(plan.Notes) != 1 {
		t.Fatalf("counts = %+v notes = %+v, want one no-item note", plan.Counts, plan.Notes)
	}
	n := plan.Notes[0]
	if n.Kind != recNoItem || n.Principal != "oid-ada" || n.Ref != "CN=Printers" {
		t.Errorf("note = %+v, want a no-item note against oid-ada for CN=Printers", n)
	}
	if len(plan.Found) != 0 {
		t.Errorf("found = %+v; an unmodelled group is not a discrepancy", plan.Found)
	}
}

// TestReconcileAnUnknownHolderInScopeIsNotedNotGuessed. Somebody the directory
// has never mirrored is in the group: what they hold cannot be compared with
// anything, and the note says which product it would have been.
func TestReconcileAnUnknownHolderInScopeIsNotedNotGuessed(t *testing.T) {
	in := reconcileInput{Users: []User{ada()}, Items: reconcilePathsVPN()}
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-stranger", Ref: "CN=VPN"}), in)

	if plan.Counts.NoSubject != 1 || len(plan.Notes) != 1 {
		t.Fatalf("counts = %+v notes = %+v, want one no-subject note", plan.Counts, plan.Notes)
	}
	if n := plan.Notes[0]; n.Kind != recNoSubject || n.ItemID != "vpn" || n.Principal != "oid-stranger" {
		t.Errorf("note = %+v, want no-subject for oid-stranger on vpn", n)
	}
	if plan.Counts.Unmanaged != 0 {
		t.Errorf("counts = %+v; an unresolved holder was reported as an unmanaged right", plan.Counts)
	}
}

// TestReconcileNotesComeBackInOneOrder. Within one kind and one product the
// notes sort by holder, and then by reference, so two runs over the same reading
// produce reports that diff cleanly.
func TestReconcileNotesComeBackInOneOrder(t *testing.T) {
	in := reconcileInput{Users: []User{ada()}, Items: reconcilePathsVPN()}
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-zed", Ref: "CN=VPN"},
		rightObservation{Subject: "oid-amy", Ref: "CN=VPN"},
		rightObservation{Subject: "oid-bob", Ref: "CN=Zeta"},
		rightObservation{Subject: "oid-bob", Ref: "CN=Alpha"},
	), in)

	var got []string
	for _, n := range plan.Notes {
		got = append(got, n.Kind+":"+n.Principal+":"+n.Ref)
	}
	want := []string{
		"no-subject:oid-amy:CN=VPN",
		"no-subject:oid-zed:CN=VPN",
		"out-of-scope:oid-bob:CN=Alpha",
		"out-of-scope:oid-bob:CN=Zeta",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("notes = %v, want %v", got, want)
	}
}
