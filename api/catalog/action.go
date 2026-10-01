package catalog

import (
	"regexp"
	"sort"
	"strings"
)

// A product's actions (ADR-0429).
//
// ADR-0425 gave a lifecycle process three operations — provision, change, deprovision
// — as a closed map from operation to message. A product has, in practice, an unknown
// number of things that can be asked of what somebody holds: more storage, a password
// reset, an inactivation. So the list is open, and what an action *means* to the
// order and the inventory is closed: its effect. The order interprets every action it
// is asked to run through four effects, whatever the product calls the action.
//
// An action is a command — an intention that may be refused — named by a key that is
// the contract and a message that is the process's business, the way a published
// interface maps an entry-point name to an element (ADR-0373).

// The effects an action can have on the position it is asked of. Closed, because the
// order layer has to interpret each one.
const (
	// EffectProvision grants: pending → running → done.
	EffectProvision = "provision"
	// EffectDeprovision revokes: done → returning → returned.
	EffectDeprovision = "deprovision"
	// EffectChange changes the configuration of what is held; the position stays
	// done, and neither the item nor the variant changes (ADR-0359).
	EffectChange = "change"
	// EffectService changes nothing about what is held: the process does something
	// for the holder — a password reset, an inactivation (ADR-0429 §10).
	EffectService = "service"
)

// The keys of the two actions the order itself runs. They are reserved for the two
// effects, so trigger ids and recorded instances keep the names ADR-0425 gave them.
const (
	ActionProvision   = OpProvision
	ActionDeprovision = OpDeprovision
	// ActionChange is the key the legacy change route addresses (ADR-0428).
	ActionChange = OpChange
)

// Who may ask for an action. Closed for the same reason the effects are.
const (
	// TriggerCustomer is the orderer, the recipient who holds the right, or an
	// operator acting for either (ADR-0429 §10, decision 4).
	TriggerCustomer = "customer"
	// TriggerOperator is somebody running the service.
	TriggerOperator = "operator"
	// TriggerSystem is something observed about the held right, not a person: a
	// threshold crossed, an expiry near (ADR-0429 §1).
	TriggerSystem = "system"
)

// How a command can end. Each outcome is a fact, published under the event type the
// action declares for it (ADR-0429 §3).
const (
	OutcomeCompleted = "completed"
	OutcomeRejected  = "rejected"
	OutcomeFailed    = "failed"
)

var (
	knownEffects  = map[string]bool{EffectProvision: true, EffectDeprovision: true, EffectChange: true, EffectService: true}
	knownTriggers = map[string]bool{TriggerCustomer: true, TriggerOperator: true, TriggerSystem: true}
	knownOutcomes = map[string]bool{OutcomeCompleted: true, OutcomeRejected: true, OutcomeFailed: true}
	// actionKeyPattern is the shape of a key: what a requirement, a capability's
	// interface and a portal route point at, so it follows ADR-0305's rules for a key.
	actionKeyPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

// Action is one thing that can be asked of a position of this product.
type Action struct {
	// Key is the contract: what the portal, the order and a later requirement or
	// capability name. Lower-case letters, digits and dashes; unique per product.
	Key string `json:"key"`
	// Message is the message the lifecycle process starts or waits at for this
	// action — the process's business, so renaming it changes the mapping and not
	// what an order asks for. Unique per product.
	Message string `json:"message"`
	// Effect is what the action does to the position; one of the Effect constants.
	Effect string `json:"effect"`
	// Triggers says who may ask for it; Trigger constants. The order starts the
	// provision action, so it names none.
	Triggers []string `json:"triggers,omitempty"`
	// Labels is what a button says, per language. A missing one is a reported
	// translation gap, never a refusal (ADR-0414).
	Labels map[string]string `json:"labels,omitempty"`
	// Form names an Atlas form for what the action needs — the new size, the reason
	// (ADR-0358). Like a product's configuration form, it is not resolved here.
	Form string `json:"form,omitempty"`
	// Outcomes maps an outcome to the event type it is published under. An outcome
	// it does not name is published as <message>.<outcome> (ADR-0429 §3).
	Outcomes map[string]string `json:"outcomes,omitempty"`
}

// ActionList is the product's actions, in either shape it may say them in: the
// declared list, or — for a product that still carries ADR-0425's operation map — the
// actions that map means. Nil for a product with no lifecycle process. Every consumer
// asks this rather than reading either field, so the two shapes cannot be read two
// ways.
func (it Item) ActionList() []Action {
	if !it.UsesLifecycleProcess() {
		return nil
	}
	if len(it.Actions) > 0 {
		return it.Actions
	}
	return actionsFromOperations(it.Operations)
}

// actionsFromOperations reads ADR-0425's operation map as the actions it means, in a
// fixed order. Provision is started by the order; the return and the change are what
// the orderer or an operator may ask for today.
func actionsFromOperations(ops map[string]string) []Action {
	var out []Action
	for _, a := range []Action{
		{Key: ActionProvision, Effect: EffectProvision},
		{Key: ActionDeprovision, Effect: EffectDeprovision, Triggers: []string{TriggerCustomer, TriggerOperator}},
		{Key: ActionChange, Effect: EffectChange, Triggers: []string{TriggerCustomer, TriggerOperator}},
	} {
		msg := strings.TrimSpace(ops[a.Key])
		if msg == "" {
			continue
		}
		a.Message = msg
		out = append(out, a)
	}
	return out
}

// actionFor is the action an operation or key names: provision and deprovision by
// effect, any other name by key.
func (it Item) actionFor(name string) (Action, bool) {
	for _, a := range it.ActionList() {
		switch name {
		case OpProvision, OpDeprovision:
			if a.Effect == name {
				return a, true
			}
		default:
			if a.Key == name {
				return a, true
			}
		}
	}
	return Action{}, false
}

// ActionNamed is the action this product declares under key, in either shape.
func (it Item) ActionNamed(key string) (Action, bool) {
	for _, a := range it.ActionList() {
		if a.Key == key {
			return a, true
		}
	}
	return Action{}, false
}

// OwnsMessage reports whether one of this product's actions starts or waits at the
// message: the name no inbound watch may claim and no name-correlated publish may
// reach around the order (ADR-0425 §8, ADR-0429 §1). It returns the action's key.
func (it Item) OwnsMessage(message string) (string, bool) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", false
	}
	for _, a := range it.ActionList() {
		if strings.TrimSpace(a.Message) == msg {
			return a.Key, true
		}
	}
	return "", false
}

