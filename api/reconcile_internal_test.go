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

// The other axis of the same promise: a scope by subject
// (ADR-draft-reconciliation).
//
// `refs` answers "is this group's membership what we think". It structurally
// cannot answer "is this person out of everything", because absence from a group
// nobody named says nothing — and that second question is the one an offboarding
// asks.

// TestASubjectScopeExaminesEverythingThatPersonHoldsInTheSystem.
//
// This is the leaver check. Ada's VPN right is recorded and she is not in the
// group; no run named CN=VPN, so a ref-scoped run would conclude nothing. Naming
// *her* is a different promise — "everything Ada holds here, read whole" — and it
// makes the same silence a finding.
func TestASubjectScopeExaminesEverythingThatPersonHoldsInTheSystem(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
		Held: []model.EntitlementValue{heldBy("usr_ada", "vpn", model.OriginOrdered)},
	}

	// Named by reference only, and CN=VPN is not among them: nothing is concluded.
	byRef := decideReconcile(reconcileMessage{System: "ad", Refs: []string{"CN=Other"}}, in)
	if byRef.Counts.Missing != 0 {
		t.Errorf("a run that named neither CN=VPN nor Ada concluded %d missing; it read "+
			"neither", byRef.Counts.Missing)
	}

	// Named by subject: the same silence is now a finding.
	bySubject := decideReconcile(reconcileMessage{System: "ad", Subjects: []string{"oid-ada"}}, in)
	if bySubject.Counts.Missing != 1 {
		t.Fatalf("a run that read everything Ada holds found %d missing, want the VPN right "+
			"it records and the system does not have: %+v", bySubject.Counts.Missing, bySubject)
	}
	if !bySubject.examined("usr_ada", "vpn") {
		t.Error("the pair is reported and not marked examined, so the journal would never " +
			"close the finding when it is repaired")
	}
}

// TestASubjectScopeSaysNothingAboutAnotherSystem.
//
// "Everything Ada holds in Active Directory" is a promise about Active Directory.
// Without the system filter a subject-scoped run would report her Jira rights as
// missing from AD — true of nothing, and the kind of finding that teaches people
// to ignore findings.
func TestASubjectScopeSaysNothingAboutAnotherSystem(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "vpn", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
			{ID: "jira", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "jira", Ref: "jira-users"}}},
			{ID: "laptop", State: catalog.StateActive}, // nothing outside grants it
		},
		Held: []model.EntitlementValue{
			heldBy("usr_ada", "vpn", model.OriginOrdered),
			heldBy("usr_ada", "jira", model.OriginOrdered),
			heldBy("usr_ada", "laptop", model.OriginOrdered),
		},
	}
	plan := decideReconcile(reconcileMessage{System: "ad", Subjects: []string{"oid-ada"}}, in)

	if plan.Counts.Missing != 1 {
		t.Errorf("counts.missing = %d, want only the one right this system could have had: "+
			"%+v", plan.Counts.Missing, plan.Found)
	}
	if plan.examined("usr_ada", "jira") {
		t.Error("a right that lives in another system was examined by an Active Directory run")
	}
	if plan.examined("usr_ada", "laptop") {
		t.Error("a product no target system grants was examined; nothing out there could " +
			"ever confirm it, so every run would report it missing forever")
	}
}

// TestTheTwoPromisesCompose: a run may say "these groups whole, and everything Ada
// has". Neither narrows the other.
func TestTheTwoPromisesCompose(t *testing.T) {
	bo := User{ID: "usr_bo", Username: "bo", Email: "bo@example.org", DirectoryID: "oid-bo"}
	in := reconcileInput{
		Users: []User{ada(), bo},
		Items: []catalog.Item{
			{ID: "vpn", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}},
			{ID: "sap", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=SAP"}}},
		},
		Held: []model.EntitlementValue{
			heldBy("usr_bo", "vpn", model.OriginOrdered),  // in ref scope, not in subject scope
			heldBy("usr_ada", "sap", model.OriginOrdered), // in subject scope, not in ref scope
		},
	}
	plan := decideReconcile(reconcileMessage{
		System: "ad", Refs: []string{"CN=VPN"}, Subjects: []string{"oid-ada"},
	}, in)

	if plan.Counts.Missing != 2 {
		t.Errorf("counts.missing = %d, want one from each promise: %+v", plan.Counts.Missing, plan.Found)
	}
	for _, pair := range []struct{ principal, item string }{{"usr_bo", "vpn"}, {"usr_ada", "sap"}} {
		if !plan.examined(pair.principal, pair.item) {
			t.Errorf("%s/%s was not examined; one promise is narrowing the other",
				pair.principal, pair.item)
		}
	}
}

