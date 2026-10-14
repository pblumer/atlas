package order

import (
	"fmt"
	"sort"
)

// Driving an order forward.
//
// Waves say what may run together, and they are what a person reads in a release.
// What may start *now* is a finer question, and waves cannot answer it once one
// has failed in part: a wave boundary knows only that the previous round is over,
// not which of its lines arrived. So the orchestrator asks per line — has
// everything this needs been provisioned — which is the same edge set the
// blocking rule reads, taken the other way round.
//
// Nothing here starts anything or talks to a target system. It says what is
// ready and records what came back; provisioning is a process, and this is the
// bookkeeping around it.

// Next reports the lines that may be started now, sorted: those still waiting
// whose preconditions are all satisfied.
//
// A line already running or finished is not offered again, which is what lets an
// orchestrator ask after every single result rather than tracking rounds itself.
func Next(o Order) []string {
	// Preconditions name *products*, because that is what the release knows: an
	// edge runs between two catalogue items and has nothing to say about how many
	// positions of one were ordered. So a product is satisfied when every position
	// of it is. A map holding one status per product would keep whichever line came
	// last and let a dependant start while the other phone was still pending.
	satisfied := make(map[string]bool, len(o.Lines))
	carried := make(map[string]bool, len(o.Lines))
	for _, l := range o.Lines {
		if !carried[l.ItemID] {
			carried[l.ItemID] = true
			satisfied[l.ItemID] = true
		}
		if !l.Status.Satisfied() {
			satisfied[l.ItemID] = false
		}
	}

	var ready []string
	for _, l := range o.Lines {
		if l.Status != StatusPending {
			continue
		}
		blocked := false
		for _, need := range o.Requires[l.ItemID] {
			// A precondition this order does not carry is not waited for: whether
			// the recipient already holds it is the provisioning process's question.
			if carried[need] && !satisfied[need] {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, l.Key())
		}
	}
	sort.Strings(ready)
	return ready
}

// Apply records one line's outcome, propagates what that outcome stopped, and
// returns the updated order. It returns the order unchanged when it refuses.
//
// Only the two outcomes a provisioning attempt can produce may be set here. A
// rejection and an abandonment are decisions with an author, so they go through
// [Reject] and [Abandon]; blocked is derived and never set directly. Accepting
// them here would be a second way to reach a status whose whole point is that it
// has exactly one.
func Apply(o Order, ref string, status LineStatus, at int64) (Order, error) {
	switch status {
	case StatusDone, StatusSkipped, StatusFailed, StatusRunning, StatusReturned, StatusReturnFailed:
	default:
		return o, fmt.Errorf("order: a line cannot be set to %s here — "+
			"rejection, abandonment and cancellation are decisions with an author, "+
			"blocked is derived, and returning is entered by asking for the return", status)
	}
	if at == 0 {
		return o, fmt.Errorf("order: recording a line's outcome needs the moment it happened")
	}

	key, err := ResolveLine(o, ref)
	if err != nil {
		return o, err
	}
	found := false
	lines := make([]Line, len(o.Lines))
	copy(lines, o.Lines)
	for i := range lines {
		if lines[i].Key() != key {
			continue
		}
		// A return is reported only by the revocation that was asked for. Without
		// this, a provisioning worker reporting "returned" would take a line
		// somebody holds and record it as given back, with nothing having run.
		if (status == StatusReturned || status == StatusReturnFailed) && lines[i].Status != StatusReturning {
			return o, fmt.Errorf("order: line %s is %s, so nothing is giving it back",
				key, lines[i].Status)
		}
		lines[i].Status = status
		found = true
		break
	}
	if !found {
		return o, fmt.Errorf("order %s carries no line for %s", o.ID, key)
	}

	o.Lines = Propagate(lines, o.Requires)
	o.UpdatedAt = at
	return o, nil
}

// Ready is [Next] with what an orchestrator needs to act: each line that may
// start now, in the same order, carrying the process that provisions it and the
// variant that was chosen.
//
// Two functions rather than one because they answer to different readers. Next
// is the question — which lines are ready — and is what a person or a status
// screen wants; Ready is the same answer with the detail a machine needs to do
// something about it.
func Ready(o Order) []Line {
	keys := Next(o)
	byKey := make(map[string]Line, len(o.Lines))
	for _, l := range o.Lines {
		byKey[l.Key()] = l
	}
	out := make([]Line, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}
