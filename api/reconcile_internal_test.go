package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// What a reconciliation may and may not conclude (ADR-draft-reconciliation).
//
// Every one of these is a way the comparison could report a falsehood confidently,
// and confidence is the whole product here: a finding is what somebody acts on.

func heldBy(principal, itemID string, origin model.EntitlementOrigin) model.EntitlementValue {
	return model.EntitlementValue{Principal: principal, ItemID: itemID, Since: 1_000, Origin: origin}
}

// reading builds a message that claims to have read refs completely.
func reading(system string, refs []string, obs ...rightObservation) reconcileMessage {
	return reconcileMessage{System: system, Refs: refs, Observations: obs}
}

func kindsOf(plan reconcilePlan) map[string]string {
	out := map[string]string{}
	for _, d := range append(append([]discrepancy{}, plan.Found...), plan.Notes...) {
		out[d.Principal+"|"+d.ItemID+"|"+d.Ref] = d.Kind
	}
	return out
}

// TestNothingIsConcludedOutsideTheDeclaredScope is the property the whole endpoint
// rests on.
//
// A commissioning load may read half a system and be right about what it found. A
// reconciliation reads absence as a finding, so the same half-read would report
// everybody in the unread half as having lost their access. The scope is the
// promise that makes absence interpretable, and an item outside it must be
// untouched — not "assumed present", not "assumed missing", but unexamined.
func TestNothingIsConcludedOutsideTheDeclaredScope(t *testing.T) {
	items := []catalog.Item{
		{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
		{ID: "sap", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=SAP"}}},
	}
	in := reconcileInput{
		Users: []User{ada()},
		Items: items,
		Held: []model.EntitlementValue{
			heldBy("usr_ada", "vpn", model.OriginOrdered),
			heldBy("usr_ada", "sap", model.OriginOrdered),
		},
	}

	// Only CN=VPN was read, and Ada was found in it. SAP is not in scope.
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN"}), in)

	if plan.Counts.Missing != 0 {
		t.Errorf("counts.missing = %d; the SAP right was outside the scope this run read, so "+
			"nothing may be concluded about it. Reporting it missing is how one group's "+
			"reconciliation reports a whole estate as broken", plan.Counts.Missing)
	}
	if plan.Counts.Agrees != 1 {
		t.Errorf("counts.agrees = %d, want the one right in scope to agree", plan.Counts.Agrees)
	}
	if plan.InScopeItems["sap"] {
		t.Error("sap is marked in scope; the run never claimed to have read CN=SAP")
	}
}

// TestBothDirectionsAreFound: the comparison is not one-sided.
//
// The direction people expect is "somebody has something nobody granted". The one
// that corrupts the evidence is the other: Atlas asserting a right the target
// system does not have, confidently, forever, until somebody looks.
func TestBothDirectionsAreFound(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada(), {ID: "usr_bo", Username: "bo", Email: "bo@example.org", DirectoryID: "oid-bo"}},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginOrdered)},
	}
	// Bo is in the group and not in the inventory; Ada is in the inventory and not
	// in the group.
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-bo", Ref: "CN=VPN"}), in)

	got := kindsOf(plan)
	if got["usr_bo|vpn|CN=VPN"] != recUnmanaged {
		t.Errorf("bo decided %q, want unmanaged", got["usr_bo|vpn|CN=VPN"])
	}
	if got["usr_ada|vpn|"] != recMissing {
		t.Errorf("ada decided %q, want missing", got["usr_ada|vpn|"])
	}
	if plan.Counts.Unmanaged != 1 || plan.Counts.Missing != 1 {
		t.Errorf("counts = %+v, want one of each", plan.Counts)
	}

	// The origin travels with a missing finding, because it decides how alarming it
	// is: an ordered right that vanished is a provisioning that came undone.
	for _, d := range plan.Found {
		if d.Kind == recMissing && d.Origin != "ordered" {
			t.Errorf("the missing finding says origin %q; without it a reader cannot tell a "+
				"provisioning that came undone from a group somebody tidied up years ago", d.Origin)
		}
	}
}

