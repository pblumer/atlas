package compiler

import (
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/expr"
)

// DMNJobType is the reserved job type business rule tasks carry. The in-process
// DMN worker subscribes to it to pick up decisions for evaluation, the same way
// an external worker subscribes to a service task's job type.
const DMNJobType = "io.atlas.dmn"

// Builder constructs a CompiledProcess programmatically. It stands in for the
// XML parse/resolve/linearize pipeline until that front end exists: callers add
// nodes and flows, and Build linearizes them into the immutable form (assigning
// the shared topology array, detail tables, and start-event list).
type Builder struct {
	key           uint64
	bpmnProcessId string
	version       int32

	nodes             []CompiledNode
	flows             []CompiledFlow
	serviceTasks      []ServiceTaskDetail
	scriptTasks       []ScriptTaskDetail
	businessRuleTasks []BusinessRuleTaskDetail
	timerCatches      []TimerCatchDetail
	messageCatches    []MessageDetail
	messageThrows     []MessageDetail
	messageStarts     []MessageDetail
	elementIds        []int32 // interned source BPMN id per node, -1 if unset

	interner map[string]int32
	strings  []string
}

// NewBuilder starts a builder for the process definition identified by key.
func NewBuilder(key uint64, bpmnProcessId string, version int32) *Builder {
	return &Builder{
		key:           key,
		bpmnProcessId: bpmnProcessId,
		version:       version,
		interner:      map[string]int32{},
	}
}

func (b *Builder) intern(s string) int32 {
	if s == "" {
		return -1
	}
	if idx, ok := b.interner[s]; ok {
		return idx
	}
	idx := int32(len(b.strings))
	b.strings = append(b.strings, s)
	b.interner[s] = idx
	return idx
}

func (b *Builder) addNode(t BpmnType, detail int32) int32 {
	id := int32(len(b.nodes))
	b.nodes = append(b.nodes, CompiledNode{
		ElementId: id,
		Type:      t,
		FlowScope: -1, // process root; nested scopes arrive with subprocesses
		Detail:    detail,
	})
	b.elementIds = append(b.elementIds, -1) // kept in lockstep with nodes
	return id
}

// SetElementBpmnId records the source BPMN element id (e.g. "StartEvent_1") for a
// node so it can be mapped back for diagnostics and the live diagram overlay. It
// is optional: nodes without one report "" from CompiledProcess.ElementBpmnId.
func (b *Builder) SetElementBpmnId(nodeID int32, bpmnID string) {
	if b.validNode(nodeID) {
		b.elementIds[nodeID] = b.intern(bpmnID)
	}
}

// AddStartEvent adds a none start event and returns its element id.
func (b *Builder) AddStartEvent() int32 { return b.addNode(TypeStartEvent, -1) }

// AddMessageStartEvent adds a message start event and returns its element id. It
// is a process entry point like a none start event — at runtime it simply flows
// straight on — but the engine also registers it at deploy time so a correlating
// message (a throw event or an API publish of messageName) instantiates a fresh
// process instance seeded with the message's payload (ADR-0025). correlationKey
// is compiled for future use; message-start matching is by name today.
func (b *Builder) AddMessageStartEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageStarts))
	b.messageStarts = append(b.messageStarts, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageStartEvent, detail)
}

// AddEndEvent adds a none end event and returns its element id.
func (b *Builder) AddEndEvent() int32 { return b.addNode(TypeEndEvent, -1) }

// AddServiceTask adds a service task with the given job type and retries and
// returns its element id.
func (b *Builder) AddServiceTask(jobType string, retries int32) int32 {
	detail := int32(len(b.serviceTasks))
	b.serviceTasks = append(b.serviceTasks, ServiceTaskDetail{
		JobType: b.intern(jobType),
		Retries: retries,
	})
	return b.addNode(TypeServiceTask, detail)
}

// AddScriptTask adds a script task that evaluates the given compiled FEEL
// expression and writes the result to resultVar. Returns its element id.
func (b *Builder) AddScriptTask(e *expr.Compiled, resultVar string) int32 {
	detail := int32(len(b.scriptTasks))
	b.scriptTasks = append(b.scriptTasks, ScriptTaskDetail{Expr: e, ResultVar: resultVar})
	return b.addNode(TypeScriptTask, detail)
}

// AddBusinessRuleTask adds a business rule task that evaluates the named DMN
// decision with the given static input context, and returns its element id. The
// inputs map is JSON-encoded and interned at deploy time (never on the hot path,
// invariant I5); a nil or empty map records no inputs. It returns an error if the
// inputs cannot be encoded.
func (b *Builder) AddBusinessRuleTask(decisionId string, inputs map[string]any, retries int32) (int32, error) {
	inputsIdx := int32(-1)
	if len(inputs) > 0 {
		encoded, err := json.Marshal(inputs)
		if err != nil {
			return -1, fmt.Errorf("compiler: business rule task %q inputs: %w", decisionId, err)
		}
		inputsIdx = b.intern(string(encoded))
	}
	detail := int32(len(b.businessRuleTasks))
	b.businessRuleTasks = append(b.businessRuleTasks, BusinessRuleTaskDetail{
		JobType:    b.intern(DMNJobType),
		DecisionId: b.intern(decisionId),
		Inputs:     inputsIdx,
		Retries:    retries,
	})
	return b.addNode(TypeBusinessRuleTask, detail), nil
}

