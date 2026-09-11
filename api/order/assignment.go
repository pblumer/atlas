package order

import (
	"errors"
	"fmt"
)

// Where a line's approval currently sits, and how a deadline may move it.
//
// An approval that nobody acts on is the quieter half of the problem a deadline
// on an incident solves: a stuck incident stands out in an operations view, an
// unattended approval task looks exactly like every other task in an inbox. The
// order waits on it with no failure to escalate and nothing to notice.
//
// So a deadline on an approval reminds, and then escalates to the approver's
// superior — the same directory lookup the "superior" approval rule already uses,
// so no second list has to be kept current for the day it is needed. What it
// must never do is decide. Silence is not a refusal, and recording one would put
// into a record kept forever a decision nobody made — the same thing [Abandon]
// exists to prevent on the incident path, and the rule is kept the same way:
// [Reject] takes the principal who decided, so a clock has nothing to pass and
// therefore no call to make. The only thing a deadline can do here is move the
// approval along.

// ErrNoFurtherEscalation means the chain has nowhere left to go: the current
// holder has no superior, or everyone above them has already had it. It is a
// distinct error because it is the one an operator's tooling must act on — that
// approval needs a person — where an invalid assignment is a defect.
var ErrNoFurtherEscalation = errors.New("order: the approval can escalate no further")

// Escalation is one hop of an approval from one holder to the next.
type Escalation struct {
	// From and To are principal ids — never names (ADR-draft-portal-personal-data).
	From string `json:"from"`
	To   string `json:"to"`
	At   int64  `json:"at"`
	// By is the principal who moved it, empty when a deadline did. The absence is
	// the record: it distinguishes a hop a clock made from somebody's decision to
	// intervene, and both are kept.
	By string `json:"by,omitempty"`
}

// Assignment is one line's approval: who holds it now, who it started with, and
// every hop between.
type Assignment struct {
	ItemID string `json:"itemId"`
	// Approver is the principal the approval is with now.
	Approver string `json:"approver"`
	// Original is who it was first assigned to. It survives every escalation,
	// because an approval that travelled must still say where it started — a
	// decision eventually taken by a third deputy reads very differently from one
	// taken by the line manager it was meant for.
	Original string `json:"original"`
	// AssignedAt is when it was first assigned, which is what a deadline measures
	// from.
	AssignedAt  int64        `json:"assignedAt"`
	Escalations []Escalation `json:"escalations,omitempty"`
	// StalledAt is when the chain was found to have nowhere left to go, zero while
	// it has somewhere. A stalled approval is still with whoever last held it —
	// stalling reports a fact, it does not take the task away — but it is now
	// visible, which is the whole point: an approval nobody can escalate and
	// nobody is looking at is how an order waits forever.
	StalledAt int64 `json:"stalledAt,omitempty"`
}

// Stalled reports whether this approval has been found to have nowhere left to
// escalate to.
func (a Assignment) Stalled() bool { return a.StalledAt != 0 }

// Assign puts a line's approval with somebody.
func Assign(itemID, approver string, at int64) Assignment {
	return Assignment{ItemID: itemID, Approver: approver, Original: approver, AssignedAt: at}
}

// Valid reports whether this assignment can be acted on.
func (a Assignment) Valid() error {
	if a.ItemID == "" {
		return fmt.Errorf("order: assignment names no line")
	}
	if a.Approver == "" {
		// An approval nobody holds is a task in nobody's inbox, which is the state
		// this whole mechanism exists to make impossible.
		return fmt.Errorf("order: assignment for %s has no approver", a.ItemID)
	}
	return nil
}

// held reports whether this principal has already held the approval.
func (a Assignment) held(who string) bool {
	if a.Approver == who || a.Original == who {
		return true
	}
	for _, e := range a.Escalations {
		if e.From == who || e.To == who {
			return true
		}
	}
	return false
}

// Escalate moves the approval to the current holder's superior, recording the
// hop. It returns the assignment unchanged when it refuses.
//
// superiorOf answers who a principal reports to, and "" means nobody does —
// the top of the hierarchy, or somebody the directory has no answer for. That case
// is an error rather than a silent no-op: an escalation that quietly left the
// approval where it was would hide that the deadline achieved nothing, which is
// the only thing anybody wanted to learn from it. Callers distinguish it with
// [ErrNoFurtherEscalation] and mark the assignment with [Stall].
func Escalate(a Assignment, superiorOf func(string) string, at int64) (Assignment, error) {
	if err := a.Valid(); err != nil {
		return a, err
	}
	if at == 0 {
		return a, fmt.Errorf("order: escalating an approval needs the moment it happened")
	}

	next := superiorOf(a.Approver)
	if next == "" {
		// The top of a hierarchy has nobody above it, so this is an ordinary end
		// rather than a misconfiguration.
		return a, fmt.Errorf("%w: %s has no superior (line %s)",
			ErrNoFurtherEscalation, a.Approver, a.ItemID)
	}
	if a.held(next) {
		// A directory can loop, and two colleagues recorded as each other's
		// superior is the common shape of it. Following it would hand the approval
		// back and forth until the order is forgotten, and somebody who already
		// held it will not act on it now — so operationally this is the same case
		// as the top of the hierarchy: there is nowhere new to go.
		return a, fmt.Errorf("%w: the approval for %s has already been with %s",
			ErrNoFurtherEscalation, a.ItemID, next)
	}

	a.Escalations = withHop(a.Escalations, Escalation{From: a.Approver, To: next, At: at})
	a.Approver = next
	return a, nil
}

// Stall records that an approval has nowhere left to escalate to, which is what
// makes it visible to somebody who can act. The caller raises whatever its
// operations surface uses for "a human must look at this"; this only marks it.
func Stall(a Assignment, at int64) (Assignment, error) {
	if err := a.Valid(); err != nil {
		return a, err
	}
	if at == 0 {
		return a, fmt.Errorf("order: stalling an approval needs the moment it happened")
	}
	a.StalledAt = at
	return a, nil
}

// Reassign puts a stuck approval with somebody a person chose, recording who
// intervened, and clears the stall so the deadline works normally again from the
// new holder.
//
// Unlike [Escalate] it may send the approval to somebody who already held it. The
// loop guard exists to stop a clock cycling an approval between two colleagues; a
// person sending it back to the original approver knows something the guard does
// not — that they are back from leave, say. What it will not do is reassign to the
// current holder, which would record a hop and move nothing.
func Reassign(a Assignment, to, by string, at int64) (Assignment, error) {
	if err := a.Valid(); err != nil {
		return a, err
	}
	if to == "" {
		return a, fmt.Errorf("order: reassigning the approval for %s needs somebody to give it to", a.ItemID)
	}
	if by == "" {
		return a, fmt.Errorf("order: reassigning an approval needs the principal who did it")
	}
	if at == 0 {
		return a, fmt.Errorf("order: reassigning an approval needs the moment it happened")
	}
	if to == a.Approver {
		return a, fmt.Errorf("order: the approval for %s is already with %s", a.ItemID, to)
	}

	a.Escalations = withHop(a.Escalations, Escalation{From: a.Approver, To: to, At: at, By: by})
	a.Approver = to
	a.StalledAt = 0
	return a, nil
}

// withHop appends to a copy, so a caller holding the previous assignment keeps its
// own history rather than sharing the array under it.
func withHop(hops []Escalation, h Escalation) []Escalation {
	out := make([]Escalation, len(hops), len(hops)+1)
	copy(out, hops)
	return append(out, h)
}