// TestAgreementProducesNothing: a hundred identical readings must produce nothing.
//
// A journal of samples is a journal nobody reads — the shape api/panorama/drift.go
// argues for, here made durable. Agreement is counted and never recorded.
func TestAgreementProducesNothing(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginLegacy)},
	}
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-ada", Ref: "CN=VPN"}), in)

	if len(plan.Found) != 0 {
		t.Errorf("agreement produced %d finding(s): %+v", len(plan.Found), plan.Found)
	}
	if plan.Counts.Agrees != 1 {
		t.Errorf("counts.agrees = %d, want 1", plan.Counts.Agrees)
	}
}

// TestAnObservationOutsideTheScopeIsNamedRatherThanUsed.
//
// A reading may carry more than it promised to have read whole. That surplus
// cannot be treated as unmanaged — it is exactly the half-read the scope exists to
// refuse — and it must not be dropped either, because a caller who thinks they
// reconciled a group and did not needs to be told.
func TestAnObservationOutsideTheScopeIsNamedRatherThanUsed(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "vpn", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
			{ID: "sap", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=SAP"}}},
		},
	}
	plan := decideReconcile(reading("ad", []string{"CN=VPN"},
		rightObservation{Subject: "oid-ada", Ref: "CN=SAP"}), in)

	if plan.Counts.Unmanaged != 0 {
		t.Errorf("an observation outside the declared scope was treated as a finding: %+v", plan.Found)
	}
	if plan.Counts.OutOfScope != 1 {
		t.Errorf("counts.outOfScope = %d; a caller who thinks they reconciled CN=SAP and did "+
			"not has to be told", plan.Counts.OutOfScope)
	}
}

// TestAReferenceNothingModelsIsReportedOncePerReference, not once per holder.
//
// Eight hundred lines saying the same thing is one fact, and the fact is about the
// catalogue rather than about any of the eight hundred people.
func TestAReferenceNothingModelsIsReportedOncePerReference(t *testing.T) {
	in := reconcileInput{Users: []User{ada()}}
	plan := decideReconcile(reading("ad", []string{"CN=Unmodelled"},
		rightObservation{Subject: "oid-ada", Ref: "CN=Unmodelled"},
		rightObservation{Subject: "oid-ada", Ref: "CN=Unmodelled"}), in)

	if plan.Counts.NoItem != 1 {
		t.Errorf("counts.noItem = %d, want the reference reported once", plan.Counts.NoItem)
	}
	if plan.Counts.Unmanaged != 0 {
		t.Errorf("a reference no product claims produced %d finding(s); there is no inventory "+
			"side to compare it against", plan.Counts.Unmanaged)
	}
}

// TestAnEmptyReadingIsBelievedAndDoubtedOutLoud.
//
// A reading carrying nothing means, taken at its word, that everything in scope has
// gone. That is what the scope promise says and the findings are computed
// accordingly — a special case for zero would protect against one shape of a broken
// reading and not against a worker that returned half. What is special about zero is
// the prior, not the logic, so the answer says so where somebody will read it before
// acting on four hundred findings.
func TestAnEmptyReadingIsBelievedAndDoubtedOutLoud(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginOrdered)},
	}
	msg := reading("ad", []string{"CN=VPN"})
	plan := decideReconcile(msg, in)

	if plan.Counts.Missing != 1 {
		t.Fatalf("an empty reading over a scope with a recorded right found %d missing; the "+
			"contract says the scope was read whole", plan.Counts.Missing)
	}
	warn := reconcileWarning(plan, msg)
	if !strings.Contains(warn, "no observations") || !strings.Contains(warn, "broken") {
		t.Errorf("the warning reads %q; it has to say that an empty answer is more often a "+
			"failed read than an emptied estate, or somebody acts on the findings", warn)
	}
	// And it is only a warning where it is warranted: a reading that carried
	// something has no business being doubted this way.
	full := reading("ad", []string{"CN=VPN"}, rightObservation{Subject: "oid-ada", Ref: "CN=VPN"})
	if w := reconcileWarning(decideReconcile(full, in), full); w != "" {
		t.Errorf("a reading that carried observations was warned about: %q", w)
	}
}