// AddTask adds an undefined/manual task — one with no execution semantics — and
// returns its element id. It carries no detail and simply passes the token
// straight through, so a model can be drafted and its routing tested before its
// tasks are given real implementations.
func (b *Builder) AddTask() int32 { return b.addNode(TypeTask, -1) }

// AddParallelGateway adds a parallel (AND) gateway and returns its element id. It
// forks a token onto every outgoing flow and joins by waiting until a token has
// arrived on each of its incoming flows.
func (b *Builder) AddParallelGateway() int32 { return b.addNode(TypeParallelGateway, -1) }

// AddExclusiveGateway adds a data-based exclusive gateway (XOR split) and returns
// its element id. Its outgoing flows carry the conditions; see SetFlowCondition
// and SetFlowDefault.
func (b *Builder) AddExclusiveGateway() int32 { return b.addNode(TypeExclusiveGateway, -1) }

// AddTimerCatchEvent adds an intermediate timer catch event that waits the given
// fixed duration (nanoseconds) before continuing, and returns its element id.
func (b *Builder) AddTimerCatchEvent(durationNanos int64) int32 {
	detail := int32(len(b.timerCatches))
	b.timerCatches = append(b.timerCatches, TimerCatchDetail{DurationNanos: durationNanos})
	return b.addNode(TypeTimerCatchEvent, detail)
}

// AddMessageCatchEvent adds an intermediate message catch event that, on
// activation, subscribes to the named message with a correlation key produced by
// the given compiled FEEL expression (evaluated over the instance's variables),
// then waits until a matching message is correlated. Returns its element id.
func (b *Builder) AddMessageCatchEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageCatches))
	b.messageCatches = append(b.messageCatches, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageCatchEvent, detail)
}

// AddMessageThrowEvent adds an intermediate message throw event that, on
// activation, publishes the named message with a correlation key produced by the
// given compiled FEEL expression (evaluated over the throwing instance's
// variables), then completes. Returns its element id.
func (b *Builder) AddMessageThrowEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageThrows))
	b.messageThrows = append(b.messageThrows, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageThrowEvent, detail)
}

// Connect adds a sequence flow from source to target and returns its flow id, so
// the caller can attach a condition or mark it the default.
func (b *Builder) Connect(source, target int32) int32 {
	id := int32(len(b.flows))
	b.flows = append(b.flows, CompiledFlow{
		Id:     id,
		Source: source,
		Target: target,
	})
	return id
}

// SetFlowCondition attaches a compiled FEEL guard to a flow (an exclusive gateway
// takes the first flow whose condition is true).
func (b *Builder) SetFlowCondition(flowID int32, c *expr.Compiled) {
	if flowID >= 0 && int(flowID) < len(b.flows) {
		b.flows[flowID].Condition = c
	}
}

// SetFlowDefault marks a flow as its gateway's default (taken when no condition matches).
func (b *Builder) SetFlowDefault(flowID int32) {
	if flowID >= 0 && int(flowID) < len(b.flows) {
		b.flows[flowID].Default = true
	}
}

// Build linearizes the accumulated nodes and flows into an immutable
// CompiledProcess. It returns an error if a flow references an unknown node.
func (b *Builder) Build() (*CompiledProcess, error) {
	for _, f := range b.flows {
		if !b.validNode(f.Source) || !b.validNode(f.Target) {
			return nil, fmt.Errorf("compiler: flow %d references unknown node", f.Id)
		}
	}

	// Group outgoing flow ids by source node into one shared array.
	var outgoing []int32
	for i := range b.nodes {
		n := &b.nodes[i]
		n.OutgoingStart = int32(len(outgoing))
		for _, f := range b.flows {
			if f.Source == n.ElementId {
				outgoing = append(outgoing, f.Id)
			}
		}
		n.OutgoingCount = int32(len(outgoing)) - n.OutgoingStart
	}

	// Count incoming flows per node, so a parallel join knows how many tokens to
	// wait for.
	for _, f := range b.flows {
		b.nodes[f.Target].IncomingCount++
	}

	var startEvents []int32
	for i := range b.nodes {
		if isStartEvent(b.nodes[i].Type) {
			startEvents = append(startEvents, b.nodes[i].ElementId)
		}
	}

	return &CompiledProcess{
		Key:               b.key,
		BpmnProcessId:     b.intern(b.bpmnProcessId),
		Version:           b.version,
		nodes:             b.nodes,
		flows:             b.flows,
		outgoingFlows:     outgoing,
		serviceTasks:      b.serviceTasks,
		scriptTasks:       b.scriptTasks,
		businessRuleTasks: b.businessRuleTasks,
		timerCatches:      b.timerCatches,
		messageCatches:    b.messageCatches,
		messageThrows:     b.messageThrows,
		messageStarts:     b.messageStarts,
		startEvents:       startEvents,
		elementIds:        b.elementIds,
		strings:           b.strings,
	}, nil
}

func (b *Builder) validNode(id int32) bool {
	return id >= 0 && int(id) < len(b.nodes)
}

// hasStartEvent reports whether any start event has been added.
func (b *Builder) hasStartEvent() bool {
	for i := range b.nodes {
		if isStartEvent(b.nodes[i].Type) {
			return true
		}
	}
	return false
}

// isStartEvent reports whether a node type is a process entry point. A message
// start event is one too: a correlating message instantiates the process, and a
// plain create then activates it like a none start (ADR-0025).
func isStartEvent(t BpmnType) bool {
	return t == TypeStartEvent || t == TypeMessageStartEvent
}
