package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// Filling the inventory on the day the portal is commissioned
// (ADR-draft-inventory-commissioning-load).
//
// On that day the inventory is empty and reality is full. Everything the estate
// already grants — every group membership, every licence — is a right Atlas knows
// nothing about, and the first reconciliation would report every one of them as a
// discrepancy carrying an executable "remove it in the target system". That is how
// a new access-governance tool locks a company out of itself on its first run.
//
// So the rights that exist are read and written down first, with origin
// [model.OriginLegacy]: *this predates us, we did not grant it, and we are not
// claiming we did*. That origin has existed since ADR-0312 and has had no writer
// until now; this is it.
//
// # Two resolutions, both of which can fail honestly
//
// A target system reports a *subject* (a directory object id, a mail address) and a
// *right* (a group, a SKU). An entitlement holds a principal id and a catalogue item
// id. Neither side matches without a lookup:
//
//   - subject → principal, through the account the directory mirror already wrote
//     (ADR-0332): by directory id, else by mail;
//   - right → item, through [catalog.TargetRef], which is the item's own claim about
//     what it is called out there.
//
// Either can come up empty, and an unresolved observation is *reported*, never
// dropped. The unmatched ones are the most valuable thing a first load produces:
// unresolved subjects are people in the estate with no account here, and unresolved
// refs are rights the estate grants that no catalogue models. Nobody could have
// listed either before.
//
// # The shape of this file
//
// The same two-function split the directory mirror uses, for the same reason.
// [decideInventoryLoad] produces a complete plan and writes nothing;
// [applyInventoryPlan] writes that plan and decides nothing. The reporting run is
// the first half alone, so a preview and a real run cannot drift apart — there is
// only one decision. TestAReportPredictsTheLoadExactly holds the two against the
// same input.

// inventoryLoadMessage is what one reading of one target system reports.
//
// The mode field is `apply`, not `dryRun`, and the asymmetry is the same one the
// directory mirror argues: an absent JSON field decodes to the zero value, so a
// message that forgot to say what it wanted writes nothing. Spelled the other way,
// the same omission would enter a target system's entire membership list into an
// evidence store kept for years.
type inventoryLoadMessage struct {
	// Apply is the mode. False — including absent — decides everything and writes
	// nothing.
	Apply bool `json:"apply"`

	// System names the target system this batch was read from, in the installation's
	// own vocabulary. It is matched against [catalog.TargetRef.System], so a
	// misspelling here resolves nothing and says so in the report rather than
	// attributing rights to the wrong products.
	System string `json:"system"`

	Observations []rightObservation `json:"observations"`
}

// rightObservation is one thing one subject was found to hold.
//
// There is deliberately no "absent" half. A load reports what it *found*; it never
// reports what it did not find, because a batch is one system's answer and often
// only part of it, and treating silence as evidence of removal would revoke rights
// on the strength of a paginated read that stopped early. Revocation is
// reconciliation's job, with a person deciding, and it is not in this file.
type rightObservation struct {
	// Subject is who holds it, as the target system names them: a directory object
	// id, or a mail address. Never an Atlas principal id — a target system has never
	// heard of one, and accepting it here would let a caller attribute a right to an
	// account without the directory agreeing that the two are the same person.
	Subject string `json:"subject"`

	// Ref is the right, as the target system names it.
	Ref string `json:"ref"`

	// VariantID is the variant held, where the reading system can tell them apart.
	// Empty is the ordinary answer: most target systems have no notion of a variant,
	// and guessing one from a group name would be Atlas inventing a fact.
	VariantID string `json:"variantId,omitempty"`

	// Since is when the hold began, if the system knows. Most do not — a directory
	// records that a membership exists, rarely when it started — and zero means
	// exactly that, at which point the load's own moment is recorded instead. See
	// [inventoryDecider.since] for why that is the honest choice and not a
	// convenient one.
	Since int64 `json:"since,omitempty"`
}

