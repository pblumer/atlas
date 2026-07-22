package compiler

import (
	"fmt"

	"github.com/pblumer/atlas/expr"
)

// Builder constructs a CompiledProcess programmatically. It stands in for the
// XML parse/resolve/linearize pipeline until that front end exists: callers add
// nodes and flows, and Build linearizes them into the immutable form (assigning
// the shared topology array, detail tables, and start-event list).
type Builder struct {
	key           uint64
	bpmnProcessId string
	version       int32

	nodes        []CompiledNode
	flows        []CompiledFlow
	serviceTasks []ServiceTaskDetail
	scriptTasks  []ScriptTaskDetail
	elementIds   []int32 // interned source BPMN id per node, -1 if unset

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

// AddScriptTask adds a script task that evaluates the given compiled FEEL
// expression and writes the result to resultVar. Returns its element id.
func (b *Builder) AddScriptTask(e *expr.Compiled, resultVar string) int32 {
	detail := int32(len(b.scriptTasks))
	b.scriptTasks = append(b.scriptTasks, ScriptTaskDetail{Expr: e, ResultVar: resultVar})
	return b.addNode(TypeScriptTask, detail)
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
		Key:           b.key,
		BpmnProcessId: b.intern(b.bpmnProcessId),
		Version:       b.version,
		nodes:         b.nodes,
		flows:         b.flows,
		outgoingFlows: outgoing,
		serviceTasks:  b.serviceTasks,
		scriptTasks:   b.scriptTasks,
		startEvents:   startEvents,
		elementIds:    b.elementIds,
		strings:       b.strings,
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
