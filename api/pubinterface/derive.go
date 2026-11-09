package pubinterface

import (
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// This file reads a contract out of a deployed definition (ADR-0373: "derive what
// can be derived"). The publisher never types an entry point's kind, correlation key
// or payload; they choose which elements to publish and what to call them, and the
// rest is read from the compiled process. Everything here runs off the run loop on
// an immutable compiled process, at design time.

// CandidateEntryPoint is one element a message can reach, offered for publication.
type CandidateEntryPoint struct {
	ElementID      string  `json:"elementId"`
	ElementName    string  `json:"elementName,omitempty"`
	Kind           string  `json:"kind"`
	Starts         bool    `json:"starts"`
	MessageName    string  `json:"messageName"`
	CorrelationKey string  `json:"correlationKey,omitempty"`
	Payload        Payload `json:"payload"`
}

// CandidateOutcome is one root-scope end event: a terminal outcome an instance can
// reach. Publishing names every one of them, because a contract that lists some
// outcomes would tell a consumer an instance cannot end in the others.
type CandidateOutcome struct {
	ElementID   string `json:"elementId"`
	ElementName string `json:"elementName,omitempty"`
	Kind        string `json:"kind"`
}

// CandidateReturns are the codes that can leave an instance uncaught, and the
// messages it throws — from which the publisher picks the ones that are replies.
type CandidateReturns struct {
	Errors      []string `json:"errors"`
	Escalations []string `json:"escalations"`
	// ThrownMessages are the names the definition's message throw and end events
	// send. Which of them are replies a consumer may receive is the publisher's to
	// say: a throw to another process of the same domain is no part of a contract.
	ThrownMessages []string `json:"thrownMessages"`
}

// Candidates is everything a definition offers for publication.
type Candidates struct {
	EntryPoints []CandidateEntryPoint `json:"entryPoints"`
	Outcomes    []CandidateOutcome    `json:"outcomes"`
	Returns     CandidateReturns      `json:"returns"`
}

// Derive reads the candidates out of a compiled process. names maps a BPMN element
// id to its name, for display only; it may be nil.
func Derive(cp *compiler.CompiledProcess, names map[string]string) Candidates {
	out := Candidates{
		EntryPoints: []CandidateEntryPoint{},
		Outcomes:    []CandidateOutcome{},
		Returns:     CandidateReturns{Errors: []string{}, Escalations: []string{}, ThrownMessages: []string{}},
	}
	if cp == nil {
		return out
	}
	errs, escs, thrown := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := 0; i < cp.NodeCount(); i++ {
		id := int32(i)
		n := cp.Node(id)
		bpmnID := cp.ElementBpmnId(id)
		if e, ok := entryPointOf(cp, id, n); ok {
			e.ElementID, e.ElementName = bpmnID, names[bpmnID]
			out.EntryPoints = append(out.EntryPoints, e)
		}
		switch n.Type {
		case compiler.TypeErrorEndEvent:
			if code := cp.ErrorEnd(n.Detail).ErrorCode; code != "" && !caught(cp, id, compiler.BoundaryError, code) {
				errs[code] = true
			}
		case compiler.TypeEscalationThrowEvent, compiler.TypeEscalationEndEvent:
			if code := cp.Escalation(n.Detail).EscalationCode; code != "" && !caught(cp, id, compiler.BoundaryEscalation, code) {
				escs[code] = true
			}
		case compiler.TypeMessageThrowEvent, compiler.TypeMessageEndEvent:
			if name := cp.MessageThrow(n.Detail).MessageName; name != "" {
				thrown[name] = true
			}
		}
		if n.FlowScope == -1 {
			if kind := outcomeKind(n.Type); kind != "" {
				out.Outcomes = append(out.Outcomes, CandidateOutcome{ElementID: bpmnID, ElementName: names[bpmnID], Kind: kind})
			}
		}
	}
	out.Returns.Errors = sortedKeys(errs)
	out.Returns.Escalations = sortedKeys(escs)
	out.Returns.ThrownMessages = sortedKeys(thrown)
	return out
}

// entryPointOf answers the entry point an element is, if it is one: the five places
// a message can reach, as [compiler.CompiledProcess.MessageReceipts] reads them. A
// message start nested in an event subprocess is that subprocess's trigger, and is
// offered once, as the subprocess.
func entryPointOf(cp *compiler.CompiledProcess, id int32, n *compiler.CompiledNode) (CandidateEntryPoint, bool) {
	var e CandidateEntryPoint
	var md *compiler.MessageDetail
	payloadAt := id
	switch {
	case n.EventSub >= 0:
		d := cp.EventSubProcess(n.EventSub)
		if d.Kind != compiler.BoundaryMessage {
			return e, false
		}
		e.Kind, e.MessageName, e.CorrelationKey = KindEventSubprocess, d.MessageName, source(d.CorrelationKey)
		payloadAt = d.StartNode
	case n.Type == compiler.TypeMessageStartEvent:
		if n.FlowScope != -1 {
			return e, false
		}
		md, e.Kind, e.Starts = cp.MessageStart(n.Detail), KindMessageStart, true
	case n.Type == compiler.TypeMessageCatchEvent:
		md, e.Kind = cp.MessageCatch(n.Detail), KindIntermediateCatch
	case n.Type == compiler.TypeReceiveTask:
		md, e.Kind = cp.ReceiveTask(n.Detail), KindReceiveTask
	case n.Type == compiler.TypeBoundaryEvent:
		d := cp.BoundaryEvent(n.Detail)
		if d.Kind != compiler.BoundaryMessage {
			return e, false
		}
		e.Kind, e.MessageName, e.CorrelationKey = KindBoundary, d.MessageName, source(d.CorrelationKey)
	default:
		return e, false
	}
	if md != nil {
		e.MessageName, e.CorrelationKey = md.MessageName, source(md.CorrelationKey)
	}
	if e.MessageName == "" {
		return e, false
	}
	e.Payload = payloadOf(cp, payloadAt)
	return e, true
}

// payloadOf reads what the element states about the message it takes: a data object
// typed against the information model first, then the names its output mappings
// read, and otherwise nothing — said as untyped rather than invented.
func payloadOf(cp *compiler.CompiledProcess, id int32) Payload {
	if id < 0 {
		return Payload{Basis: BasisUntyped}
	}
	types := map[string]string{}
	for _, do := range cp.DataObjects() {
		if do.ItemType >= 0 {
			types[cp.Intern(do.Name)] = cp.Intern(do.ItemType)
		}
	}
	for _, a := range cp.DataOutputAssociations(id) {
		if class := types[cp.Intern(a.DataObject)]; class != "" {
			return Payload{Basis: BasisInformationModel, Class: class}
		}
	}
	fields := map[string]bool{}
	for _, m := range cp.IOOutputs(id) {
		if m.Source == nil {
			continue
		}
		for _, in := range m.Source.Inputs() {
			fields[in] = true
		}
	}
	if len(fields) > 0 {
		return Payload{Basis: BasisFields, Fields: sortedKeys(fields)}
	}
	return Payload{Basis: BasisUntyped}
}

// caught reports whether an error or escalation raised at node is caught inside the
// process: by an event subprocess or a boundary of any scope enclosing it, matched
// by code or by a catch-all. What is not caught can leave the instance, which is
// what a consumer has to model a boundary event for.
func caught(cp *compiler.CompiledProcess, node int32, kind compiler.BoundaryEventKind, code string) bool {
	matches := func(k compiler.BoundaryEventKind, errCode, escCode string) bool {
		if k != kind {
			return false
		}
		c := errCode
		if kind == compiler.BoundaryEscalation {
			c = escCode
		}
		return c == "" || c == code
	}
	scope := cp.Node(node).FlowScope
	for {
		handlers := cp.RootEventSubprocesses()
		if scope >= 0 {
			handlers = cp.EventSubprocesses(scope)
		}
		for _, h := range handlers {
			hn := cp.Node(h)
			if hn.EventSub < 0 || h == node {
				continue
			}
			if d := cp.EventSubProcess(hn.EventSub); matches(d.Kind, d.ErrorCode, d.EscalationCode) {
				return true
			}
		}
		if scope < 0 {
			return false
		}
		for _, b := range cp.BoundaryEvents(scope) {
			if d := cp.BoundaryEvent(cp.Node(b).Detail); matches(d.Kind, d.ErrorCode, d.EscalationCode) {
				return true
			}
		}
		scope = cp.Node(scope).FlowScope
	}
}

func outcomeKind(t compiler.BpmnType) string {
	switch t {
	case compiler.TypeEndEvent:
		return OutcomeEnd
	case compiler.TypeErrorEndEvent:
		return OutcomeError
	case compiler.TypeEscalationEndEvent:
		return OutcomeEscalation
	case compiler.TypeMessageEndEvent:
		return OutcomeMessage
	case compiler.TypeSignalEndEvent:
		return OutcomeSignal
	case compiler.TypeTerminateEndEvent:
		return OutcomeTerminate
	case compiler.TypeCompensationEndEvent:
		return OutcomeCompensation
	}
	return ""
}

func source(c *expr.Compiled) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Source())
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Selection is what a publisher chooses: which elements become which entry points,
// what every terminal outcome is called, and which thrown messages are replies.
type Selection struct {
	EntryPoints []Pick   `json:"entryPoints"`
	Outcomes    []Pick   `json:"outcomes"`
	Replies     []string `json:"replies"`
}

