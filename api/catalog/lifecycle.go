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

// The instance forms of a lifecycle process
// (ADR-0428).
const (
	// FormPerOperation starts an instance for every operation (ADR-0425). It is
	// what an empty LifecycleForm means.
	FormPerOperation = "per-operation"
	// FormPerPosition starts one instance per order position at provisioning and
	// delivers every later operation to it.
	FormPerPosition = "per-position"
)

// PerPosition reports whether the item's lifecycle runs as one instance per order
// position.
func (it Item) PerPosition() bool {
	return it.UsesLifecycleProcess() && it.LifecycleForm == FormPerPosition
}

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
// binds. The two-process form offers provision and deprovision. On a lifecycle
// process, provision and deprovision are the actions of those effects and any other
// name is an action's key (ADR-0429), read from either shape the product says its
// actions in.
func (it Item) BindingFor(op string) Binding {
	if it.UsesLifecycleProcess() {
		a, ok := it.actionFor(op)
		msg := strings.TrimSpace(a.Message)
		if !ok || msg == "" {
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
		switch {
		case len(it.Operations) > 0 && len(it.Actions) > 0:
			// Two answers to what a product's actions are is no answer (ADR-0429).
			add(Problem{Item: it.ID, Message: "carries an operation map and actions; bind one " +
				"shape — the actions say everything the map did"})
		case len(it.Actions) > 0:
			checkActions(it, add)
		default:
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
		}
		if it.LifecycleProcess == FulfilmentProcess {
			add(Problem{Item: it.ID, Message: "is bound to " + FulfilmentProcess + ", the process " +
				"that works the order itself; it would start itself for this position, again " +
				"and again, without end"})
		}
		switch it.LifecycleForm {
		case "", FormPerOperation, FormPerPosition:
		default:
			add(Problem{Item: it.ID, Message: "lifecycle form " + it.LifecycleForm + " is not one " +
				"a lifecycle process runs in (" + FormPerOperation + ", " + FormPerPosition + ")"})
		}
		return
	}
	if it.LifecycleForm != "" {
		add(Problem{Item: it.ID, Message: "names a lifecycle form but binds no lifecycle process"})
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
	if len(it.Actions) > 0 {
		// The two-process form starts its processes by hand; there is no message an
		// action could name.
		add(Problem{Item: it.ID, Message: "names actions but binds no lifecycle process"})
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

// ShapeLookup answers what a per-position lifecycle process owes beyond its start
// events: which messages it waits for, and whether it can circle without waiting. A
// lookup that does not implement it cannot check a per-position binding, and
// [LifecycleProblems] says so rather than passing it.
type ShapeLookup interface {
	// CatchPoints returns the newest deployed version's message catch points: for
	// each, the message it waits for and whether it declares a correlation key.
	CatchPoints(processID string) []CatchPoint
	// WaitlessCycle returns the elements of a cycle in the newest deployed version
	// that waits for nothing outside the token, or nil.
	WaitlessCycle(processID string) []string
}

// CatchPoint is one element of a process that waits for a message.
type CatchPoint struct {
	Element    string
	Message    string
	Correlated bool
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
		for _, a := range it.ActionList() {
			msg := strings.TrimSpace(a.Message)
			// In the per-position form a change or a service is delivered to the
			// running strand, so it is a catch there, checked below — not a start.
			if msg == "" || have[msg] || (it.PerPosition() && deliveredToStrand(a.Effect)) {
				continue
			}
			out = append(out, Problem{Item: it.ID, Message: it.actionNoun() + " " + a.Key + " names " +
				msg + ", which is not a message start event of " + it.LifecycleProcess})
		}
		if it.PerPosition() {
			out = append(out, perPositionProblems(it, look)...)
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

// perPositionProblems holds what a per-position binding owes beyond ADR-0425's:
// the later operations are messages the strand waits for, keyed on the position, and
// no cycle in it runs without waiting.
func perPositionProblems(it Item, look EntryPointLookup) []Problem {
	shape, ok := look.(ShapeLookup)
	if !ok {
		return []Problem{{Item: it.ID, Message: "lifecycle process " + it.LifecycleProcess +
			" runs per position, and this server cannot read what the process waits for"}}
	}
	var out []Problem
	catches := map[string][]CatchPoint{}
	for _, c := range shape.CatchPoints(it.LifecycleProcess) {
		catches[c.Message] = append(catches[c.Message], c)
	}
	for _, a := range it.ActionList() {
		msg := strings.TrimSpace(a.Message)
		if msg == "" || (a.Effect != EffectDeprovision && !deliveredToStrand(a.Effect)) {
			continue
		}
		points := catches[msg]
		if len(points) == 0 {
			out = append(out, Problem{Item: it.ID, Message: it.actionNoun() + " " + a.Key + " names " + msg +
				", which " + it.LifecycleProcess + " never waits for; a per-position lifecycle " +
				"delivers it to the running instance, so the strand must catch it"})
			continue
		}
		for _, c := range points {
			if !c.Correlated {
				out = append(out, Problem{Item: it.ID, Message: c.Element + " waits for " + msg +
					" without a correlation key; key it on the position, or one message by name " +
					"reaches every position of the product"})
			}
		}
	}
	if cycle := shape.WaitlessCycle(it.LifecycleProcess); cycle != nil {
		out = append(out, Problem{Item: it.ID, Message: "lifecycle process " + it.LifecycleProcess +
			" circles through " + strings.Join(cycle, ", ") + " without waiting for anything; " +
			"an instance that lives as long as the right would run until the engine stops it"})
	}
	return out
}

// deliveredToStrand reports whether an action of this effect reaches a per-position
// product's running instance rather than starting one: a change and a service do;
// a provision starts the strand, and a deprovision does both (ADR-0428 §3).
func deliveredToStrand(effect string) bool {
	return effect == EffectChange || effect == EffectService
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
