package engine

import (
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// MigrationElement is one live element instance, as the migration validator needs to
// see it. It is a plain struct rather than model.ElementInstanceValue so the validator
// can be called from the API — which reads the store — and from the processor — which
// reads the in-flight transaction — over the same rules (ADR-0162).
type MigrationElement struct {
	Key             uint64
	ElementId       int32
	FlowScopeKey    uint64
	BpmnElementType uint8
	MultiInstance   uint8
	EventGatewayKey uint64
	AttachedToKey   uint64
}

// MigrationElementOf projects a stored element instance onto what the validator reads.
func MigrationElementOf(key uint64, ei *model.ElementInstanceValue) MigrationElement {
	return MigrationElement{
		Key:             key,
		ElementId:       ei.ElementId,
		FlowScopeKey:    ei.FlowScopeKey,
		BpmnElementType: ei.BpmnElementType,
		MultiInstance:   ei.MultiInstance,
		EventGatewayKey: ei.EventGatewayKey,
		AttachedToKey:   ei.AttachedToKey,
	}
}

// MigrationProblem is one reason a migration is refused, named so an operator can act
// on it: which element instance, which element the modeler would recognise, and what
// is wrong with moving it.
type MigrationProblem struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ElementID          string `json:"elementId"`
	Reason             string `json:"reason"`
}

// ValidateMigration reports every reason the given mapping cannot rebind these live
// element instances from the `from` definition to the `to` one. An empty result means
// the migration is safe to submit; anything else refuses it (ADR-0162).
//
// It refuses rather than guesses, and that is the whole point of it. A token left on an
// index that means something else in the target graph, or landed on an element of
// another type, corrupts an instance in a way no later fix repairs — while a refused
// migration costs an operator one message. So every rule here answers "could this
// rebinding produce an instance the engine can still run correctly?", and anything it
// cannot answer yes to is a problem.
//
// It is a pure function of its arguments: the API calls it to refuse before submitting
// a command, and the processor calls it again on the run loop, where the state may have
// moved since. It never runs inside applyToState — the fold reads the mapping the
// event froze and computes nothing (invariant I4).
func ValidateMigration(from, to *compiler.CompiledProcess, live []MigrationElement, mapping map[int32]int32) []MigrationProblem {
	if from == nil || to == nil {
		return []MigrationProblem{{Reason: "the source or target definition is not deployed"}}
	}
	var problems []MigrationProblem
	add := func(el MigrationElement, format string, args ...any) {
		problems = append(problems, MigrationProblem{
			ElementInstanceKey: el.Key,
			ElementID:          from.ElementBpmnId(el.ElementId),
			Reason:             fmt.Sprintf(format, args...),
		})
	}
	// Which element instance holds which source index, so a scope's mapping can be
	// checked against its parent's without a second pass over the list.
	byKey := make(map[uint64]MigrationElement, len(live))
	for _, el := range live {
		byKey[el.Key] = el
	}

	for _, el := range live {
		target, ok := mapping[el.ElementId]
		if !ok {
			// The token would sit on an index that means something else in the target
			// graph, or nothing at all. This is the rule the others exist to refine.
			add(el, "no element in the target version is mapped to it, so its token would be stranded")
			continue
		}
		if target < 0 || int(target) >= to.NodeCount() {
			// Node indexes the graph directly, so an index from outside it — a stale
			// mapping, a hand-written override — has to be checked before it is used.
			add(el, "maps to element index %d, which the target version does not have", target)
			continue
		}
		src, dst := from.Node(el.ElementId), to.Node(target)
		if src.Type != dst.Type {
			// BpmnElementType is stored on the element instance for dispatch: a token
			// waiting on a receive task cannot become a token on a user task.
			add(el, "is a %s but maps to %s, which is a %s", src.Type, to.ElementBpmnId(target), dst.Type)
			continue
		}
		// The parent scope must map to the mapped element's parent scope, or the token
		// moves into or out of a subprocess — which is a different instance, not a
		// rebinding of this one.
		if parent, isChild := byKey[el.FlowScopeKey]; isChild {
			parentTarget, parentMapped := mapping[parent.ElementId]
			if !parentMapped {
				add(el, "sits inside %s, which is not mapped", from.ElementBpmnId(parent.ElementId))
				continue
			}
			if got := to.Node(target).FlowScope; got != parentTarget {
				add(el, "would move out of %s into a different scope in the target version",
					from.ElementBpmnId(parent.ElementId))
				continue
			}
		}
		// A multi-instance body seeds iterations and an iteration runs the node's real
		// behavior (ADR-0077); swapping the two makes the scope's child counts mean
		// something else. The target must be multi-instance in the same way.
		srcMulti, dstMulti := from.Node(el.ElementId).MultiInstance >= 0, dst.MultiInstance >= 0
		if el.MultiInstance != 0 && !dstMulti {
			add(el, "is running as a multi-instance activity but maps to %s, which is not one", to.ElementBpmnId(target))
			continue
		}
		if srcMulti != dstMulti {
			add(el, "changes between a multi-instance and a plain activity, which its scope's child counts are keyed on")
			continue
		}
		// A catch armed by an event-based gateway belongs to a race group: the first to
		// fire cancels the others (ADR-0110). If a member is not mapped with the rest,
		// the losers can no longer be cancelled.
		if el.EventGatewayKey != 0 {
			if gw, ok := byKey[el.EventGatewayKey]; ok {
				if _, mapped := mapping[gw.ElementId]; !mapped {
					add(el, "is armed by an event-based gateway that is not mapped, so its race group would break apart")
					continue
				}
			}
		}
		// A boundary event and the activity it is attached to must stay attached.
		if el.AttachedToKey != 0 {
			if host, ok := byKey[el.AttachedToKey]; ok {
				hostTarget, hostMapped := mapping[host.ElementId]
				if !hostMapped {
					add(el, "is attached to %s, which is not mapped", from.ElementBpmnId(host.ElementId))
					continue
				}
				attached := false
				for _, b := range to.BoundaryEvents(hostTarget) {
					if b == target {
						attached = true
						break
					}
				}
				if !attached {
					add(el, "would no longer be attached to %s in the target version", from.ElementBpmnId(host.ElementId))
					continue
				}
			}
		}
		// A waiting catch's subscription is keyed by the message or signal *name*, not
		// only labelled with it, so a target that waits on a different one leaves the
		// armed subscription stale — the instance would wait forever on a name its
		// model no longer mentions. Re-arming is a follow-up; refusing is this record.
		if srcName, ok := waitName(from, el.ElementId); ok {
			if dstName, ok := waitName(to, target); ok && dstName != srcName {
				add(el, "waits for %q but maps to an element waiting for %q; the armed subscription would be stale", srcName, dstName)
				continue
			}
		}
	}
	return problems
}

// waitName is the message or signal a waiting element is subscribed by, and whether the
// element waits on a name at all. It is the identity half of a subscription's key —
// what makes a changed name a stale subscription rather than a relabelled one.
func waitName(cp *compiler.CompiledProcess, id int32) (string, bool) {
	n := cp.Node(id)
	switch n.Type {
	case compiler.TypeMessageCatchEvent:
		return cp.MessageCatch(n.Detail).MessageName, true
	}
	return "", false
}
