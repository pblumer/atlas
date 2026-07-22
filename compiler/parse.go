package compiler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// defaultRetries is used when a service task's task definition omits retries.
const defaultRetries = 3

// Parse reads a BPMN 2.0 XML model and compiles the first <process> into an
// immutable CompiledProcess keyed by key at the given version. It is the front
// end to the linearizer (compiler.md stages 1–2 and 6): it parses the XML,
// resolves string element ids to integer indices, and pours the result into the
// shared Builder. Validation beyond reference integrity (reachability, gateway
// coverage) is a later stage.
//
// Service-task job types come from the Zeebe task-definition extension element
// (<zeebe:taskDefinition type="..." retries="..."/>), the de-facto standard for
// executable BPMN.
func Parse(key uint64, version int32, r io.Reader) (*CompiledProcess, error) {
	var defs xmlDefinitions
	if err := xml.NewDecoder(r).Decode(&defs); err != nil {
		return nil, fmt.Errorf("compiler: parse BPMN: %w", err)
	}
	if len(defs.Processes) == 0 {
		return nil, fmt.Errorf("compiler: no <process> element in definitions")
	}
	proc := defs.Processes[0]

	// Messages are declared at the <definitions> level and referenced by a
	// message event's <messageEventDefinition messageRef="…">. Index them by id so
	// catch/throw events can resolve their name and correlation-key expression.
	messages := make(map[string]xmlMessage, len(defs.Messages))
	for _, m := range defs.Messages {
		if m.Id != "" {
			messages[m.Id] = m
		}
	}
	// resolveMessage turns a messageRef into the message name and its compiled
	// correlation-key expression. An empty correlation key compiles to nil, which
	// evaluates to "" — matching only publishes with an empty key.
	resolveMessage := func(ownerId, messageRef string) (string, *expr.Compiled, error) {
		m, ok := messages[messageRef]
		if !ok {
			return "", nil, fmt.Errorf("compiler: message event %q references unknown message %q", ownerId, messageRef)
		}
		if m.Name == "" {
			return "", nil, fmt.Errorf("compiler: message %q referenced by %q has no name", messageRef, ownerId)
		}
		var keyExpr *expr.Compiled
		if text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.Subscription.CorrelationKey), "=")); text != "" {
			ce, err := expr.CompileAuto(text)
			if err != nil {
				return "", nil, fmt.Errorf("compiler: message %q correlationKey: %w", messageRef, err)
			}
			keyExpr = ce
		}
		return m.Name, keyExpr, nil
	}

	b := NewBuilder(key, proc.Id, version)
	ids := make(map[string]int32, len(proc.StartEvents)+len(proc.ServiceTasks)+len(proc.EndEvents))
	register := func(id string, nodeID int32) error {
		if id == "" {
			return fmt.Errorf("compiler: element with empty id")
		}
		if _, dup := ids[id]; dup {
			return fmt.Errorf("compiler: duplicate element id %q", id)
		}
		ids[id] = nodeID
		b.SetElementBpmnId(nodeID, id) // retain for the live diagram overlay
		return nil
	}

	for _, s := range proc.StartEvents {
		if err := register(s.Id, b.AddStartEvent()); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ServiceTasks {
		if st.TaskDefinition.Type == "" {
			return nil, fmt.Errorf("compiler: service task %q has no task definition type", st.Id)
		}
		retries := int32(defaultRetries)
		if r := st.TaskDefinition.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: service task %q has invalid retries %q: %w", st.Id, r, err)
			}
			retries = int32(n)
		}
		if err := register(st.Id, b.AddServiceTask(st.TaskDefinition.Type, retries)); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ScriptTasks {
		text := strings.TrimSpace(st.Script.Expression)
		text = strings.TrimPrefix(text, "=") // Zeebe marks expressions with a leading '='
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("compiler: script task %q has no expression", st.Id)
		}
		if st.Script.ResultVariable == "" {
			return nil, fmt.Errorf("compiler: script task %q has no result variable", st.Id)
		}
		// FEEL is compiled once, at deploy time (ADR-0008/0015). CompileAuto
		// discovers the process variables the expression reads; a syntax or type
		// error fails here — i.e. fails deploy.
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: script task %q: %w", st.Id, err)
		}
		if err := register(st.Id, b.AddScriptTask(e, st.Script.ResultVariable)); err != nil {
			return nil, err
		}
	}
	for _, brt := range proc.BusinessRuleTasks {
		if brt.CalledDecision.DecisionId == "" {
			return nil, fmt.Errorf("compiler: business rule task %q has no calledDecision decisionId", brt.Id)
		}
		retries := int32(defaultRetries)
		if r := brt.CalledDecision.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: business rule task %q has invalid retries %q: %w", brt.Id, r, err)
			}
			retries = int32(n)
		}
		inputs, err := decisionInputs(brt.Inputs)
		if err != nil {
			return nil, fmt.Errorf("compiler: business rule task %q: %w", brt.Id, err)
		}
		node, err := b.AddBusinessRuleTask(brt.CalledDecision.DecisionId, inputs, retries)
		if err != nil {
			return nil, err
		}
		if err := register(brt.Id, node); err != nil {
			return nil, err
		}
	}
	for _, g := range proc.ExclusiveGateways {
		if err := register(g.Id, b.AddExclusiveGateway()); err != nil {
			return nil, err
		}
	}
	for _, ev := range proc.IntermediateCatchEvents {
		switch {
		case ev.Timer != nil:
			text := strings.TrimSpace(ev.Timer.TimeDuration)
			text = strings.TrimSpace(strings.TrimPrefix(text, "=")) // tolerate a FEEL '=' prefix
			nanos, err := parseISO8601Duration(text)
			if err != nil {
				return nil, fmt.Errorf("compiler: intermediate catch event %q timer: %w", ev.Id, err)
			}
			if err := register(ev.Id, b.AddTimerCatchEvent(nanos)); err != nil {
				return nil, err
			}
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return nil, err
			}
			if err := register(ev.Id, b.AddMessageCatchEvent(name, keyExpr)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("compiler: intermediate catch event %q: only timer and message events are supported yet", ev.Id)
		}
	}
	for _, ev := range proc.IntermediateThrowEvents {
		if ev.Message == nil {
			return nil, fmt.Errorf("compiler: intermediate throw event %q: only message events are supported yet", ev.Id)
		}
		name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
		if err != nil {
			return nil, err
		}
		if err := register(ev.Id, b.AddMessageThrowEvent(name, keyExpr)); err != nil {
			return nil, err
		}
	}
	for _, e := range proc.EndEvents {
		if err := register(e.Id, b.AddEndEvent()); err != nil {
			return nil, err
		}
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	// Report an unsupported element with a clear message rather than letting it
	// surface later as a confusing "unknown targetRef" when a flow points at it.
	for _, u := range []struct {
		label string
		nodes []xmlNode
	}{
		{"task", proc.Tasks}, {"userTask", proc.UserTasks},
		{"sendTask", proc.SendTasks}, {"receiveTask", proc.ReceiveTasks},
		{"manualTask", proc.ManualTasks},
		{"parallelGateway", proc.ParallelGateways},
		{"inclusiveGateway", proc.InclusiveGateways},
	} {
		if len(u.nodes) > 0 {
			return nil, fmt.Errorf("compiler: element %q is a <%s>, which Atlas can't execute yet "+
				"(supported: start/end events, service tasks, script tasks, business rule tasks, exclusive gateways, "+
				"and timer/message intermediate events)", u.nodes[0].Id, u.label)
		}
	}

	// Connect flows, compiling any FEEL condition, and remember each BPMN flow id
	// so a gateway's default flow can be marked afterwards.
	flowIdx := make(map[string]int32, len(proc.Flows))
	for _, f := range proc.Flows {
		src, ok := ids[f.SourceRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown sourceRef %q", f.Id, f.SourceRef)
		}
		tgt, ok := ids[f.TargetRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown targetRef %q", f.Id, f.TargetRef)
		}
		fid := b.Connect(src, tgt)
		flowIdx[f.Id] = fid
		if cond := strings.TrimSpace(f.Condition); cond != "" {
			cond = strings.TrimSpace(strings.TrimPrefix(cond, "=")) // FEEL condition, '=' prefix per Zeebe
			ce, err := expr.CompileAuto(cond)
			if err != nil {
				return nil, fmt.Errorf("compiler: flow %q condition: %w", f.Id, err)
			}
			b.SetFlowCondition(fid, ce)
		}
	}
	// Mark each exclusive gateway's default flow.
	for _, g := range proc.ExclusiveGateways {
		if g.Default == "" {
			continue
		}
		fid, ok := flowIdx[g.Default]
		if !ok {
			return nil, fmt.Errorf("compiler: exclusive gateway %q default references unknown flow %q", g.Id, g.Default)
		}
		b.SetFlowDefault(fid)
	}

	return b.Build()
}