// checkActions holds what a declared action list owes to publish, beyond what every
// lifecycle binding owes. It is the part of the rule the catalogue can check alone;
// whether the process starts or waits at each message is [LifecycleProblems]'s.
func checkActions(it Item, add func(Problem)) {
	say := func(msg string) { add(Problem{Item: it.ID, Message: msg}) }
	keys := map[string]bool{}
	messages := map[string]string{}
	count := map[string]int{}
	for _, a := range it.Actions {
		key := a.Key
		switch {
		case !actionKeyPattern.MatchString(key):
			say("action key " + quote(key) + " is not lower-case letters, digits and dashes, " +
				"1 to 64 characters; the key is what an order and the portal name")
		case keys[key]:
			say("action key " + key + " is used twice; an order could not tell which one it asked for")
		}
		keys[key] = true

		if !knownEffects[a.Effect] {
			say("action " + key + ": effect " + a.Effect + " is not one an order interprets " +
				"(provision, deprovision, change, service)")
		} else {
			count[a.Effect]++
		}
		switch {
		case (a.Effect == EffectProvision || a.Effect == EffectDeprovision) && key != a.Effect:
			say("action " + key + ": an action of effect " + a.Effect + " is keyed " + a.Effect +
				", the name the order starts it by")
		case (key == ActionProvision || key == ActionDeprovision) && a.Effect != key && knownEffects[a.Effect]:
			say("action key " + key + " is reserved for the action of effect " + key)
		}

		msg := strings.TrimSpace(a.Message)
		if msg == "" {
			say("action " + key + " names no message; it is the message the process starts " +
				"or waits at for it")
		} else if other, dup := messages[msg]; dup {
			say("message " + msg + " is used by two actions, " + other + " and " + key +
				"; a delivery could not tell which one was asked for")
		} else {
			messages[msg] = key
		}

		checkTriggers(it.ID, a, say)

		outs := make([]string, 0, len(a.Outcomes))
		for o := range a.Outcomes {
			outs = append(outs, o)
		}
		sort.Strings(outs)
		for _, o := range outs {
			if !knownOutcomes[o] {
				say("action " + key + ": outcome " + o + " is not one a command ends in " +
					"(completed, rejected, failed)")
			} else if strings.TrimSpace(a.Outcomes[o]) == "" {
				say("action " + key + ": outcome " + o + " names no event type; leave it out " +
					"to publish it as " + msg + "." + o)
			}
		}
		if a.Form != "" && strings.TrimSpace(a.Form) == "" {
			say("action " + key + " names a blank form; leave it out for an action that " +
				"needs no details")
		}
	}
	for _, e := range []string{EffectProvision, EffectDeprovision} {
		switch {
		case count[e] == 0:
			say("declares no action of effect " + e + "; a product that cannot be " +
				e + "ed is not a lifecycle")
		case count[e] > 1:
			say("declares more than one action of effect " + e + "; the order starts exactly one")
		}
	}
}

// checkTriggers holds who may ask for each effect.
func checkTriggers(item string, a Action, say func(string)) {
	for _, tr := range a.Triggers {
		if !knownTriggers[tr] {
			say("action " + a.Key + ": trigger " + tr + " is not one an action has " +
				"(customer, operator, system)")
		}
	}
	switch a.Effect {
	case EffectProvision:
		if len(a.Triggers) > 0 {
			say("action " + a.Key + " names triggers, but the order starts it; it names none")
		}
	case EffectDeprovision:
		for _, tr := range a.Triggers {
			if tr != TriggerCustomer && tr != TriggerOperator {
				say("action " + a.Key + ": deprovision is triggered by customer or operator; " +
					"expiry, recertification and reconciliation return a right through the order " +
					"and are not declared")
				break
			}
		}
	case EffectChange, EffectService:
		if len(a.Triggers) == 0 {
			say("action " + a.Key + " names no trigger; nobody could ask for it")
		}
	}
}

// actionNoun is how a problem names an entry: "action" for a declared list,
// "operation" for ADR-0425's map, so a maintainer finds the field they wrote.
func (it Item) actionNoun() string {
	if len(it.Actions) > 0 {
		return "action"
	}
	return "operation"
}

// CopyActions copies a list of actions, so a release does not share their slices
// and maps with the row it was frozen from, nor an order line with its release.
func CopyActions(in []Action) []Action {
	if in == nil {
		return nil
	}
	out := make([]Action, len(in))
	for i, a := range in {
		a.Triggers = append([]string(nil), a.Triggers...)
		a.Labels = copyOperations(a.Labels)
		a.Outcomes = copyOperations(a.Outcomes)
		out[i] = a
	}
	return out
}