// What a plan may decide about one observation. Named rather than derived, because
// the report groups by them and because "this is new" and "somebody already holds
// it by a better authority" are entirely different events that both write nothing
// new.
const (
	invGrant = "grant"
	// invHeld is an entitlement already recorded as legacy. Nothing is written: a
	// second load must not move Since, or every re-run would make the whole estate
	// look freshly acquired and the age of a right — the only thing Since is for —
	// would be destroyed by the act of checking.
	invHeld = "held"
	// invKeep is an entitlement already recorded with a *better* origin: ordered or
	// adopted. This is the one that must never be written, and the reason the load
	// reads the inventory at all. GrantEntitlement replaces, so writing legacy over
	// an ordered right would silently discard the order that produced it and the
	// approval behind it — the single most destructive thing a load could do, and
	// invisible afterwards.
	invKeep = "keep"
	// invNoSubject is an observation whose subject resolves to no account.
	invNoSubject = "no-subject"
	// invNoItem is an observation whose ref no product claims.
	invNoItem = "no-item"
	// invAmbiguous is an observation whose ref two products claim. Publish refuses
	// this within one release; across releases it is only visible here.
	invAmbiguous = "ambiguous"
	// invMalformed is an observation missing a half.
	invMalformed = "malformed"
)

// inventoryDecision is what the plan decided about one observation, complete enough
// that applying it needs no second thought.
type inventoryDecision struct {
	Action string `json:"action"`

	// Subject and Ref are echoed as the message gave them, so a report can be read
	// beside the target system's own export without a second lookup.
	Subject string `json:"subject"`
	Ref     string `json:"ref"`

	// Principal and ItemID are what they resolved to, empty where they did not.
	Principal string `json:"principal,omitempty"`
	ItemID    string `json:"itemId,omitempty"`

	// Value is the entitlement to write. It is filled only for invGrant, and it is
	// carried whole rather than rebuilt at apply time: the point of the split is
	// that applying re-decides nothing.
	Value model.EntitlementValue `json:"-"`

	// Why says, in a sentence, what a reader should do about it. Filled for
	// everything that is not a plain grant.
	Why string `json:"why,omitempty"`
}

// inventoryCounts is the plan by action. Numbers, so they are unbounded.
type inventoryCounts struct {
	Grant     int `json:"grant"`
	Held      int `json:"held"`
	Keep      int `json:"keep"`
	NoSubject int `json:"noSubject"`
	NoItem    int `json:"noItem"`
	Ambiguous int `json:"ambiguous"`
	Malformed int `json:"malformed"`
}

// inventoryPlan is everything one message decided.
type inventoryPlan struct {
	Grants   []inventoryDecision
	Notes    []inventoryDecision
	Counts   inventoryCounts
	Unmapped []unmappedRef
}

// unmappedRef is one right the estate grants that no product claims, with how many
// subjects hold it.
//
// This is rolled up rather than listed per person, and that is the difference
// between a report somebody reads and one they scroll past: eight hundred lines
// saying "nobody models CN=AllStaff" is one fact, and it is the fact the load was
// worth running for. It is what nothing in the estate could produce before —
// a list of what is granted that nobody decided to offer.
type unmappedRef struct {
	Ref      string `json:"ref"`
	Subjects int    `json:"subjects"`
}

// inventoryDecider carries the indexes and the plan as it grows.
type inventoryDecider struct {
	system string
	now    int64

	// byDirectory and byEmail resolve a subject to an account. Mail is matched
	// case-insensitively because mail addresses are; a directory object id is not,
	// because it is an opaque identifier and two that differ in case are two
	// identifiers.
	byDirectory map[string]User
	byEmail     map[string]User

	// itemByRef resolves a right to a product, and ambiguous names the refs two
	// products claim. Both are built from the whole item store rather than from one
	// release, which is where a clash across catalogues becomes visible at all.
	itemByRef map[string]catalog.Item
	ambiguous map[string][]string

	// held answers what a principal already holds, as the inventory stood when this
	// plan was decided.
	held func(principal, itemID string) (*model.EntitlementValue, bool)

	// granting records what this plan already decided to grant, so the same
	// (person, item) arriving twice in one message — two groups mapping to one
	// product, which is the ordinary case — is one grant and not two.
	granting map[string]bool

	plan inventoryPlan
}

