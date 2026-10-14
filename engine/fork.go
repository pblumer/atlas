package engine

import (
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// ValidateFork reports every reason an instance cannot be forked onto the target
// version at the given resume points: ended where it is, and continued as a new
// instance of `to` starting at those elements
// (ADR-0389).
//
// A fork is what an operator reaches for when [ValidateMigration] has refused — the
// token sits on an element the target version no longer has, or has as something else,
// so there is no honest way to rebind it. What crosses instead is the instance's data
// and a human's decision about where the work picks up again. That decision is the one
// thing this cannot check, so everything around it is checked strictly: a resume point
// that cannot seed an execution is refused before anything is written, because the
// alternative is a terminated predecessor and a successor that can never run.
//
// It is a pure function of its arguments, called by the API to refuse before submitting
// a command and again by the processor on the run loop, where the instance may have
// moved since. `parent` is the instance's ParentElementInstanceKey. It never runs inside
// applyToState (invariant I4).
func ValidateFork(from, to *compiler.CompiledProcess, parent uint64, resume []int32) []MigrationProblem {
	if from == nil || to == nil {
		return []MigrationProblem{{Reason: "the source or target definition is not deployed"}}
	}
	if from.Key == to.Key {
		return []MigrationProblem{{Reason: "the instance is already running this version"}}
	}
	if from.ProcessId() != to.ProcessId() {
		return []MigrationProblem{{Reason: fmt.Sprintf(
			"the target is process %q, but this instance runs %q — an instance can only continue in a version of its own process",
			to.ProcessId(), from.ProcessId())}}
	}
	if parent != 0 {
		// The caller waits on *this* instance's key through its call activity (ADR-0076).
		// Ending it and handing the caller a different child is a decision about that
		// contract, not a detail of this one — so it is refused rather than guessed at.
		return []MigrationProblem{{Reason: "this instance was started by a call activity, and its caller waits for it; a called instance cannot be forked"}}
	}
	if len(resume) == 0 {
		// An instance with no token never ends and never advances: it would wait for
		// nothing, forever, which is worse than the refusal.
		return []MigrationProblem{{Reason: "no resume point was named, so the new instance would start with no token at all"}}
	}

	var problems []MigrationProblem
	add := func(id int32, format string, args ...any) {
		name := ""
		if id >= 0 && int(id) < to.NodeCount() {
			name = to.ElementBpmnId(id)
		}
		problems = append(problems, MigrationProblem{ElementID: name, Reason: fmt.Sprintf(format, args...)})
	}
	seen := make(map[int32]struct{}, len(resume))
	for _, id := range resume {
		if id < 0 || int(id) >= to.NodeCount() {
			problems = append(problems, MigrationProblem{
				Reason: fmt.Sprintf("resume point %d is not an element of the target version", id)})
			continue
		}
		if _, dup := seen[id]; dup {
			// Two tokens where the operator asked for one. If two paths are wanted, that
			// is two elements in the model, not one element named twice.
			add(id, "is named twice as a resume point, which would start two tokens on it")
			continue
		}
		seen[id] = struct{}{}
		n := to.Node(id)
		// The element's own kind is asked first: an event-subprocess start event is also
		// an element inside a scope, and "this is armed by its scope" is the answer an
		// operator can act on, where "this is nested" would send them looking for an
		// enclosing element to resume at instead.
		switch {
		case n.Type == compiler.TypeBoundaryEvent:
			// A boundary event is armed by the activity it is attached to. On its own it
			// is a catch with nothing behind it.
			add(id, "is a boundary event, which is armed by the activity it is attached to and cannot be started on its own")
			continue
		case n.Type == compiler.TypeEventSubProcessStart,
			n.FlowScope != -1 && to.IsEventSubProcess(n.FlowScope):
			// The scope is asked as well as the type: an event subprocess's start event
			// is the handler *container* at runtime (the trigger's ElementId is the
			// container node, see isEventSubTrigger), so the compiled start node inside
			// one is an ordinary start event and only its scope gives it away.
			add(id, "is inside an event subprocess, which its scope arms when the scope is entered rather than being started on its own")
			continue
		case to.IsEventSubProcess(id):
			add(id, "is an event subprocess, which runs when its trigger fires rather than on its own")
			continue
		case (n.Type == compiler.TypeParallelGateway || n.Type == compiler.TypeInclusiveGateway) && incomingCount(to, id) > 1:
			// A joining gateway waits for the other branches. Seeded with one token it is
			// a deadlock rather than a resume point.
			add(id, "is a joining gateway: started with a single token it would wait forever for the other branches")
			continue
		case n.FlowScope != -1:
			// Seeding inside a subprocess needs that subprocess's scope to exist first;
			// the token's FlowScopeKey would name a scope nobody created. Work that
			// belongs inside a subprocess resumes at the subprocess.
			add(id, "sits inside %s rather than in the process itself; resume at the enclosing element instead",
				elementName(to, n.FlowScope))
			continue
		}
	}
	return problems
}

// elementName is what to call an element in a message to an operator: its BPMN id, or a
// plain description when the process has none. A model built through the builder API
// rather than parsed from XML carries no ids at all, and "sits inside " reads like a
// defect where "sits inside an enclosing element" reads like the sentence it is.
func elementName(cp *compiler.CompiledProcess, id int32) string {
	if name := cp.ElementBpmnId(id); name != "" {
		return name
	}
	return "an enclosing element"
}

// incomingCount is how many sequence flows end at the given node. There is no incoming
// index — the engine only ever walks flows forwards — and this runs once per resume
// point on an operator's command, never on the token path (invariant I1).
func incomingCount(cp *compiler.CompiledProcess, target int32) int {
	n := 0
	for i := int32(0); int(i) < cp.NodeCount(); i++ {
		for _, f := range cp.Outgoing(i) {
			if cp.Flow(f).Target == target {
				n++
			}
		}
	}
	return n
}

// handleProcessInstanceForking ends a running instance and continues its work in a new
// instance of another deployed version, at the resume points the command carries
// (ADR-0389).
//
// Like a migration, the API has already validated and refused; this re-runs the same
// check on the run loop, because between that answer and this command the instance is
// free to have moved or finished. A fork that no longer holds is dropped rather than
// half-applied — which here means the predecessor is never terminated without its
// successor existing, since both are emitted into the same batch and therefore commit
// under one fsync (invariant I2).
//
// It emits no new kind of event. A fork is an instance being created and another being
// terminated, plus the link each record carries to the other, so the durable surface is
// the oldest one in the engine and replay needs nothing new to reproduce it (I4/I6).
func handleProcessInstanceForking(c *ProcessingContext) {
	oldKey := c.cmd.Key
	pi := c.GetProcessInstance(oldKey)
	if pi == nil || pi.State != model.PIActive {
		return // finished, cancelled, or already forked away
	}
	targetDefKey := c.cmd.Value.process.ProcessDefKey
	from, to := c.process(pi.ProcessDefKey), c.process(targetDefKey)
	if len(ValidateFork(from, to, pi.ParentElementInstanceKey, c.cmd.StartElements)) > 0 {
		return
	}

	// What crosses: the root-scope variables, and the data objects the target version
	// still declares. Both are read here, on the run loop, and travel as ordinary
	// creation events — so the fold reads values rather than deriving them (I4).
	//
	// Only the root scope is asked. An activity-local scope is scratch that belongs to
	// an activity the successor has not reached and may not have (ADR-0068); carrying it
	// would put values under a scope key nothing in the new instance owns.
	//
	// Each value travels as it stands; the creation re-scopes it onto the successor, and
	// the write path re-stamps both the producer (no element of the successor wrote it)
	// and whether the *target* version declares the name searchable — which is the same
	// correction a migration makes, for the same reason (ADR-0244/0295).
	var vars []model.VariableValue
	c.VariablesOfScope(oldKey, func(v model.VariableValue) {
		vars = append(vars, v)
	})
	carried := map[string]model.DataObjectValue{}
	for _, d := range to.DataObjects() {
		name := to.Intern(d.Name)
		if obj := c.GetDataObject(oldKey, name); obj != nil {
			carried[name] = *obj
		}
	}

	newKey := c.NewKey()
	activateInstance(c, newKey, instanceSeed{
		Instance: model.ProcessInstanceValue{
			ProcessDefKey:          targetDefKey,
			CorrelationKey:         pi.CorrelationKey,
			PredecessorInstanceKey: oldKey,
		},
		Vars:          vars,
		StartElements: c.cmd.StartElements,
		DataObjects:   carried,
	})
	// The successor's timeline opens with the reason it exists, rather than with a token
	// appearing in the middle of the diagram (ADR-0159's record, two new kinds).
	c.AppendOperatorActionEvent(model.OperatorActionValue{
		ProcessInstanceKey: newKey,
		Kind:               model.OperatorActionForkedFrom,
		Actor:              c.cmd.Actor,
		Reason:             c.cmd.Reason,
	})
	c.AppendOperatorActionEvent(model.OperatorActionValue{
		ProcessInstanceKey: oldKey,
		Kind:               model.OperatorActionForkedTo,
		Actor:              c.cmd.Actor,
		Reason:             c.cmd.Reason,
		FromProcessDefKey:  pi.ProcessDefKey,
	})
	// Last, so the record that ends the predecessor already names the successor it is
	// handing its work to — and so a reader of the history never sees a terminated
	// instance whose successor does not yet exist. Re-read rather than reusing the value
	// above: the terminal event carries the whole record into history, and reading it
	// through the transaction is what keeps that record whatever this batch last made
	// it, not whatever it was when this handler started.
	if latest := c.GetProcessInstance(oldKey); latest != nil {
		pi = latest
	}
	terminateInstance(c, oldKey, pi, newKey)
}
