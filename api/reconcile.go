package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// Comparing what Atlas believes against what a target system actually holds
// (ADR-draft-reconciliation).
//
// The inventory asserts something Atlas cannot guarantee: that a right exists in
// another system. Target systems are changed outside Atlas — by an administrator
// in a hurry, by a script, by a merger — so the assertion decays, silently, and an
// inventory nobody checks is a list of things that were once true.
//
// # Silence is the whole difficulty
//
// A commissioning load reports what it *found* and never what it did not: a batch
// is one system's partial answer, and treating its silence as evidence of removal
// would revoke rights because a paginated read stopped early. Reconciliation is
// the opposite by necessity — absence *is* the finding — and that makes the same
// silence dangerous in exactly the place the load was safe.
//
// So a run declares a **scope**: the references it read *completely*. Inside that
// scope, silence means something and both directions are computed. Outside it,
// nothing is said at all — an entitlement whose item is not in scope is not
// missing, it is unexamined, and a reconciliation that could not tell those apart
// would report the whole inventory as wrong on its first partial read.
//
// # Two directions, and they are not symmetrical
//
//   - **Unmanaged**: the target system grants it, Atlas has no record. Somebody
//     has access nobody here decided to give them.
//   - **Missing**: Atlas records it, the target system does not. Atlas is asserting
//     something untrue — and until somebody looks, it will keep asserting it.
//
// The first is the one people expect. The second is the one that corrupts the
// evidence, because an inventory that is wrong in this direction answers "who had
// access when" with a confident falsehood.
//
// # Nothing here acts
//
// This file decides and reports. Every action is a separate, deliberate call by a
// person (reconcileactions.go). A system that silently removed privileges it did
// not grant would lock a company out on its first bad read, and the value Atlas
// adds is naming the discrepancy — which nothing else in the estate can do.

// reconcileMessage is what one reading of one target system reports for
// comparison.
//
// It carries the same observations a commissioning load does, deliberately: the
// same worker reads the same thing, and two vocabularies for one fact would be two
// things to keep in step. What it adds is [reconcileMessage.Refs] — and that one
// field is the whole difference between the two endpoints.
type reconcileMessage struct {
	// System names the target system, matched against [catalog.TargetRef.System].
	System string `json:"system"`

	// Refs is the scope: the references this run read **completely**. It is
	// required, and a run naming none is refused rather than treated as "the whole
	// system" — a default that guessed at completeness would turn a truncated read
	// into a report that every right Atlas knows about has vanished.
	//
	// "Completely" is a promise the caller makes and Atlas cannot check. It is the
	// one thing in this exchange that rests on the model being written correctly,
	// which is why the shipped example reads whole groups and says so in its own
	// documentation rather than leaving it to be assumed.
	Refs []string `json:"refs"`

	// Subjects is the other axis of the same promise: the subjects whose holdings
	// in this system this run read **completely**. It answers a question `Refs`
	// structurally cannot — *is this person out of everything?* — and that is the
	// question an offboarding asks.
	//
	// A group listing cannot answer it. To learn that a leaver holds nothing you
	// would have to reconcile every group in the system and notice their absence
	// from all of them, which is a whole-system run wearing a disguise. Reading one
	// person's memberships is a different call to the target system and a different
	// promise, so it is a different field.
	//
	// Named in the target system's vocabulary, exactly as an observation's subject
	// is — a directory object id or a mail address, never an Atlas principal id.
	Subjects []string `json:"subjects"`

	// Observations are who was found holding what, within that scope. An
	// observation outside both promises is reported and otherwise ignored: it is a
	// fact the run did not claim to have read whole, and counting it as unmanaged
	// would be acting on exactly the half-read the scope exists to prevent.
	Observations []rightObservation `json:"observations"`
}

// What a comparison can conclude about one (principal, item) pair inside the
// scope. Named rather than derived: the journal groups by them, and the report is
// read by somebody deciding, so the words are the interface.
const (
	// recUnmanaged is held in the target system and absent from the inventory.
	recUnmanaged = "unmanaged"
	// recMissing is recorded in the inventory and absent from the target system.
	// This is the direction that makes the inventory lie, and the one an operator
	// is least likely to expect.
	recMissing = "missing"
	// recAgrees is recorded and observed. It produces no journal entry — a
	// hundred identical readings must produce nothing, or the journal becomes a
	// stream of "healthy" nobody reads (the shape api/panorama/drift.go already
	// argues for, here made durable because it is evidence rather than a view).
	recAgrees = "agrees"
	// recNoSubject is an observed subject that resolves to no account.
	recNoSubject = "no-subject"
	// recOutOfScope is an observation naming a reference the run did not claim to
	// have read completely.
	recOutOfScope = "out-of-scope"
	// recNoItem is a reference in scope that no product claims.
	recNoItem = "no-item"
	// recAmbiguous is a reference two products claim.
	recAmbiguous = "ambiguous"
)

