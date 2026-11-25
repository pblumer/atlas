package engine

import "github.com/pblumer/atlas/compiler"

// handleJoinRecheck runs the inclusive join's firing rule for every inclusive join that has
// tokens parked on it in the instance (ADR-0551 part 1). The processor schedules it after a
// batch in which a token in the instance completed or was terminated: the join waits until
// no token can still reach it, and a token that ends elsewhere, is interrupted or leaves on
// a flow outside the join's reach can be exactly what makes that true. Before this, the
// rule ran only when a token arrived at the join, so such a join waited for an arrival
// that would never come and the instance never completed.
//
// The rule is the arrival's own (fireInclusiveJoinIfReady), so a re-check fires a join if
// and only if an arrival at that moment would have. Its firing is the persisted
// Completed/Activating chain; the command itself is never logged, so replay never runs it
// (I4, I6).
func handleJoinRecheck(c *ProcessingContext) {
	piKey := c.cmd.Key
	pi := c.GetProcessInstance(piKey)
	if pi == nil {
		return // the instance ended in the meantime
	}
	cp := c.process(pi.ProcessDefKey)
	if !cp.HasInclusiveJoins() {
		return
	}
	// One arrival per (scope, join): the rule reads every arrival on the join, so running
	// it once per parked join is enough. Collected first, because firing changes the set.
	type parkedJoin struct {
		scope uint64
		node  int32
		key   uint64
	}
	var joins []parkedJoin
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		ei := c.GetElementInstance(elKey)
		if ei == nil || compiler.BpmnType(ei.BpmnElementType) != compiler.TypeInclusiveGateway ||
			cp.Node(ei.ElementId).IncomingCount <= 1 {
			return
		}
		for _, j := range joins {
			if j.scope == ei.FlowScopeKey && j.node == ei.ElementId {
				return
			}
		}
		joins = append(joins, parkedJoin{scope: ei.FlowScopeKey, node: ei.ElementId, key: elKey})
	})
	for _, j := range joins {
		ei := c.GetElementInstance(j.key)
		if ei == nil {
			continue
		}
		// A join that could not route is parked on an incident and waits for an operator;
		// resolving the incident resumes it. A re-check must not raise the incident again.
		if joinHasIncident(c, piKey, j.scope, j.node) {
			continue
		}
		v := *ei
		fireInclusiveJoinIfReady(c, j.key, &v)
	}
}

// joinHasIncident reports whether any arrival parked on the join carries an open incident.
func joinHasIncident(c *ProcessingContext, piKey, scope uint64, node int32) bool {
	for _, a := range c.ArrivalsOnNode(piKey, scope, node) {
		if c.GetIncident(a.Key) != nil {
			return true
		}
	}
	return false
}