// decideInventoryLoad turns one message into a plan, against the accounts, products
// and inventory as they stand. It writes nothing and reads nothing but its
// arguments.
func decideInventoryLoad(msg inventoryLoadMessage, users []User, items []catalog.Item,
	held func(principal, itemID string) (*model.EntitlementValue, bool), now int64) inventoryPlan {

	d := &inventoryDecider{
		system:      strings.TrimSpace(msg.System),
		now:         now,
		byDirectory: map[string]User{},
		byEmail:     map[string]User{},
		itemByRef:   map[string]catalog.Item{},
		ambiguous:   map[string][]string{},
		held:        held,
		granting:    map[string]bool{},
	}
	for _, u := range users {
		if u.DirectoryID != "" {
			d.byDirectory[u.DirectoryID] = u
		}
		if u.Email != "" {
			d.byEmail[strings.ToLower(u.Email)] = u
		}
	}
	d.indexItems(items)

	// Unmapped refs are counted per ref and not per observation, so the roll-up is
	// stable however the batch was ordered.
	unmapped := map[string]int{}
	for _, obs := range msg.Observations {
		if ref, counted := d.decide(obs); counted {
			unmapped[ref]++
		}
	}
	for ref, n := range unmapped {
		d.plan.Unmapped = append(d.plan.Unmapped, unmappedRef{Ref: ref, Subjects: n})
	}
	// Most-held first: the ref eight hundred people hold is the one worth modelling,
	// and ties by name so two runs of the same input read identically.
	sort.SliceStable(d.plan.Unmapped, func(i, j int) bool {
		if d.plan.Unmapped[i].Subjects != d.plan.Unmapped[j].Subjects {
			return d.plan.Unmapped[i].Subjects > d.plan.Unmapped[j].Subjects
		}
		return d.plan.Unmapped[i].Ref < d.plan.Unmapped[j].Ref
	})
	return d.plan
}

// indexItems builds the ref → product index over every product, and records the
// refs more than one claims.
//
// A draft product is deliberately not indexed. A draft is not a decision yet — it
// cannot be published and nothing can be ordered against it — and attributing years
// of evidence to one would leave the evidence pointing at something that may never
// exist. A withdrawn product *is* indexed: withdrawal stops it being ordered and
// changes nothing about the people still holding it, which is the whole reason
// ADR-0312 has no deleted state.
func (d *inventoryDecider) indexItems(items []catalog.Item) {
	sorted := append([]catalog.Item(nil), items...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].ID < sorted[b].ID })
	for _, it := range sorted {
		if it.State == catalog.StateDraft {
			continue
		}
		for _, ref := range it.Targets {
			if !strings.EqualFold(strings.TrimSpace(ref.System), d.system) {
				continue
			}
			key := ref.Ref
			if other, taken := d.itemByRef[key]; taken {
				if len(d.ambiguous[key]) == 0 {
					d.ambiguous[key] = []string{other.ID}
				}
				d.ambiguous[key] = append(d.ambiguous[key], it.ID)
				continue
			}
			d.itemByRef[key] = it
		}
	}
}

// decide settles one observation. It returns the ref to count as unmapped, and
// whether to count it.
func (d *inventoryDecider) decide(obs rightObservation) (string, bool) {
	subject := strings.TrimSpace(obs.Subject)
	ref := strings.TrimSpace(obs.Ref)
	if subject == "" || ref == "" {
		d.note(inventoryDecision{
			Action: invMalformed, Subject: obs.Subject, Ref: obs.Ref,
			Why: "an observation needs both a subject and a right; one naming only half of " +
				"a holding is a fact about nothing that a reader would nonetheless count",
		})
		return "", false
	}

	// The right is resolved first, because an unmodelled right is the answer for
	// every subject holding it and resolving the person first would produce the same
	// sentence once per person.
	if others, clash := d.ambiguous[ref]; clash {
		d.note(inventoryDecision{
			Action: invAmbiguous, Subject: subject, Ref: ref,
			Why: "products " + strings.Join(others, ", ") + " all claim this reference, so the " +
				"right could be attributed to any of them and is attributed to none. Remove the " +
				"reference from all but one; publishing the catalogue refuses this within one " +
				"release, and across releases this is the only place it shows",
		})
		return "", false
	}
	item, known := d.itemByRef[ref]
	if !known {
		d.note(inventoryDecision{
			Action: invNoItem, Subject: subject, Ref: ref,
			Why: "no product claims this reference in system " + d.system + ". The right exists " +
				"and stays out of the inventory: this is a thing the estate grants that the " +
				"catalogue does not model, which is worth knowing and is not an error",
		})
		return ref, true
	}

	u, found := d.resolve(subject)
	if !found {
		d.note(inventoryDecision{
			Action: invNoSubject, Subject: subject, Ref: ref, ItemID: item.ID,
			Why: "no account matches this subject by directory id or mail. The person exists " +
				"in the target system and not here, so their rights cannot be attributed to " +
				"anybody — run the directory mirror first, or expect this list to be the " +
				"people it does not carry",
		})
		return "", false
	}

	// Already-held, in the two ways that differ.
	if existing, has := d.held(u.ID, item.ID); has {
		action, why := invHeld, "already recorded as legacy; left exactly as it is, because a "+
			"second load that moved its start date would make every right in the estate look "+
			"freshly acquired and destroy the one thing the date is for"
		if existing.Origin != model.OriginLegacy {
			action = invKeep
			why = "already recorded with origin " + existing.Origin.String() + ", which is better " +
				"knowledge than this load has: Atlas watched that one happen. Writing legacy over " +
				"it would replace the record and discard the order behind it"
		}
		d.note(inventoryDecision{
			Action: action, Subject: subject, Ref: ref,
			Principal: u.ID, ItemID: item.ID, Why: why,
		})
		return "", false
	}
	// The same person and product reached twice in one message — two groups mapping
	// to one product — is one holding, not two.
	if key := u.ID + "\x00" + item.ID; d.granting[key] {
		d.note(inventoryDecision{
			Action: invHeld, Subject: subject, Ref: ref, Principal: u.ID, ItemID: item.ID,
			Why: "this batch already grants " + item.ID + " to this person through another " +
				"reference; one product held once",
		})
		return "", false
	} else {
		d.granting[key] = true
	}

	dec := inventoryDecision{
		Action: invGrant, Subject: subject, Ref: ref, Principal: u.ID, ItemID: item.ID,
		Value: model.EntitlementValue{
			Principal: u.ID, ItemID: item.ID, VariantID: obs.VariantID,
			Since: d.since(obs), Origin: model.OriginLegacy,
			// OrderID stays empty, and that is the honest answer rather than an
			// omission: nothing here produced this right.
		},
	}
	d.plan.Grants = append(d.plan.Grants, dec)
	d.plan.Counts.Grant++
	return "", false
}

