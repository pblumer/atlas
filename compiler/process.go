// Package compiler turns a BPMN model into an immutable, integer-indexed
// CompiledProcess (ADR-0004). Element ids become array indices, topology lives
// in shared contiguous arrays, and per-type data lives in detail tables, so the
// runtime hot path is pointer arithmetic with no strings, maps, or locks
// (invariant I5).
//
// This is the minimal target structure plus a programmatic [Builder]. The XML
// parse/resolve/validate front end (compiler.md stages 1–5) is a later
// milestone; the linearized result here is the shape the engine consumes.
package compiler

// BpmnType is the kind of a BPMN element. It is stored in element-instance state
// (as uint8) for O(1) behavior dispatch.
type BpmnType uint8

const (
	TypeUnspecified BpmnType = iota
	TypeStartEvent
	TypeEndEvent
	TypeServiceTask

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 16
)

// NumBpmnTypes is the size a behavior dispatch table indexed by BpmnType needs.
const NumBpmnTypes = numBpmnTypes

func (t BpmnType) String() string {
	switch t {
	case TypeStartEvent:
		return "StartEvent"
	case TypeEndEvent:
		return "EndEvent"
	case TypeServiceTask:
		return "ServiceTask"
	default:
		return "Unspecified"
	}
}

// CompiledNode is one BPMN element. It stays small; type-specific data lives in
// detail tables referenced by Detail.
type CompiledNode struct {
	ElementId     int32 // == index in nodes[]
	Type          BpmnType
	OutgoingStart int32 // offset into outgoingFlows
	OutgoingCount int32
	FlowScope     int32 // ElementId of enclosing scope, -1 = process root
	Detail        int32 // index into the matching detail table, -1 if none
}

// CompiledFlow is a sequence flow between two nodes.
type CompiledFlow struct {
	Id     int32
	Source int32 // ElementId
	Target int32 // ElementId
	// Condition (compiled FEEL) arrives with the gateway milestone.
}

// ServiceTaskDetail is the per-service-task data a behavior needs at runtime.
type ServiceTaskDetail struct {
	JobType int32 // interned string → index
	Retries int32
}

// CompiledProcess is the immutable result of compiling one process definition.
// It is safe for concurrent reads without synchronization.
type CompiledProcess struct {
	Key           uint64 // ProcessDefinitionKey
	BpmnProcessId int32  // interned
	Version       int32

	nodes []CompiledNode
	flows []CompiledFlow

	outgoingFlows []int32 // shared topology: flow ids grouped by source node
	serviceTasks  []ServiceTaskDetail
	startEvents   []int32
	strings       []string // intern table (index → string), for debug/export
}

// Node returns the node with the given ElementId.
func (p *CompiledProcess) Node(id int32) *CompiledNode { return &p.nodes[id] }

// Flow returns the flow with the given id.
func (p *CompiledProcess) Flow(id int32) *CompiledFlow { return &p.flows[id] }

// Outgoing returns the flow ids leaving node id, as a slice into the shared
// topology array (no allocation).
func (p *CompiledProcess) Outgoing(id int32) []int32 {
	n := &p.nodes[id]
	return p.outgoingFlows[n.OutgoingStart : n.OutgoingStart+n.OutgoingCount]
}

// ServiceTask returns the detail at the given table index.
func (p *CompiledProcess) ServiceTask(detail int32) *ServiceTaskDetail {
	return &p.serviceTasks[detail]
}

// StartEvents returns the process's entry-point element ids.
func (p *CompiledProcess) StartEvents() []int32 { return p.startEvents }

// Intern returns the string for an interned index, or "" if out of range.
func (p *CompiledProcess) Intern(idx int32) string {
	if idx < 0 || int(idx) >= len(p.strings) {
		return ""
	}
	return p.strings[idx]
}
