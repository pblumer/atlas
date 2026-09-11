package order

import "sort"

// Propagating an outcome through an order.
//
// The fulfilment process works a release's waves, but a wave boundary knows only
// that the previous wave is done — not which lines a failure took with it. So
// after every line settles it asks here: given what each line is now, and what
// each line requires, which lines can no longer be attempted?
//
// The answer has to distinguish three things that all mean "not provisioned", and
// keeping them apart is the whole point of the status vocabulary. A failure is
// repairable and belongs in an incident queue. A rejection is a decision and
// belongs in neither. A blocked line was never attempted and has nothing of its
// own wrong with it — what it needs is the name of the thing that actually
// stopped it.

// Propagate returns the lines with every line that can no longer be attempted
// marked blocked, naming what stopped it. It does not modify its argument: a
// caller that retried against rewritten lines would resolve against its own
// previous conclusions rather than against the facts.
//
// It is idempotent, so the fulfilment process may call it after every settled
// line without tracking whether it already has.
func Propagate(in []Line, requires map[string][]string) []Line {
	out := make([]Line, len(in))
	copy(out, in)
	if len(requires) == 0 {
		return out
	}

	status := make(map[string]LineStatus, len(out))
	for _, l := range out {
		status[l.ItemID] = l.Status
	}

	// A line's root causes are the failed or rejected lines it transitively
	// depends on. Resolved depth-first with memoisation: the release proved the
	// precedence graph acyclic, so the recursion terminates, and an order can
	// carry a line several times over as a precondition of several others.
	memo := map[string][]string{}
	var causes func(id string) []string
	causes = func(id string) []string {
		if got, done := memo[id]; done {
			return got
		}
		memo[id] = nil // guard against re-entry while this id is being resolved

		found := map[string]bool{}
		for _, need := range requires[id] {
			switch s := status[need]; {
			case s == StatusFailed || s == StatusRejected:
				// A direct cause. It is the root: it has a fault or a decision of
				// its own, not an inherited one.
				found[need] = true
			case s.Satisfied(), s == "":
				// Met, or not part of this order at all. Either way it stops
				// nothing: an order that does not carry a precondition is an order
				// whose recipient was not asked to obtain it here.
			default:
				// Pending, running or itself blocked: inherit whatever stopped it,
				// so the reader is given the fault rather than the chain.
				for _, c := range causes(need) {
					found[c] = true
				}
			}
		}

		list := make([]string, 0, len(found))
		for c := range found {
			list = append(list, c)
		}
		sort.Strings(list)
		memo[id] = list
		return list
	}

	for i := range out {
		// A line that has already settled keeps what it is. One provisioned before
		// something upstream failed stays done: provisioning happened, and the
		// record has to say so.
		if out[i].Status.Settled() {
			continue
		}
		if blocking := causes(out[i].ItemID); len(blocking) > 0 {
			out[i].Status = StatusBlocked
			out[i].BlockedBy = blocking
		}
	}
	return out
}

// Derive reports where a whole order stands, from its lines alone. It is computed
// rather than stored so it can never disagree with them.
func Derive(ls []Line) Status {
	provisioned, settled := 0, 0
	for _, l := range ls {
		if !l.Status.Settled() {
			return OrderRunning
		}
		settled++
		if l.Status.Satisfied() {
			provisioned++
		}
	}
	switch {
	case provisioned == settled:
		// Also the empty order, which is complete rather than unfulfilled: nothing
		// was asked for and nothing is outstanding.
		return OrderCompleted
	case provisioned == 0:
		return OrderUnfulfilled
	default:
		return OrderPartial
	}
}