// BPMN XML is matched by element/attribute local name, so namespace prefixes
// (bpmn:, zeebe:) are handled transparently by encoding/xml.

type xmlDefinitions struct {
	Processes []xmlProcess `xml:"process"`
	Messages  []xmlMessage `xml:"message"`
}

// A top-level message declaration. Its Zeebe subscription carries the FEEL
// correlationKey expression shared by every catch/throw event that references it.
type xmlMessage struct {
	Id           string               `xml:"id,attr"`
	Name         string               `xml:"name,attr"`
	Subscription xmlZeebeSubscription `xml:"extensionElements>subscription"`
}

type xmlZeebeSubscription struct {
	CorrelationKey string `xml:"correlationKey,attr"`
}

type xmlMessageEventDefinition struct {
	MessageRef string `xml:"messageRef,attr"`
}

type xmlProcess struct {
	Id                string                `xml:"id,attr"`
	StartEvents       []xmlNode             `xml:"startEvent"`
	EndEvents         []xmlNode             `xml:"endEvent"`
	ServiceTasks      []xmlServiceTask      `xml:"serviceTask"`
	ScriptTasks       []xmlScriptTask       `xml:"scriptTask"`
	BusinessRuleTasks []xmlBusinessRuleTask `xml:"businessRuleTask"`
	ExclusiveGateways []xmlExclusiveGateway `xml:"exclusiveGateway"`

	IntermediateCatchEvents []xmlIntermediateCatchEvent `xml:"intermediateCatchEvent"`
	IntermediateThrowEvents []xmlIntermediateThrowEvent `xml:"intermediateThrowEvent"`

	Flows []xmlSequenceFlow `xml:"sequenceFlow"`

	// Captured only to give a clear "unsupported element" error (see Parse); none
	// of these are executable yet.
	Tasks             []xmlNode `xml:"task"`
	UserTasks         []xmlNode `xml:"userTask"`
	SendTasks         []xmlNode `xml:"sendTask"`
	ReceiveTasks      []xmlNode `xml:"receiveTask"`
	ManualTasks       []xmlNode `xml:"manualTask"`
	ParallelGateways  []xmlNode `xml:"parallelGateway"`
	InclusiveGateways []xmlNode `xml:"inclusiveGateway"`
}

