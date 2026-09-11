package order

import "fmt"

// Where a line's approval currently sits, and how a deadline may move it.
//
// An approval that nobody acts on is the quieter half of the problem a deadline
// on an incident solves: a stuck incident stands out in an operations view, an
// unattended approval task looks exactly like every other task in an inbox. The
// order waits on it with no failure to escalate and nothing to notice.
//
// So a deadline on an approval reminds, and then escalates to a deputy. What it
// must never do is decide. Silence is not a refusal, and recording one would put
// into a record kept forever a decision nobody made — the same thing [Abandon]
// exists to prevent on the incident path, and the rule is kept the same way:
// [Reject] takes the principal who decided, so a clock has nothing to pass and
// therefore no call to make. The only thing a deadline can do here is move the
// approval along.

// Escalation is one hop of an approval from one holder to the next.
type Escalation struct {
	// From and To are principal ids — never names (ADR-draft-portal-personal-data).
	From string `json:"from"`
	To   string `json:"to"`
	At   int64  `json:"at"`
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
}

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

// Escalate moves the approval to the current holder's deputy, recording the hop.
// It returns the assignment unchanged when it refuses.
//
// deputyOf answers who stands in for a principal, and "" means nobody does. That
// case is an error rather than a silent no-op: an escalation that quietly left the
// approval where it was would hide that the deadline achieved nothing, which is
// the only thing anybody wanted to learn from it.
func Escalate(a Assignment, deputyOf func(string) string, at int64) (Assignment, error) {
	if err := a.Valid(); err != nil {
		return a, err
	}
	if at == 0 {
		return a, fmt.Errorf("order: escalating an approval needs the moment it happened")
	}

	deputy := deputyOf(a.Approver)
	if deputy == "" {
		return a, fmt.Errorf("order: %s has no deputy, so the approval for %s cannot escalate",
			a.Approver, a.ItemID)
	}
	if a.held(deputy) {
		// Two colleagues deputising for each other is an ordinary arrangement, and
		// following it would hand the approval back and forth until the order is
		// forgotten. Somebody who already held it will not act on it now.
		return a, fmt.Errorf("order: the approval for %s has already been with %s",
			a.ItemID, deputy)
	}

	hops := make([]Escalation, len(a.Escalations), len(a.Escalations)+1)
	copy(hops, a.Escalations)
	a.Escalations = append(hops, Escalation{From: a.Approver, To: deputy, At: at})
	a.Approver = deputy
	return a, nil
}