// TestADraftProductIsNotComparedAndAWithdrawnOneIs — the same asymmetry the
// commissioning load holds, for the same reason: a draft may never be published, a
// withdrawal says nothing about who still holds the thing.
func TestADraftProductIsNotComparedAndAWithdrawnOneIs(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "draft", State: catalog.StateDraft,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=Draft"}}},
			{ID: "old", State: catalog.StateWithdrawn,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=Old"}}},
		},
	}
	plan := decideReconcile(reading("ad", []string{"CN=Draft", "CN=Old"},
		rightObservation{Subject: "oid-ada", Ref: "CN=Draft"},
		rightObservation{Subject: "oid-ada", Ref: "CN=Old"}), in)

	got := kindsOf(plan)
	if got["|draft|CN=Draft"] != recNoItem && got["usr_ada|draft|CN=Draft"] != recNoItem {
		if plan.InScopeItems["draft"] {
			t.Error("a draft product was compared; evidence would point at something that may " +
				"never be published")
		}
	}
	if got["usr_ada|old|CN=Old"] != recUnmanaged {
		t.Errorf("a withdrawn product was not compared (%q); withdrawal stops ordering and says "+
			"nothing about who holds it", got["usr_ada|old|CN=Old"])
	}
}

// TestAFindingKeepsItsIdentityAcrossRuns: the same disagreement tomorrow is the
// same record, not a second one. Without that the journal is a pile of samples
// wearing a transition's clothes.
func TestAFindingKeepsItsIdentityAcrossRuns(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginOrdered)},
	}
	msg := reading("ad", []string{"CN=VPN"})
	first, second := decideReconcile(msg, in), decideReconcile(msg, in)

	if len(first.Found) != 1 || len(second.Found) != 1 {
		t.Fatalf("want one finding each, got %d and %d", len(first.Found), len(second.Found))
	}
	if first.Found[0].ID != second.Found[0].ID {
		t.Errorf("the same disagreement got two ids: %q then %q",
			first.Found[0].ID, second.Found[0].ID)
	}

	// And the two directions of one pair are two findings, not one that flips: a
	// right that went missing and later turned up unmanaged has two histories.
	unmanaged := discrepancyID(recUnmanaged, "ad", "usr_ada", "vpn")
	if unmanaged == first.Found[0].ID {
		t.Error("the unmanaged and missing findings for one pair share an id, so one would " +
			"overwrite the other's history")
	}
}

// TestTheJournalRecordsEpisodesRatherThanRewritingHistory.
//
// A membership somebody keeps re-adding is itself the finding, and a journal of one
// record per identity hides it perfectly unless reopening is counted.
func TestTheJournalRecordsEpisodesRatherThanRewritingHistory(t *testing.T) {
	found := discrepancy{
		ID: "x", Kind: recUnmanaged, System: "ad", Principal: "usr_ada", ItemID: "vpn",
	}
	first, _ := seenDiscrepancy(discrepancyRecord{}, found, 100)
	if first.Episodes != 1 || first.OpenedAt != 100 {
		t.Fatalf("first sighting = %+v", first)
	}

	// Seen again: the moment moves, the episode does not.
	again, changed := seenDiscrepancy(first, found, 200)
	if !changed || again.Episodes != 1 || again.OpenedAt != 100 || again.LastSeenAt != 200 {
		t.Errorf("a second sighting = %+v, want the same episode with a later last-seen", again)
	}

	// Closed, then back: a new episode that remembers how the last one ended.
	closed := closeDiscrepancy(again, closedAdopted, "usr_root", 300)
	back, _ := seenDiscrepancy(closed, found, 400)
	switch {
	case back.Episodes != 2:
		t.Errorf("episodes = %d after it came back, want 2", back.Episodes)
	case back.PreviousClosedHow != closedAdopted:
		t.Errorf("previousClosedHow = %q, want the closure it is returning from", back.PreviousClosedHow)
	case !back.Open():
		t.Error("a finding that came back is still marked closed")
	case back.OpenedAt != 400:
		t.Errorf("openedAt = %d, want the new episode's start", back.OpenedAt)
	}
}
