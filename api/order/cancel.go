package order

import "fmt"

// Taking an order back.
//
// The likeliest support call a self-service portal receives is somebody who
// ordered the wrong thing a minute ago, and until now the only answer was to
// telephone the approver and ask them to refuse it — which files a decision
// nobody made, in a record kept for years, in the place a reader would go to find
// out whether that colleague's laptop was turned down.
//
// So a cancellation is its own transition with its own author. What it can take
// back is exactly what has not happened yet: [LineStatus.Cancellable] says which,
// and everything else keeps the outcome it already has. It is deliberately not a
// way to undo a provisioned line — revoking what was granted is deprovisioning,
// it runs the process the order froze for that purpose, and it is not this.

// Cancel withdraws one line. It returns the line unchanged when it refuses.
//
// by is the principal withdrawing it and is required, for the reason every other
// settled-without-provisioning transition here requires one: a status kept forever
// that says somebody decided, without saying who, is a decision nobody made. A
// reason is optional — a cancellation is explained to the person who made it.
func Cancel(l Line, by string, at int64, reason string) (Line, error) {
	if !l.Status.Cancellable() {
		return l, fmt.Errorf("order: line %s is %s and cannot be withdrawn", l.ItemID, l.Status)
	}
	if by == "" {
		return l, fmt.Errorf("order: withdrawing line %s needs the principal who did it", l.ItemID)
	}
	if at == 0 {
		return l, fmt.Errorf("order: withdrawing line %s needs the moment it happened", l.ItemID)
	}
	l.Status = StatusCancelled
	l.DecidedBy, l.DecidedAt, l.Reason = by, at, reason
	// A blocked line carries what stopped it. Once withdrawn it was not stopped by
	// anything — it was taken back — and leaving the causes on it would have the
	// portal tell somebody their cancelled line is waiting for a laptop.
	l.BlockedBy, l.TerminallyBlocked = nil, false
	return l, nil
}

// CancelOrder withdraws every line of an order that still can be, and reports
// which ones it could not touch.
//
// It is one call rather than a line at a time because that is what somebody
// means: a person cancelling an order is not making a series of per-line
// decisions. Lines it leaves alone it names, because "your order is cancelled"
// when a laptop is already on its way is the sentence that produces the second
// support call.
func CancelOrder(o Order, by string, at int64, reason string) (Order, []string, []string, error) {
	lines := make([]Line, len(o.Lines))
	copy(lines, o.Lines)

	var cancelled, kept []string
	for i := range lines {
		if !lines[i].Status.Cancellable() {
			kept = append(kept, lines[i].ItemID)
			continue
		}
		next, err := Cancel(lines[i], by, at, reason)
		if err != nil {
			return o, nil, nil, err
		}
		lines[i] = next
		cancelled = append(cancelled, lines[i].ItemID)
	}
	if len(cancelled) == 0 {
		return o, nil, kept, fmt.Errorf("order: nothing in %s can still be withdrawn", o.ID)
	}

	// Recomputed, because a withdrawn line is a root cause like a refused one:
	// anything waiting on it is waiting for nothing, and the pass is what says so.
	o.Lines = Propagate(lines, o.Requires)
	o.UpdatedAt = at
	return o, cancelled, kept, nil
}
