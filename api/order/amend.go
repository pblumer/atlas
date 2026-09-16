package order

import (
	"fmt"
	"sort"
)

// Changing a position after it was ordered, and the two things that means
// (ADR-0359).
//
// The story asks to "modify or delete positions directly". Deleting existed only
// for a whole order — [CancelOrder] takes back everything that has not happened —
// so somebody who no longer wanted the second screen had to take back the laptop
// with it. Modifying did not exist at all.
//
// "Modify" is two different acts, and treating them as one is how a record starts
// lying:
//
//   - Changing **what is held** — another product, another variant — is a
//     different claim about the past. It is deliberately not offered. A line that
//     was provisioned and then quietly became a different product leaves the access
//     record unable to answer what somebody had and when, which is the one question
//     it exists for. The path already exists: give it back, order the other thing,
//     and the record carries both.
//   - Correcting **what was recorded about it** — the configuration answers a
//     product asked for (ADR-0358): a mistyped cost
//     centre, the wrong site. That is the same right with corrected details. A cost
//     centre is not access, and an access review does not ask about it.
//
// Only the second is here, and what it may do depends on where the line stands —
// which the status machine already decides, so this asks it rather than inventing
// a second rule beside it.

// AmendedAnswers is one correction to a line's configuration answers, kept beside
// them rather than instead of them.
//
// It exists for the case an overwrite cannot make honest: a line the recipient
// already **holds**. The laptop is at the wrong site and correcting the record does
// not move it, so an overwrite would leave the order saying something that was
// never true of the delivery. Recording the correction *as* a correction is more
// true than either version alone — it says what was ordered, what it should have
// been, who said so and when.
type AmendedAnswers struct {
	// Was is what the answers said before this correction.
	Was map[string]string `json:"was"`
	// By is the principal who corrected them and At is when. Both are required,
	// for the reason every settled transition in this package requires them: a
	// change kept forever that says somebody corrected something, without saying
	// who, is a correction nobody made.
	By string `json:"by"`
	At int64  `json:"at"`
	// Reason is optional and usually short — "cost centre was wrong". It is what a
	// reader months later has instead of a phone call.
	Reason string `json:"reason,omitempty"`
}

// AmendAnswers replaces a line's configuration answers, and returns the line
// unchanged when it refuses.
func AmendAnswers(l Line, answers map[string]string, by string, at int64, reason string) (Line, error) {
	switch {
	case l.ConfigForm == "":
		return l, fmt.Errorf("order: %s asks for no details, so there are none to correct", l.ItemID)
	case by == "":
		return l, fmt.Errorf("order: correcting the details of %s needs the principal who did it", l.ItemID)
	case at == 0:
		return l, fmt.Errorf("order: correcting the details of %s needs the moment it happened", l.ItemID)
	}

	switch {
	case l.Status.Cancellable():
		// Nothing has been attempted, so the order is still only a request — and
		// correcting a request is correcting a request. No amendment is recorded,
		// because there is no delivery the old answers were ever true of.
		l.Config = copyAnswers(answers)
		return l, nil

	case l.Status.Held():
		// The thing exists in the world with the old details, so the correction is
		// recorded as one and the old answers are kept.
		l.Amendments = append(l.Amendments, AmendedAnswers{
			Was: copyAnswers(l.Config), By: by, At: at, Reason: reason,
		})
		l.Config = copyAnswers(answers)
		return l, nil

	case l.Status == StatusRunning:
		// A provisioning process has this line now, which is a conversation with a
		// system this server does not control. Changing the answers underneath it
		// would leave the record saying one thing and the target system having been
		// told another, with nothing anywhere saying which the delivery followed.
		return l, fmt.Errorf("order: %s is being provisioned now, so its details cannot "+
			"be changed underneath the process acting on them — wait for it to finish, "+
			"then correct them", l.ItemID)

	default:
		// Rejected, cancelled, abandoned: closed records of requests that produced
		// nothing. Correcting the cost centre of a laptop nobody ever received
		// would be editing a closed record for no reader's benefit.
		return l, fmt.Errorf("order: %s is %s and nothing was delivered under these "+
			"details, so there is nothing to correct", l.ItemID, l.Status)
	}
}

// CancelLine withdraws one position rather than the whole order.
//
// [CancelOrder] exists because somebody cancelling *an order* is not making a
// series of per-line decisions. Removing one position is the opposite act: exactly
// one decision, about one thing.
//
// Two things it does that a handler doing this by hand would forget.
//
// An **integral** line is refused. The basket will not let anybody deselect a part
// its whole always carries — a workplace is not a workplace without its account —
// and a rule enforced when ordering and not afterwards is not a rule. It names
// what carries the line rather than only refusing, because the answer somebody
// needs is "take back the workplace instead".
//
// And the schedule is recomputed. A withdrawn line is a root cause like a refused
// one, and anything waiting on it is waiting for nothing; skipping that pass
// leaves a line blocked forever behind something that will never arrive.
func CancelLine(o Order, itemID, by string, at int64, reason string) (Order, error) {
	idx := -1
	for i := range o.Lines {
		if o.Lines[i].ItemID == itemID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return o, fmt.Errorf("order %s carries no line for %s", o.ID, itemID)
	}
	if o.Lines[idx].Integral {
		if whole := carriers(o, itemID); len(whole) > 0 {
			return o, fmt.Errorf("order: %s is part of %s and is not ordered on its own, "+
				"so it cannot be taken back on its own — withdraw what carries it",
				itemID, join(whole))
		}
		return o, fmt.Errorf("order: %s was not ordered on its own and cannot be taken "+
			"back on its own", itemID)
	}
	next, err := Cancel(o.Lines[idx], by, at, reason)
	if err != nil {
		return o, err
	}
	lines := make([]Line, len(o.Lines))
	copy(lines, o.Lines)
	lines[idx] = next
	o.Lines = Propagate(lines, o.Requires)
	o.UpdatedAt = at
	return o, nil
}

// carriers names the lines in this order that carry itemID as an integral part.
// Sorted, so the same order refuses with the same sentence every time.
func carriers(o Order, itemID string) []string {
	var out []string
	for _, l := range o.Lines {
		for _, part := range l.Includes {
			if part == itemID {
				out = append(out, l.ItemID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// join renders a short list for a sentence a person reads.
func join(ids []string) string {
	switch len(ids) {
	case 1:
		return ids[0]
	case 2:
		return ids[0] + " and " + ids[1]
	default:
		return ids[0] + ", " + join(ids[1:])
	}
}
