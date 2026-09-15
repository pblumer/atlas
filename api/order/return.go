package order

import (
	"fmt"
	"sort"
)

// Giving back what an order granted.
//
// Every line has carried a DeprovisionProcess since the order was placed, frozen
// there so that revoking what this order granted uses the process that was in
// force when it was granted. Nothing ever called it. The catalogue has known how
// to take something back since the first day; the portal only never asked.
//
// A return is the mirror of a provisioning and is modelled as one: the same
// process start, the same report of an outcome, a status while it is in flight and
// a status when it has come back. What it is *not* is a cancellation. A
// cancellation is a line that never was; a return is a line that was and is no
// longer, and a record that cannot tell the two apart cannot answer who had access
// when — which is the question an audit of an access record exists to answer.

// Returnable reports whether a line can be given back now, and says why not.
//
// Three things have to hold. It must be held — a skipped line the recipient got
// elsewhere was never this order's to revoke, and one already returned is gone. It
// must have a process to revoke it with, because a return with nothing to run is a
// status change pretending to be an act. And nothing still held may require it.
//
// That last one is the precedence graph read backwards. Provisioning ordered the
// account before the laptop that needs it; giving back runs the other way, and an
// account revoked under a laptop that still uses it leaves the laptop working
// until somebody notices, or not working for a reason nobody connects to this.
func Returnable(o Order, itemID string) error {
	var line Line
	found := false
	for _, l := range o.Lines {
		if l.ItemID == itemID {
			line, found = l, true
			break
		}
	}
	if !found {
		return fmt.Errorf("order %s carries no line for %s", o.ID, itemID)
	}
	// Narrower than Held on purpose. A line already on its way back must not be
	// asked for twice — two revocations racing against one target system is how a
	// half-deleted account happens — while one whose revocation failed is exactly
	// what a retry is for.
	if line.Status != StatusDone && line.Status != StatusReturnFailed {
		switch line.Status {
		case StatusReturning:
			return fmt.Errorf("order: line %s is already going back", itemID)
		default:
			return fmt.Errorf("order: line %s is %s and is not held by anybody", itemID, line.Status)
		}
	}
	if line.DeprovisionProcess == "" {
		return fmt.Errorf("order: line %s names no process to revoke it with", itemID)
	}
	if blockers := stillNeeding(o, itemID); len(blockers) > 0 {
		return fmt.Errorf("order: line %s is still needed by %v, which %s held",
			itemID, blockers, plural(len(blockers)))
	}
	return nil
}

// stillNeeding names the lines that require this one and are still held — which
// includes one whose own return is under way or has failed, because neither is
// gone. Sorted, so the message is the same every time.
func stillNeeding(o Order, itemID string) []string {
	held := map[string]bool{}
	for _, l := range o.Lines {
		if l.Status.Held() {
			held[l.ItemID] = true
		}
	}
	var out []string
	for dependent, needs := range o.Requires {
		if !held[dependent] {
			continue
		}
		for _, need := range needs {
			if need == itemID {
				out = append(out, dependent)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func plural(n int) string {
	if n == 1 {
		return "is still"
	}
	return "are still"
}

// Returning marks a line's revocation as under way. The process itself is started
// by the caller, which is the half that needs an engine.
func Returning(o Order, itemID string, at int64) (Order, error) {
	if err := Returnable(o, itemID); err != nil {
		return o, err
	}
	if at == 0 {
		return o, fmt.Errorf("order: returning line %s needs the moment it happened", itemID)
	}
	lines := make([]Line, len(o.Lines))
	copy(lines, o.Lines)
	for i := range lines {
		if lines[i].ItemID == itemID {
			lines[i].Status = StatusReturning
			break
		}
	}
	// Not propagated. A line on its way back is not a cause of anything: the guard
	// above already proved nothing held requires it, and running the pass here
	// would only recompute blocks for lines that are all settled anyway.
	o.Lines = lines
	o.UpdatedAt = at
	return o, nil
}

// ReturnProcessOf is the process that revokes a line, for a caller about to start
// it. It reads it from the order rather than from the catalogue, because what was
// granted is what has to be revoked — a product whose deprovisioning was changed
// afterwards must not revoke an older grant by the newer rules.
func ReturnProcessOf(o Order, itemID string) string {
	for _, l := range o.Lines {
		if l.ItemID == itemID {
			return l.DeprovisionProcess
		}
	}
	return ""
}