// discrepancy is one disagreement, complete enough to be journalled and acted on
// without a second comparison.
type discrepancy struct {
	// ID is stable across runs: the same disagreement seen tomorrow is the same
	// record, not a second one. That is what makes the journal a history of
	// transitions rather than a pile of samples.
	ID   string `json:"id"`
	Kind string `json:"kind"`

	System string `json:"system"`
	// Ref is the reference in the target system, empty for a missing right whose
	// item declares several — the item is what is missing, not one of its names.
	Ref       string `json:"ref,omitempty"`
	Principal string `json:"principal"`
	ItemID    string `json:"itemId"`

	// Origin is what the inventory says about a right it holds: ordered, adopted
	// or legacy. Empty for an unmanaged one, which the inventory does not hold at
	// all. It travels because it decides how alarming a *missing* finding is: an
	// ordered right that vanished is a provisioning that came undone, a legacy one
	// is quite possibly a group somebody tidied up years ago.
	Origin string `json:"origin,omitempty"`

	// Why says, in a sentence, what a reader is looking at.
	Why string `json:"why,omitempty"`
}

// discrepancyID is the stable identity of one disagreement.
//
// It is derived from what the disagreement *is* — system, item, principal, kind —
// and not from when it was found, so the run that finds it again updates the
// record rather than appending beside it. Kind is part of it because a right that
// went missing and later turned up unmanaged is two findings with two histories,
// not one record that flipped.
func discrepancyID(kind, system, principal, itemID string) string {
	return kind + ":" + system + ":" + principal + ":" + itemID
}

// reconcilePlan is everything one run concluded.
type reconcilePlan struct {
	// Found are the disagreements, sorted so two runs over the same input produce
	// the same report.
	Found []discrepancy
	// Notes are the observations that could not be compared at all.
	Notes  []discrepancy
	Counts reconcileCounts
	// The scope, as the two promises resolved to. The journal needs all three:
	// closing a disagreement nobody saw this time is only sound for the pairs this
	// run actually examined, and "examined" is now a question about a pair rather
	// than about an item.
	//
	// InScopeItems are the items the references resolved to. InScopeSubjects are the
	// principals the subjects resolved to. SystemItems is every item this system's
	// references can be attributed to — without it a subject-scoped run would report
	// somebody's Jira rights as missing from Active Directory, which is true of
	// nothing.
	InScopeItems    map[string]bool
	InScopeSubjects map[string]bool
	SystemItems     map[string]bool
}

// examined reports whether this run was entitled to conclude anything about one
// (principal, item) pair. Either promise covers it, and they compose: a run may
// say "these two groups whole, and everything Ada has".
func (p reconcilePlan) examined(principal, itemID string) bool {
	return p.InScopeItems[itemID] ||
		(p.InScopeSubjects[principal] && p.SystemItems[itemID])
}

// reconcileCounts is the run by conclusion.
type reconcileCounts struct {
	Unmanaged  int `json:"unmanaged"`
	Missing    int `json:"missing"`
	Agrees     int `json:"agrees"`
	NoSubject  int `json:"noSubject"`
	OutOfScope int `json:"outOfScope"`
	NoItem     int `json:"noItem"`
	Ambiguous  int `json:"ambiguous"`
}

// reconcileInput is the state one run is decided against. It is passed rather than
// read so the comparison is a pure function of its argument: the same input
// produces the same plan, which is what lets a report be reproduced and a run be
// compared with the one before it.
type reconcileInput struct {
	Users []User
	Items []catalog.Item
	// Held is the whole inventory. Reconciliation is the one caller that needs it
	// whole — see [state.queries.Entitlements] for why the key cannot answer "who
	// holds this" any other way.
	Held []model.EntitlementValue
}

