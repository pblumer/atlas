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

// BusinessRule configures a business rule task added via AddBusinessRuleTask.
type BusinessRule struct {
	// DecisionId names the DMN decision to evaluate (required).
	DecisionId string
	// StaticInputs are constant inputs merged into the evaluation context.
	StaticInputs map[string]any
	// InputMappings binds a decision input name to a process variable name
	// (decision input → variable). Mappings win over static inputs of the same
	// name.
	InputMappings map[string]string
	// ResultVariable, if set, is the process variable the decision's outputs are
	// written back into.
	ResultVariable string
	// Retries is the job's retry budget.
	Retries int32
}

// AddBusinessRuleTask adds a business rule task from cfg and returns its element
// id. Static inputs are JSON-encoded and every name is interned at deploy time
// (never on the hot path, invariant I5). It returns an error if cfg has no
// decision id or its static inputs cannot be encoded.
func (b *Builder) AddBusinessRuleTask(cfg BusinessRule) (int32, error) {
	if cfg.DecisionId == "" {
		return -1, fmt.Errorf("compiler: business rule task has no decision id")
	}
	inputsIdx := int32(-1)
	if len(cfg.StaticInputs) > 0 {
		encoded, err := json.Marshal(cfg.StaticInputs)
		if err != nil {
			return -1, fmt.Errorf("compiler: business rule task %q inputs: %w", cfg.DecisionId, err)
		}
		inputsIdx = b.intern(string(encoded))
	}
	var mappings []VariableMapping
	for target, source := range cfg.InputMappings {
		mappings = append(mappings, VariableMapping{
			Target: b.intern(target),
			Source: b.intern(source),
		})
	}
	detail := int32(len(b.businessRuleTasks))
	b.businessRuleTasks = append(b.businessRuleTasks, BusinessRuleTaskDetail{
		JobType:        b.intern(DMNJobType),
		DecisionId:     b.intern(cfg.DecisionId),
		Inputs:         inputsIdx,
		ResultVariable: b.intern(cfg.ResultVariable),
		Retries:        cfg.Retries,
		InputMappings:  mappings,
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