// A data-based exclusive gateway; default names the flow taken when no outgoing
// condition matches.
type xmlExclusiveGateway struct {
	Id      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
}

// An intermediate catch event; the timer and message variants are executable.
// Each definition is a pointer so an absent one is detected as nil.
type xmlIntermediateCatchEvent struct {
	Id      string                     `xml:"id,attr"`
	Timer   *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

// An intermediate throw event; only the message variant is executable so far.
type xmlIntermediateThrowEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

type xmlTimerEventDefinition struct {
	TimeDuration string `xml:"timeDuration"` // ISO-8601 duration, e.g. PT30S
}

type xmlNode struct {
	Id string `xml:"id,attr"`
}

type xmlServiceTask struct {
	Id             string            `xml:"id,attr"`
	TaskDefinition xmlTaskDefinition `xml:"extensionElements>taskDefinition"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// Zeebe script tasks carry the FEEL expression and its result variable in a
// <zeebe:script> extension element.
type xmlScriptTask struct {
	Id     string         `xml:"id,attr"`
	Script xmlZeebeScript `xml:"extensionElements>script"`
}

type xmlZeebeScript struct {
	Expression     string `xml:"expression,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
}

// A business rule task references a DMN decision via the Zeebe calledDecision
// extension (<zeebe:calledDecision decisionId="..."/>). Static inputs — a
// stand-in until process variables land — are given as Atlas decisionInput
// extension elements (<atlas:decisionInput name="Season" value="Winter"/>);
// each value is parsed as JSON when it parses, else kept as a string, so numbers
// and booleans reach the decision with their FEEL types.
type xmlBusinessRuleTask struct {
	Id             string             `xml:"id,attr"`
	CalledDecision xmlCalledDecision  `xml:"extensionElements>calledDecision"`
	Inputs         []xmlDecisionInput `xml:"extensionElements>decisionInput"`
}

type xmlCalledDecision struct {
	DecisionId string `xml:"decisionId,attr"`
	Retries    string `xml:"retries,attr"`
}

type xmlDecisionInput struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// decisionInputs turns parsed <decisionInput> elements into a name→value map,
// parsing each value as JSON when possible so numbers and booleans keep their
// types (a plain string that is not valid JSON is used verbatim).
func decisionInputs(in []xmlDecisionInput) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	m := make(map[string]any, len(in))
	for _, di := range in {
		if di.Name == "" {
			return nil, fmt.Errorf("decisionInput with empty name")
		}
		if _, dup := m[di.Name]; dup {
			return nil, fmt.Errorf("duplicate decisionInput name %q", di.Name)
		}
		var v any
		if err := json.Unmarshal([]byte(di.Value), &v); err != nil {
			v = di.Value // not JSON: treat as a literal string
		}
		m[di.Name] = v
	}
	return m, nil
}

type xmlSequenceFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
	// Condition is the FEEL guard text from a <conditionExpression> child, if any.
	Condition string `xml:"conditionExpression"`
}
