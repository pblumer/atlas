package order

import "fmt"

// Giving up on a line, and the rule that keeps a clock from doing it.
//
// A deadline sits on the incident behind a failure, and what it does there is
// *escalate*: it makes the incident visible and tells somebody. It never abandons
// anything. A system that closed orders because nobody was in the incident queue
// over the holidays would tell an orderer their line is never coming, for a reason
// that was actually short staffing — and would write "the system decided" into a
// record kept forever.
//
// So abandoning is a decision with an author, and this is the only way to reach
// the status. The rule is structural rather than a review note: there is nothing
// to pass as the principal when no person is making the call, so an automated
// caller has no call to make.

// Abandon marks a failed line as one nobody will repair, recording who decided and
// when. It returns the line unchanged when it refuses.
//
// Only a failure can be abandoned: there is nothing to give up on in a line that
// never failed, and rewriting a provisioned one would deny work that happened. A
// rejection is already settled by somebody's decision, and a blocked line is a
// consequence — give up on its cause instead, and this line follows.
func Abandon(l Line, by string, at int64) (Line, error) {
	if l.Status != StatusFailed {
		return l, fmt.Errorf("order: cannot abandon a %s line, only a failed one", l.Status)
	}
	if by == "" {
		return l, fmt.Errorf("order: abandoning a line needs the principal who decided it")
	}
	if at == 0 {
		return l, fmt.Errorf("order: abandoning a line needs the moment it was decided")
	}
	l.Status = StatusAbandoned
	l.AbandonedBy, l.AbandonedAt = by, at
	return l, nil
}

// Valid holds the same rules where a line arrives as JSON rather than through
// [Abandon] — from a store, an API payload, a restored backup. A serialised line
// never went through that function, so the invariant is checked again at the
// boundary it enters through rather than assumed from the one place that upholds
// it.
func (l Line) Valid() error {
	if l.ItemID == "" {
		return fmt.Errorf("order: line names no catalogue item")
	}
	if l.Status == StatusAbandoned {
		if l.AbandonedBy == "" {
			return fmt.Errorf("order: line %s is abandoned without naming who decided it", l.ItemID)
		}
		if l.AbandonedAt == 0 {
			return fmt.Errorf("order: line %s is abandoned without a moment", l.ItemID)
		}
		return nil
	}
	if l.AbandonedBy != "" || l.AbandonedAt != 0 {
		// A line nobody abandoned that names somebody is a contradiction, and the
		// dangerous reading is the flattering one: that a decision was recorded.
		return fmt.Errorf("order: line %s is %s but carries an abandonment", l.ItemID, l.Status)
	}
	return nil
}