// decideReconcile compares one reading against the inventory. It writes nothing.
func decideReconcile(msg reconcileMessage, in reconcileInput) reconcilePlan {
	system := strings.TrimSpace(msg.System)

	byDirectory, byEmail := map[string]User{}, map[string]User{}
	for _, u := range in.Users {
		if u.DirectoryID != "" {
			byDirectory[u.DirectoryID] = u
		}
		if u.Email != "" {
			byEmail[strings.ToLower(u.Email)] = u
		}
	}
	resolve := func(subject string) (User, bool) {
		if u, ok := byDirectory[subject]; ok {
			return u, true
		}
		u, ok := byEmail[strings.ToLower(subject)]
		return u, ok
	}

	itemByRef, ambiguous := indexTargets(in.Items, system)

	plan := reconcilePlan{
		InScopeItems:    map[string]bool{},
		InScopeSubjects: map[string]bool{},
		SystemItems:     map[string]bool{},
	}
	// SystemItems is built from the same index the comparison uses, and not from the
	// catalogue directly. That is the whole point of taking it from here: a draft
	// product is not indexed, and neither is the loser of a reference two products
	// claim — so neither can enter a subject scope. Reading the catalogue again
	// would put both back, and a recorded right on either would then be reported
	// missing by a leaver run for no better reason than that nothing observed it.
	for _, it := range itemByRef {
		plan.SystemItems[it.ID] = true
	}
	add := func(d discrepancy) { plan.Found = append(plan.Found, d) }
	note := func(d discrepancy) { plan.Notes = append(plan.Notes, d) }

	// The scope, resolved to items. A reference nobody models cannot be compared —
	// there is no inventory side to compare it against — and one two products claim
	// cannot be attributed. Both are reported rather than dropped: on a first run
	// they are usually the most actionable thing in the answer.
	scopeRefs := map[string]bool{}
	for _, raw := range msg.Refs {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		scopeRefs[ref] = true
		switch others, clash := ambiguous[ref]; {
		case clash:
			plan.Counts.Ambiguous++
			note(discrepancy{Kind: recAmbiguous, System: system, Ref: ref,
				Why: "products " + strings.Join(others, ", ") + " all claim this reference, so " +
					"nothing read under it can be attributed. Nothing in scope for it was compared"})
		default:
			it, known := itemByRef[ref]
			if !known {
				plan.Counts.NoItem++
				note(discrepancy{Kind: recNoItem, System: system, Ref: ref,
					Why: "no product claims this reference in system " + system + ", so there is " +
						"no inventory side to compare it against. What holds it is unmodelled " +
						"rather than wrong"})
				continue
			}
			plan.InScopeItems[it.ID] = true
		}
	}

	// The other axis. A subject that resolves to no account is reported rather than
	// dropped: a run that promised to have read everything a leaver holds, and named
	// somebody Atlas has never heard of, has verified nothing about them — and that
	// is precisely the case an offboarding must not read as "clean".
	for _, raw := range msg.Subjects {
		subject := strings.TrimSpace(raw)
		if subject == "" {
			continue
		}
		u, found := resolve(subject)
		if !found {
			plan.Counts.NoSubject++
			note(discrepancy{Kind: recNoSubject, System: system, Principal: subject,
				Why: "this run claimed to have read everything this subject holds, and no account " +
					"matches them by directory id or mail. Nothing was verified about them — an " +
					"offboarding reading this as a clean result would be reading the absence of " +
					"an account as the absence of access"})
			continue
		}
		plan.InScopeSubjects[u.ID] = true
	}

	// What the target system says, as (principal, item) pairs.
	observed := map[string]bool{}
	refOf := map[string]string{}
	for _, obs := range msg.Observations {
		subject, ref := strings.TrimSpace(obs.Subject), strings.TrimSpace(obs.Ref)
		if subject == "" || ref == "" {
			continue
		}
		// The subject is resolved before the scope is decided, because one of the two
		// promises is about the subject. A reference nobody named is still in scope
		// when the person holding it is: a run that read everything Ada has read this
		// too, whichever group it came from.
		u, found := resolve(subject)
		inSubjectScope := found && plan.InScopeSubjects[u.ID]
		if !scopeRefs[ref] && !inSubjectScope {
			plan.Counts.OutOfScope++
			note(discrepancy{Kind: recOutOfScope, System: system, Ref: ref, Principal: subject,
				Why: "this run did not claim to have read " + ref + " completely, nor everything " +
					"this subject holds, so what it carries about the pair says nothing either " +
					"way. Name the reference in refs, or the subject in subjects"})
			continue
		}
		it, known := itemByRef[ref]
		if !known {
			if inSubjectScope {
				// Reported per holder here rather than per reference: in ref scope the
				// fact is about the catalogue, but a run that read one person's whole
				// estate has found something *they* hold that nothing models, and the
				// person is the subject of the answer.
				plan.Counts.NoItem++
				note(discrepancy{Kind: recNoItem, System: system, Ref: ref, Principal: subject,
					Why: "no product claims this reference in system " + system + ". This subject " +
						"holds something the catalogue does not model, so there is no inventory " +
						"side to compare it against"})
			}
			continue // in ref scope it is already reported once, against the reference
		}
		if !found {
			plan.Counts.NoSubject++
			note(discrepancy{Kind: recNoSubject, System: system, Ref: ref, Principal: subject,
				ItemID: it.ID,
				Why: "no account matches this subject by directory id or mail, so what they hold " +
					"cannot be compared with anything. Run the directory mirror first"})
			continue
		}
		key := u.ID + "\x00" + it.ID
		observed[key] = true
		refOf[key] = ref
	}

	// What Atlas says, for the pairs in scope only — by either promise.
	recorded := map[string]model.EntitlementValue{}
	for _, v := range in.Held {
		if plan.examined(v.Principal, v.ItemID) {
			recorded[v.Principal+"\x00"+v.ItemID] = v
		}
	}

	for key := range observed {
		principal, itemID, _ := strings.Cut(key, "\x00")
		if _, has := recorded[key]; has {
			plan.Counts.Agrees++
			continue
		}
		plan.Counts.Unmanaged++
		add(discrepancy{
			ID:   discrepancyID(recUnmanaged, system, principal, itemID),
			Kind: recUnmanaged, System: system, Ref: refOf[key],
			Principal: principal, ItemID: itemID,
			Why: "held in " + system + " and not recorded here. Somebody has this and nothing " +
				"in Atlas decided to give it to them — adopt it if it is legitimate, or run the " +
				"product's deprovisioning if it is not",
		})
	}

	for key, v := range recorded {
		if observed[key] {
			continue
		}
		plan.Counts.Missing++
		add(discrepancy{
			ID:   discrepancyID(recMissing, system, v.Principal, v.ItemID),
			Kind: recMissing, System: system,
			Principal: v.Principal, ItemID: v.ItemID, Origin: v.Origin.String(),
			Why: "recorded here as " + v.Origin.String() + " and not held in " + system + ". " +
				"Atlas is asserting something the target system does not agree with, and will " +
				"keep asserting it until somebody decides which of the two is right",
		})
	}

	sortDiscrepancies(plan.Found)
	sortDiscrepancies(plan.Notes)
	return plan
}

