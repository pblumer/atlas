package order

import (
	"sort"

	"github.com/pblumer/atlas/api/catalog"
)

// Who may receive what (ADR-0347).
//
// The catalogue's audience is fail-closed and stays the outer gate. This is the
// inner one, and it exists because the outer gate cannot be narrowed: a person
// sees exactly **one** catalogue — the highest-ranked one their groups reach — so
// moving a product into a stricter catalogue does not restrict it, it hides it
// behind the shop that person already has. Before this, a product available to
// part of a catalogue's audience could not be expressed at all.
//
// # Why this is a refusal and not an approval rule
//
// An approval rule says *who decides*; this says *who may ask*. They look
// interchangeable and are not: leaving it to approval means a thousand people may
// request domain administration and one person must say no nine hundred and
// ninety-nine times. A line manager's attention is the scarcest resource the
// portal spends — the recertification record is built around that — and spending
// it to enforce a rule that could be stated once is the trade backwards.

// ineligible is one item the recipient may not receive.
type ineligible struct {
	// ItemID is the product that was refused.
	ItemID string
	// IncludedBy is the product whose composition dragged it in, empty when the
	// recipient asked for the refused item directly.
	//
	// It is the difference between a refusal somebody can act on and one that reads
	// as a bug. An integral part is never deselectable, so a person can be told
	// "you may not receive a licence" about a licence they never chose and cannot
	// remove — leaving them to conclude the portal is broken rather than that the
	// workplace they asked for contains something restricted.
	IncludedBy string
}

// reason renders the refusal for a person who has to do something about it.
func (i ineligible) reason() string {
	if i.IncludedBy == "" {
		return "the recipient is not eligible for " + i.ItemID +
			". This product is offered to part of the catalogue's audience only, and " +
			"eligibility is about who receives it rather than who orders it"
	}
	return "the recipient is not eligible for " + i.ItemID + ", which " + i.IncludedBy +
		" is made of and always carries. " + i.IncludedBy + " cannot be ordered " +
		"without it, so this is a refusal of " + i.IncludedBy + " for this recipient " +
		"rather than something to deselect"
}

// ineligibleIn reports the first item in this order the recipient may not
// receive, or nil.
//
// groups are the recipient's group ids. An item naming no eligible group is not
// restricted — the catalogue's audience already decided, and this only ever
// narrows it.
//
// Deterministic in what it reports: the items are walked in sorted order, so the
// same basket refused twice names the same product. A refusal that moved between
// two equally-refused items would make the second attempt look like a second,
// different problem.
func ineligibleIn(rel catalog.Release, ordered []string, groups []string) *ineligible {
	restricted := map[string][]string{}
	for _, it := range rel.Items {
		if len(it.Eligible) > 0 {
			restricted[it.ID] = it.Eligible
		}
	}
	if len(restricted) == 0 {
		return nil
	}

	has := make(map[string]bool, len(groups))
	for _, g := range groups {
		has[g] = true
	}

	sorted := append([]string(nil), ordered...)
	sort.Strings(sorted)
	for _, id := range sorted {
		want, ok := restricted[id]
		if !ok || eligibleFor(want, has) {
			continue
		}
		return &ineligible{ItemID: id, IncludedBy: wholeCarrying(rel, ordered, id)}
	}
	return nil
}

// eligibleFor reports whether somebody in these groups may receive an item that
// names those.
func eligibleFor(want []string, has map[string]bool) bool {
	for _, g := range want {
		if has[g] {
			return true
		}
	}
	return false
}

// wholeCarrying names a product in this order whose composition carries the item,
// and "" when nothing does — meaning it was asked for on its own.
//
// Only one level up, and deliberately: the answer is for a person reading a
// refusal, and "your workplace contains a licence you may not have" is what they
// need. The full chain would be a graph walk rendered as prose, which is a worse
// answer to the same question.
func wholeCarrying(rel catalog.Release, ordered []string, item string) string {
	inBasket := make(map[string]bool, len(ordered))
	for _, id := range ordered {
		inBasket[id] = true
	}
	var carriers []string
	for whole, parts := range rel.Includes {
		if !inBasket[whole] {
			continue
		}
		for _, part := range parts {
			if part == item {
				carriers = append(carriers, whole)
				break
			}
		}
	}
	if len(carriers) == 0 {
		return ""
	}
	// Sorted, so a part carried by two products in one basket names the same one
	// on every attempt.
	sort.Strings(carriers)
	return carriers[0]
}
