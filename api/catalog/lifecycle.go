package catalog

import (
	"sort"
	"strings"
)

// A product's lifecycle process (ADR-0425).
//
// A product used to bind two processes, one that grants and one that revokes. It may
// instead bind one, whose operations are message start events: the process starts at
// the one an order or an external system triggers, and at nothing else. This file
// holds what the catalogue can say about such a binding on its own; what it needs
// the engine for — whether the process is deployed and has those start events — is
// asked through [EntryPointLookup].

// The operations a lifecycle process can offer.
const (
	OpProvision   = "provision"
	OpChange      = "change"
	OpDeprovision = "deprovision"
)

// knownOperations are the keys Operations may carry. A key nothing asks for is a
// typo waiting to be the operation somebody believed was bound.
var knownOperations = map[string]bool{OpProvision: true, OpChange: true, OpDeprovision: true}

// Binding is where one operation of a product starts: a process, and — for a
// lifecycle process — the message start event in it. Message is empty for the two-
// process form, which starts its process by hand.
type Binding struct {
	Process string `json:"process"`
	Message string `json:"message,omitempty"`
}

// Bound reports whether the binding names anything to start.
func (b Binding) Bound() bool { return strings.TrimSpace(b.Process) != "" }

// Triggered reports whether this binding starts its process through a trigger
// rather than by hand.
func (b Binding) Triggered() bool { return b.Message != "" }

// UsesLifecycleProcess reports whether the item binds one process for its lifecycle.
func (it Item) UsesLifecycleProcess() bool { return strings.TrimSpace(it.LifecycleProcess) != "" }

// BindingFor is where an operation of this item starts, in whichever form the item
// binds. The two-process form offers provision and deprovision; change exists only
// on a lifecycle process that names it.
func (it Item) BindingFor(op string) Binding {
	if it.UsesLifecycleProcess() {
		msg := strings.TrimSpace(it.Operations[op])
		if msg == "" {
			return Binding{}
		}
		return Binding{Process: it.LifecycleProcess, Message: msg}
	}
	switch op {
	case OpProvision:
		return Binding{Process: it.ProvisionProcess}
	case OpDeprovision:
		return Binding{Process: it.DeprovisionProcess}
	}
	return Binding{}
}

// checkBindings holds what an item's process bindings owe to publish, in either
// form. It is the part of the rule the catalogue can check alone.
func checkBindings(it Item, add func(Problem)) {
	if it.UsesLifecycleProcess() {
		if it.ProvisionProcess != "" || it.DeprovisionProcess != "" {
			add(Problem{Item: it.ID, Message: "binds a lifecycle process and a provision or " +
				"deprovision process at once; bind one form — two answers to where an order " +
				"starts are no answer"})
		}
		for _, op := range []string{OpProvision, OpDeprovision} {
			if strings.TrimSpace(it.Operations[op]) == "" {
				add(Problem{Item: it.ID, Message: "lifecycle process " + it.LifecycleProcess +
					" names no start event for " + op})
			}
		}
		var unknown []string
		for op := range it.Operations {
			if !knownOperations[op] {
				unknown = append(unknown, op)
			}
		}
		sort.Strings(unknown)
		for _, op := range unknown {
			add(Problem{Item: it.ID, Message: "operation " + op + " is not one a product offers " +
				"(provision, change, deprovision)"})
		}
		if it.LifecycleProcess == FulfilmentProcess {
			add(Problem{Item: it.ID, Message: "is bound to " + FulfilmentProcess + ", the process " +
				"that works the order itself; it would start itself for this position, again " +
				"and again, without end"})
		}
		return
	}
	if it.ProvisionProcess == "" {
		add(Problem{Item: it.ID, Message: "no provision process bound"})
	}
	if it.DeprovisionProcess == "" {
		// A catalogue that can only grant is not a lifecycle.
		add(Problem{Item: it.ID, Message: "no deprovision process bound"})
	}
	if len(it.Operations) > 0 {
		add(Problem{Item: it.ID, Message: "names operations but binds no lifecycle process"})
	}
	// The orchestration that works an order is never the process of a position
	// in it. Bound as one, it starts itself for that position, the new copy asks
	// what may start and starts the same position again, and every round adds
	// another: one installation grew to hundreds of orchestrations and their
	// provisioning tasks per minute before anybody could see why.
	if it.ProvisionProcess == FulfilmentProcess || it.DeprovisionProcess == FulfilmentProcess {
		add(Problem{Item: it.ID, Message: "is bound to " + FulfilmentProcess + ", the process " +
			"that works the order itself; it would start itself for this position, again " +
			"and again, without end"})
	}
}

// EntryPointLookup answers what the catalogue cannot know alone: which start events
// the newest deployed version of a process has. It is the server's to answer, like
// [ProcessLookup].
type EntryPointLookup interface {
	// EntryPoints returns the message names of the process's root message start
	// events, whether it has a none start, and whether any version is deployed.
	EntryPoints(processID string) (messages []string, hasNone bool, deployed bool)
}

// LifecycleProblems checks the lifecycle bindings of items against what is deployed:
// the process exists, every bound operation is one of its message start events, and
// it has no none start — an entry the catalogue never uses and a create by hand
// would seed (ADR-0426). It runs outside [Publish], which is pure, and before a
// release is saved.
func LifecycleProblems(items []Item, look EntryPointLookup) []Problem {
	if look == nil {
		return nil
	}
	var out []Problem
	for _, it := range items {
		if !it.UsesLifecycleProcess() {
			continue
		}
		messages, hasNone, deployed := look.EntryPoints(it.LifecycleProcess)
		if !deployed {
			out = append(out, Problem{Item: it.ID, Message: "lifecycle process " +
				it.LifecycleProcess + " is not deployed"})
			continue
		}
		have := make(map[string]bool, len(messages))
		for _, m := range messages {
			have[m] = true
		}
		ops := make([]string, 0, len(it.Operations))
		for op := range it.Operations {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		for _, op := range ops {
			if msg := strings.TrimSpace(it.Operations[op]); msg != "" && !have[msg] {
				out = append(out, Problem{Item: it.ID, Message: "operation " + op + " names " +
					msg + ", which is not a message start event of " + it.LifecycleProcess})
			}
		}
		if hasNone {
			out = append(out, Problem{Item: it.ID, Message: "lifecycle process " +
				it.LifecycleProcess + " has a none start event; a lifecycle process is entered " +
				"only through its operations, and a none start is an entry a start by hand " +
				"would take"})
		}
	}
	sortProblems(out)
	return out
}

// copyOperations copies an item's operation map, so a release does not share it
// with the row it was frozen from.
func copyOperations(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