// indexTargets builds the reference → product index for one system, and records
// the references more than one product claims.
//
// A draft product is not indexed, for the reason the commissioning load gives: a
// draft may never be published, so nothing should be compared against it. A
// withdrawn one is, because withdrawal stops a product being ordered and says
// nothing about who still holds it.
func indexTargets(items []catalog.Item, system string) (map[string]catalog.Item, map[string][]string) {
	byRef := map[string]catalog.Item{}
	clashes := map[string][]string{}

	sorted := append([]catalog.Item(nil), items...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].ID < sorted[b].ID })
	for _, it := range sorted {
		if it.State == catalog.StateDraft {
			continue
		}
		for _, ref := range it.Targets {
			if !strings.EqualFold(strings.TrimSpace(ref.System), system) {
				continue
			}
			if other, taken := byRef[ref.Ref]; taken {
				if len(clashes[ref.Ref]) == 0 {
					clashes[ref.Ref] = []string{other.ID}
				}
				clashes[ref.Ref] = append(clashes[ref.Ref], it.ID)
				continue
			}
			byRef[ref.Ref] = it
		}
	}
	return byRef, clashes
}

// sortDiscrepancies puts a plan in an order two runs over one input agree on. A
// report that shuffled between runs could not be diffed against the one before it,
// which is the only way anybody notices that a finding is new.
func sortDiscrepancies(ds []discrepancy) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Kind != ds[j].Kind {
			return ds[i].Kind < ds[j].Kind
		}
		if ds[i].ItemID != ds[j].ItemID {
			return ds[i].ItemID < ds[j].ItemID
		}
		if ds[i].Principal != ds[j].Principal {
			return ds[i].Principal < ds[j].Principal
		}
		return ds[i].Ref < ds[j].Ref
	})
}

// reconcileTooManyObservations is the refusal for a batch above its budget.
func reconcileTooManyObservations(got, limit int) string {
	return fmt.Sprintf("this reading carries %d observations and the ceiling is %d (%s). "+
		"It is refused whole rather than truncated, and here that matters more than usual: a "+
		"truncated reading is a reading that is not complete for its scope, and this endpoint "+
		"reads absence as a finding. Half a group's members, reported as the whole group, is a "+
		"report that everybody missing from the half has lost their access. Split the run by "+
		"reference instead — each run naming the references it read whole",
		got, limit, "ATLAS_LIMIT_RECONCILE_OBSERVATIONS")
}