// TestAnObservationIsInScopeWhenEitherPromiseCoversIt.
//
// A run that read everything Ada holds read whatever group it came from. Refusing
// it as out of scope because no `refs` entry named that group would throw away the
// finding the run was performed to produce.
func TestAnObservationIsInScopeWhenEitherPromiseCoversIt(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "sap", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=SAP"}}}},
	}
	plan := decideReconcile(reconcileMessage{
		System: "ad", Subjects: []string{"oid-ada"},
		Observations: []rightObservation{{Subject: "oid-ada", Ref: "CN=SAP"}},
	}, in)

	if plan.Counts.OutOfScope != 0 {
		t.Errorf("an observation about a subject the run read whole was refused as out of "+
			"scope: %+v", plan.Notes)
	}
	if plan.Counts.Unmanaged != 1 {
		t.Errorf("counts.unmanaged = %d, want the right Ada holds and Atlas does not record",
			plan.Counts.Unmanaged)
	}
}

// TestASubjectWithNoAccountIsNeverACleanResult.
//
// The failure that would make an offboarding verification worthless: a run names a
// leaver Atlas has never heard of, resolves them to nobody, compares nothing, and
// reports no findings. Read as "they are out of everything" that is the absence of
// an account being mistaken for the absence of access.
func TestASubjectWithNoAccountIsNeverACleanResult(t *testing.T) {
	in := reconcileInput{
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
	}
	msg := reconcileMessage{System: "ad", Subjects: []string{"ghost@example.org"}}
	plan := decideReconcile(msg, in)

	if plan.Counts.NoSubject != 1 {
		t.Fatalf("counts.noSubject = %d, want the unresolvable subject reported", plan.Counts.NoSubject)
	}
	if len(plan.InScopeSubjects) != 0 {
		t.Error("an unresolvable subject was taken into scope, so the run would claim to have " +
			"verified somebody it cannot name")
	}
	why := plan.Notes[0].Why
	if !strings.Contains(why, "offboarding") || !strings.Contains(why, "absence of") {
		t.Errorf("the note reads %q; it has to say that this is not a clean result, or the "+
			"one reader who needs the warning will not get it", why)
	}
	// And the report must not congratulate them: the reason for an empty answer
	// names the unresolved scope rather than a verified leaver.
	if r := reconcileReason(plan, msg); !strings.Contains(r, "nothing was compared") {
		t.Errorf("the reason reads %q, want it to say nothing was compared", r)
	}
}

// TestACleanLeaverGetsItsOwnSentence: the answer an offboarding wants is an empty
// one, and an empty answer that reads like "nothing happened" wastes the run that
// actually confirmed something.
func TestACleanLeaverGetsItsOwnSentence(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
			Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}},
	}
	msg := reconcileMessage{System: "ad", Subjects: []string{"oid-ada"}}
	plan := decideReconcile(msg, in)

	if len(plan.Found) != 0 {
		t.Fatalf("a leaver holding nothing produced findings: %+v", plan.Found)
	}
	r := reconcileReason(plan, msg)
	if !strings.Contains(r, "out of it") {
		t.Errorf("the reason reads %q; the one run that confirms a leaver is clean should "+
			"say so rather than report an absence of news", r)
	}
}

// TestASubjectScopeInheritsTheCatalogueJudgementsRatherThanRepeatingThem.
//
// A subject scope has to decide which items belong to the system it names, and the
// tempting way to do that is to read the catalogue again. It is wrong twice over,
// and both mistakes produce the same shape of falsehood — a right reported missing
// because nothing observed it, when nothing *could* have observed it.
//
// A draft product is not compared, by the rule the reference scope already keeps: a
// right held against something that may never be published is not evidence. And a
// reference two products claim is attributable to neither. Deriving the system's
// items from the comparison's own index is what makes those two judgements hold on
// both axes rather than on one.
func TestASubjectScopeInheritsTheCatalogueJudgementsRatherThanRepeatingThem(t *testing.T) {
	in := reconcileInput{
		Users: []User{ada()},
		Items: []catalog.Item{
			{ID: "draft", State: catalog.StateDraft,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=Draft"}}},
			{ID: "shared-a", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=Shared"}}},
			{ID: "shared-b", State: catalog.StateActive,
				Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=Shared"}}},
		},
		Held: []model.EntitlementValue{
			heldBy("usr_ada", "draft", model.OriginLegacy),
			heldBy("usr_ada", "shared-b", model.OriginLegacy),
		},
	}

	// Everything Ada holds in AD was read, and the reading found nothing.
	plan := decideReconcile(reconcileMessage{System: "ad", Subjects: []string{"oid-ada"}}, in)

	if plan.Counts.Missing != 0 {
		t.Errorf("a leaver run reported %d right(s) missing: %+v. Neither of these can be "+
			"observed — one hangs off a draft product, the other off a reference two products "+
			"claim — so absence says nothing about either",
			plan.Counts.Missing, kindsOf(plan))
	}
	if plan.SystemItems["draft"] {
		t.Error("a draft product entered a subject scope; the reference scope refuses to " +
			"compare one, and the two axes must not disagree about the catalogue")
	}
	if plan.SystemItems["shared-b"] {
		t.Error("the loser of a contested reference entered a subject scope; nothing read " +
			"under that reference can be attributed to either product")
	}
}