// since is when the hold began.
//
// Where the target system knows, that is used. Where it does not — which is most of
// them, since a directory records that a membership exists and rarely when it
// started — the load's own moment is recorded, and the alternative is worse than it
// looks. Zero would render as 1970: a date, in a date field, that nobody chose and
// that reads like data. The load's moment is at least a true statement about
// something — *this is when Atlas learned it* — and the origin beside it already
// says the knowledge is second-hand, so no reader who sees `legacy` will mistake
// the date for the day the person got their laptop.
func (d *inventoryDecider) since(obs rightObservation) int64 {
	if obs.Since > 0 {
		return obs.Since
	}
	return d.now
}

// resolve finds the account a subject names: by directory object id, then by mail.
//
// The order is not interchangeable. A directory id is what the mirror wrote and is
// unique by construction; mail is a human-facing attribute that changes when
// somebody marries and can be re-used when somebody leaves. Matching on it at all is
// a concession to the systems that report nothing else, and it is the second choice
// precisely because it is the one that can be wrong.
func (d *inventoryDecider) resolve(subject string) (User, bool) {
	if u, ok := d.byDirectory[subject]; ok {
		return u, true
	}
	if u, ok := d.byEmail[strings.ToLower(subject)]; ok {
		return u, true
	}
	return User{}, false
}

// note records a decision that writes nothing, and counts it.
func (d *inventoryDecider) note(dec inventoryDecision) {
	d.plan.Notes = append(d.plan.Notes, dec)
	switch dec.Action {
	case invHeld:
		d.plan.Counts.Held++
	case invKeep:
		d.plan.Counts.Keep++
	case invNoSubject:
		d.plan.Counts.NoSubject++
	case invNoItem:
		d.plan.Counts.NoItem++
	case invAmbiguous:
		d.plan.Counts.Ambiguous++
	case invMalformed:
		d.plan.Counts.Malformed++
	}
}

// inventoryTooManyObservations is the refusal for a batch above its budget, in the
// words an operator needs: what to change, not merely that something was too big.
func inventoryTooManyObservations(got, limit int) string {
	return fmt.Sprintf("this batch carries %d observations and the ceiling is %d (%s). "+
		"It is refused whole rather than truncated: a partial batch written as if it were "+
		"complete is exactly the silent gap in the evidence this load exists to prevent. "+
		"Read the target system in pages and report each page as its own message — the load "+
		"only ever adds, so pages need no ordering between them",
		got, limit, "ATLAS_LIMIT_INVENTORY_OBSERVATIONS")
}
