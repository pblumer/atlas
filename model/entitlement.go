package model

import (
	"encoding/binary"
	"strings"
)

// What somebody holds.
//
// The third of the portal's three models
// (ADR-0312), and the one that outlives the
// other two. A catalogue entry says what may be ordered; an order line says what
// was asked for, and its instance is eligible for retention deletion in ninety
// days. An entitlement says what is *true now*, and stays true for years.
//
// That difference in lifetime is the whole reason it is engine state with a column
// family of its own rather than a fact recoverable from the order that produced
// it: the order will be gone, and the access will not.

// EntitlementOrigin says where the knowledge that somebody holds something came
// from. It is the difference between evidence and an assumption, and it costs one
// byte now against a migration of an append-only column family later.
type EntitlementOrigin uint8

const (
	// OriginOrdered is the ordinary one: an order was placed, approved and
	// provisioned, and Atlas watched it happen.
	OriginOrdered EntitlementOrigin = 0
	// OriginAdopted is a privilege reconciliation found in a target system and
	// somebody accepted into the inventory. Atlas did not grant it and says so.
	OriginAdopted EntitlementOrigin = 1
	// OriginLegacy is what the estate already held when the portal was
	// commissioned.
	//
	// On the first day the inventory is empty and reality is full. Without this,
	// the first reconciliation reports every existing privilege as a discrepancy,
	// and each of those carries an executable "remove in the target system" — which
	// is how a new access-governance tool locks out a company on its first run.
	OriginLegacy EntitlementOrigin = 2
)

func (o EntitlementOrigin) String() string {
	switch o {
	case OriginAdopted:
		return "adopted"
	case OriginLegacy:
		return "legacy"
	default:
		return "ordered"
	}
}

// EntitlementValue is one thing one principal holds.
type EntitlementValue struct {
	// Principal is who holds it, as a principal id and nothing else. Never a name,
	// an address or a department: those resolve from the account when a screen is
	// rendered (ADR-0314), and an append-only log cannot
	// forget what it was given in the clear.
	Principal string
	// ItemID is the catalogue item, as the release that granted it named it. A
	// product renamed since resolves through the item, not through the text.
	ItemID string
	// VariantID is the variant held, empty where the item has none.
	VariantID string
	// OrderID names the order that produced it. Empty for an adopted or legacy
	// entitlement, which is the honest answer: nothing here produced it.
	OrderID string
	// Since is when the hold began, in unix nanoseconds. Read at command time and
	// frozen into the event, so replay reproduces it rather than re-reading a clock
	// (invariants I4/I6).
	Since  int64
	Origin EntitlementOrigin

	// Until is when this right was meant to end, in unix nanoseconds, or zero for
	// one with no end. Set only for a right the portal granted, from the ceiling
	// the release's product declared (ADR-0344).
	//
	// **It is a promise about when access should end, not a statement that it
	// has.** An entitlement past its Until is still held: the target system still
	// has the membership, and nothing has run. Removing the record when a clock
	// passes would make Atlas assert that somebody does not have access they
	// demonstrably do — which is the direction of wrongness ADR-0334 calls the one
	// that corrupts the evidence. Decaying into it is bad; manufacturing it on a
	// timer would be worse.
	//
	// Never set for an adopted or legacy right, whatever its product declares. A
	// commissioning load records Since as when the right was *found*, so a ceiling
	// measured from it would schedule the expiry of a whole estate on the
	// anniversary of the day somebody switched the portal on.
	Until int64
}

// Expired reports whether this right's end has passed. A right with no end never
// expires, which is every right an installation holds until a product declares a
// ceiling.
func (v *EntitlementValue) Expired(now int64) bool {
	return v.Until != 0 && now >= v.Until
}

func (*EntitlementValue) ValueType() ValueType { return VTEntitlement }

func (v *EntitlementValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Since))
	dst = append(dst, byte(v.Origin))
	dst = appendString(dst, v.Principal)
	dst = appendString(dst, v.ItemID)
	dst = appendString(dst, v.VariantID)
	dst = appendString(dst, v.OrderID)
	// Appended after the strings rather than beside Since, because that is what
	// keeps every record written before this field readable: a decoder that reached
	// the end has a right with no end, which is exactly what those records mean.
	return binary.LittleEndian.AppendUint64(dst, uint64(v.Until))
}

func (v *EntitlementValue) decode(src []byte) error {
	const entitlementFixed = 8 + 1
	if len(src) < entitlementFixed {
		return ErrShortBuffer
	}
	v.Since = int64(binary.LittleEndian.Uint64(src[0:]))
	v.Origin = EntitlementOrigin(src[8])

	rest := src[entitlementFixed:]
	// The two that make the record mean anything are required: a truncated record
	// that decoded to "nobody holds nothing" would be counted by every reader.
	for _, into := range []*string{&v.Principal, &v.ItemID} {
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	// The rest are append-compatible, as the other values in this package are: a
	// record written before a field simply ends, and the field decodes empty.
	//
	// This matters more here than elsewhere. The record this family implements
	// warns in as many words that a field added later costs a migration of an
	// append-only column family, and it names the fields it expects to want —
	// which target system a right lives in, what state it is in there — that
	// reconciliation will need and that nothing yet constrains well enough to
	// define. Ending early rather than erroring is what lets them be added without
	// one.
	for _, into := range []*string{&v.VariantID, &v.OrderID} {
		if len(rest) == 0 {
			return nil
		}
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	// And the first non-string field to arrive late. Absent means no end, which is
	// what every record written before ceilings existed meant and still means.
	if len(rest) >= 8 {
		v.Until = int64(binary.LittleEndian.Uint64(rest))
	}
	return nil
}

// Valid reports whether this entitlement can be written down. Both halves of "who
// holds what" are required: an entitlement naming nobody is a fact about nothing,
// and one naming no item is a person holding the unspecified.
func (v *EntitlementValue) Valid() bool {
	return strings.TrimSpace(v.Principal) != "" && strings.TrimSpace(v.ItemID) != ""
}
