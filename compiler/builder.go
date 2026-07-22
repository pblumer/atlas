package compiler

import (
	"encoding/json"
	"fmt"
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
	businessRuleTasks []BusinessRuleTaskDetail
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

// Connect adds a sequence flow from source to target.
func (b *Builder) Connect(source, target int32) {
	b.flows = append(b.flows, CompiledFlow{
		Id:     int32(len(b.flows)),
		Source: source,
		Target: target,
	})
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

	var startEvents []int32
	for i := range b.nodes {
		if b.nodes[i].Type == TypeStartEvent {
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
		businessRuleTasks: b.businessRuleTasks,
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
		if b.nodes[i].Type == TypeStartEvent {
			return true
		}
	}
	return false
}
