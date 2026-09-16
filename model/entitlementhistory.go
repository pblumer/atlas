package model

import (
	"encoding/binary"
	"strings"
)

// What somebody used to hold.
//
// The inventory (entitlement.go) is present tense by construction: a grant writes
// a row, a revocation deletes it. That is the right shape for "what does this
// person have", and it means the estate can answer that question and no other.
//
// # Why the present tense is not enough
//
// The order behind a right is deleted by retention long before the right ends —
// [Grant] says so in as many words, and the inventory exists as engine state
// precisely because of it. Put the two facts together and a third follows that
// nothing in the portal had a place for: when the right ends too, *everything*
// goes. The row is deleted, the order is already gone, and the estate can no
// longer answer whether the person ever held the thing, under whose approval, or
// for how long.
//
// That is the question an access review is for, and it is the one question the
// portal could not answer. It is sharpest where the portal has just become good
// at finding problems: "this person held create-supplier and approve-payment
// together for six months" is a finding that ceases to exist the moment somebody
// acts on it. **The remedy destroyed the evidence of the problem**, and an estate
// that only remembers the mistakes nobody fixed has the record exactly backwards.
//
// # Why a row rather than a scan of the log
//
// Every fact here is on the log already, and in principle a reader could fold it
// forward. In practice the log is exported and truncated, and a record that
// survives only until the next export is not a record. This column family is the
// one structure in the portal whose whole purpose is to outlive retention.

// HoldEnd says why a hold stopped being recorded — and, because the two reasons
// differ in what they assert, what the row containing it means.
//
// This is not a label on an otherwise uniform row. A returned hold is evidence
// that the person *had* the access until that moment. A corrected hold is
// evidence that Atlas *claimed* they did, and reconciliation found the target
// system disagreed. Writing the second as though it were the first would put in a
// record kept for years an assertion that somebody had access nobody can show
// they had — which is the direction of wrongness ADR-0334 calls the one that
// corrupts the evidence.
type HoldEnd uint8

const (
	// EndReturned is a right given back: the order line was returned, the
	// deprovisioning process ran, the access went away. The ordinary ending, and
	// the only one that is evidence of a held period.
	EndReturned HoldEnd = 0
	// EndCorrected is a right reconciliation found the target system did not have,
	// and somebody accepted the correction.
	//
	// The access did not end here. As far as anybody can tell it was never there,
	// or went away at a moment nobody observed. What ended is Atlas's claim, and
	// the row says only that.
	EndCorrected HoldEnd = 1
)

func (e HoldEnd) String() string {
	if e == EndCorrected {
		return "corrected"
	}
	return "returned"
}

// Held reports whether a row that ended this way is evidence the access existed
// for the period it names.
//
// The distinction every reader of this family has to make, given a name so that
// no reader has to rediscover it. A report that answers "what did this person
// hold in March" must show a corrected row differently from a returned one, or it
// asserts on Atlas's behalf something Atlas explicitly declined to decide.
func (e HoldEnd) Held() bool { return e == EndReturned }

// EntitlementHistoryValue is one hold that has ended.
//
// It copies the hold rather than referring to it, because there is nothing left
// to refer to: that is the whole point. The entitlement row is deleted in the
// same transaction that writes this one, and the order was very likely deleted
// years earlier.
type EntitlementHistoryValue struct {
	// Principal, ItemID, VariantID and OrderID are copied from the hold as it
	// stood. Principal ids and nothing else about a person, exactly as the
	// inventory holds them (ADR-0314): an append-only
	// family cannot forget what it was given in the clear, and this one is meant
	// to be read years later.
	Principal string
	ItemID    string
	VariantID string
	// OrderID names the order behind the hold. It is kept knowing the order itself
	// is probably gone — an id that resolves to nothing still says the hold came
	// through an order and names which, and an auditor with an exported log can
	// find it there.
	OrderID string
	// Since is when the hold began and EndedAt when it stopped being recorded,
	// both in unix nanoseconds. Since is copied from the hold; EndedAt is read at
	// command time and frozen into the event, so replay reproduces it rather than
	// re-reading a clock (invariants I4/I6).
	Since   int64
	EndedAt int64
	// Until is the end the hold was granted with, or zero. Kept beside EndedAt
	// rather than collapsed into it because the pair is the finding: a hold whose
	// EndedAt is far past its Until was overdue for that long, and after the row
	// is written that is the only place the fact survives.
	Until int64
	// Origin is where the knowledge came from, copied from the hold.
	Origin      EntitlementOrigin
	EndedReason HoldEnd
	// EndedBy is who decided, as a principal id, or empty where nothing recorded
	// it. A return driven by a modelled process names the principal the process
	// authenticated as, which is the honest answer — a process is who its token
	// says it is.
	EndedBy string
}

// Overdue reports how long past its promised end the hold was still recorded, in
// nanoseconds, and zero for one that had no end or ended on time.
func (v *EntitlementHistoryValue) Overdue() int64 {
	if v.Until == 0 || v.EndedAt <= v.Until {
		return 0
	}
	return v.EndedAt - v.Until
}

// CoveredAt reports whether this row says the principal was recorded as holding
// the item at that moment.
//
// Recorded, not "had": what a corrected row covers is a period Atlas claimed, and
// [HoldEnd.Held] is what separates the two. The half-open interval is deliberate
// — a hold that ended at T did not cover T, so a right returned and re-granted in
// the same nanosecond is not counted twice.
func (v *EntitlementHistoryValue) CoveredAt(at int64) bool {
	return v.Since <= at && at < v.EndedAt
}

func (*EntitlementHistoryValue) ValueType() ValueType { return VTEntitlementHistory }

func (v *EntitlementHistoryValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Since))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.EndedAt))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Until))
	dst = append(dst, byte(v.Origin), byte(v.EndedReason))
	dst = appendString(dst, v.Principal)
	dst = appendString(dst, v.ItemID)
	dst = appendString(dst, v.VariantID)
	dst = appendString(dst, v.OrderID)
	return appendString(dst, v.EndedBy)
}

func (v *EntitlementHistoryValue) decode(src []byte) error {
	const historyFixed = 8 + 8 + 8 + 1 + 1
	if len(src) < historyFixed {
		return ErrShortBuffer
	}
	v.Since = int64(binary.LittleEndian.Uint64(src[0:]))
	v.EndedAt = int64(binary.LittleEndian.Uint64(src[8:]))
	v.Until = int64(binary.LittleEndian.Uint64(src[16:]))
	v.Origin = EntitlementOrigin(src[24])
	v.EndedReason = HoldEnd(src[25])

	rest := src[historyFixed:]
	// The two that make the row mean anything are required, as in the inventory: a
	// truncated row decoding to "nobody held nothing" would be counted by every
	// reader and subtracted by none.
	for _, into := range []*string{&v.Principal, &v.ItemID} {
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	// The rest end early rather than erroring, so a field can be appended later
	// without migrating a family that is by design never rewritten.
	for _, into := range []*string{&v.VariantID, &v.OrderID, &v.EndedBy} {
		if len(rest) == 0 {
			return nil
		}
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	return nil
}

// Valid reports whether this row names both halves of "who held what". A row
// missing either is a fact about nothing.
func (v *EntitlementHistoryValue) Valid() bool {
	return strings.TrimSpace(v.Principal) != "" && strings.TrimSpace(v.ItemID) != ""
}