// Pick names one element (or, for an outcome, several end events sharing it).
type Pick struct {
	Name       string   `json:"name"`
	ElementID  string   `json:"elementId,omitempty"`
	ElementIDs []string `json:"elementIds,omitempty"`
}

func (p Pick) elements() []string {
	ids := append([]string(nil), p.ElementIDs...)
	if p.ElementID != "" {
		ids = append(ids, p.ElementID)
	}
	return ids
}

// Realize turns a selection over a definition's candidates into the contract it
// publishes and the binding that realizes it. Problems are the reasons it cannot,
// in words a publisher can act on; when there are any, the contract is not usable.
func Realize(c Candidates, processID string, definitionKey uint64, version int32, sel Selection) (Contract, Binding, []string) {
	var problems []string
	contract := Contract{}
	binding := Binding{ProcessID: processID, DefinitionKey: definitionKey, DefinitionVersion: version,
		EntryPoints: []ElementMap{}, Outcomes: []ElementMap{}}

	entries := map[string]CandidateEntryPoint{}
	for _, e := range c.EntryPoints {
		entries[e.ElementID] = e
	}
	if len(sel.EntryPoints) == 0 {
		problems = append(problems, "an interface publishes at least one entry point")
	}
	seenName, seenElement := map[string]bool{}, map[string]bool{}
	for _, p := range sel.EntryPoints {
		ids := p.elements()
		switch {
		case !ValidName(p.Name):
			problems = append(problems, "entry point name "+quote(p.Name)+" is not usable: a letter, then letters, digits, '_', '.' or '-'")
			continue
		case seenName[p.Name]:
			problems = append(problems, "entry point name "+p.Name+" is used twice")
			continue
		case len(ids) != 1:
			problems = append(problems, "entry point "+p.Name+" names exactly one element")
			continue
		}
		seenName[p.Name] = true
		cand, ok := entries[ids[0]]
		switch {
		case !ok:
			problems = append(problems, "entry point "+p.Name+": element "+quote(ids[0])+" receives no message in this definition")
			continue
		case seenElement[ids[0]]:
			problems = append(problems, "element "+ids[0]+" is published under two entry points")
			continue
		}
		seenElement[ids[0]] = true
		contract.EntryPoints = append(contract.EntryPoints, EntryPoint{
			Name: p.Name, Kind: cand.Kind, Starts: cand.Starts, CorrelationKey: cand.CorrelationKey,
			Payload: Payload{Basis: cand.Payload.Basis, Class: cand.Payload.Class, Fields: append([]string(nil), cand.Payload.Fields...)},
		})
		binding.EntryPoints = append(binding.EntryPoints, ElementMap{Name: p.Name, ElementIDs: []string{cand.ElementID}, MessageName: cand.MessageName})
	}

	ends := map[string]CandidateOutcome{}
	for _, o := range c.Outcomes {
		ends[o.ElementID] = o
	}
	named := map[string]bool{}
	outcomeNames := map[string]bool{}
	for _, p := range sel.Outcomes {
		ids := p.elements()
		switch {
		case !ValidName(p.Name):
			problems = append(problems, "outcome name "+quote(p.Name)+" is not usable: a letter, then letters, digits, '_', '.' or '-'")
			continue
		case outcomeNames[p.Name]:
			problems = append(problems, "outcome name "+p.Name+" is used twice; name all its end events in one entry")
			continue
		case len(ids) == 0:
			problems = append(problems, "outcome "+p.Name+" names no end event")
			continue
		}
		outcomeNames[p.Name] = true
		kind, ok := "", true
		for _, id := range ids {
			o, found := ends[id]
			switch {
			case !found:
				problems = append(problems, "outcome "+p.Name+": "+quote(id)+" is no end event of the process's root scope")
				ok = false
			case named[id]:
				problems = append(problems, "end event "+id+" is named by two outcomes")
				ok = false
			case kind != "" && kind != o.Kind:
				problems = append(problems, "outcome "+p.Name+" joins end events of different kinds")
				ok = false
			default:
				kind = o.Kind
			}
			named[id] = true
		}
		if !ok {
			continue
		}
		sorted := append([]string(nil), ids...)
		sort.Strings(sorted)
		contract.Outcomes = append(contract.Outcomes, Outcome{Name: p.Name, Kind: kind})
		binding.Outcomes = append(binding.Outcomes, ElementMap{Name: p.Name, ElementIDs: sorted})
	}
	var unnamed []string
	for _, o := range c.Outcomes {
		if !named[o.ElementID] {
			unnamed = append(unnamed, o.ElementID)
		}
	}
	if len(unnamed) > 0 {
		problems = append(problems, "every terminal outcome is named, or the contract would say an instance cannot end there; unnamed: "+strings.Join(unnamed, ", "))
	}

	thrown := map[string]bool{}
	for _, m := range c.Returns.ThrownMessages {
		thrown[m] = true
	}
	replies := map[string]bool{}
	for _, m := range sel.Replies {
		switch {
		case !thrown[m]:
			problems = append(problems, "reply "+quote(m)+" is no message this definition throws")
		default:
			replies[m] = true
		}
	}
	contract.Returns = Returns{
		Errors:      append([]string{}, c.Returns.Errors...),
		Escalations: append([]string{}, c.Returns.Escalations...),
		Messages:    sortedKeys(replies),
	}
	contract.canonical()
	sort.Slice(binding.EntryPoints, func(i, j int) bool { return binding.EntryPoints[i].Name < binding.EntryPoints[j].Name })
	sort.Slice(binding.Outcomes, func(i, j int) bool { return binding.Outcomes[i].Name < binding.Outcomes[j].Name })
	return contract, binding, problems
}

// SelectionOf answers the selection a binding made, so a rebinding can reuse the
// element ids a refactoring did not touch.
func SelectionOf(b Binding, replies []string) Selection {
	sel := Selection{Replies: append([]string(nil), replies...)}
	for _, m := range b.EntryPoints {
		sel.EntryPoints = append(sel.EntryPoints, Pick{Name: m.Name, ElementIDs: append([]string(nil), m.ElementIDs...)})
	}
	for _, m := range b.Outcomes {
		sel.Outcomes = append(sel.Outcomes, Pick{Name: m.Name, ElementIDs: append([]string(nil), m.ElementIDs...)})
	}
	return sel
}

func quote(s string) string { return "\"" + s + "\"" }
