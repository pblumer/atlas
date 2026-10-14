package compiler

// SignalRole says what an element does with a signal.
type SignalRole string

const (
	// SignalThrows is an intermediate or end event that broadcasts the signal.
	SignalThrows SignalRole = "throw"
	// SignalStarts is a start event of the process that starts an instance on the signal.
	SignalStarts SignalRole = "start"
	// SignalCatches is an intermediate catch event that waits for it.
	SignalCatches SignalRole = "catch"
	// SignalBoundary is a boundary event that fires on it.
	SignalBoundary SignalRole = "boundary"
	// SignalEventSubProcess is an event subprocess the signal starts, named by its
	// start event.
	SignalEventSubProcess SignalRole = "event-subprocess"
)

// SignalPoint is one element of a process that throws or receives a signal.
type SignalPoint struct {
	Element    string
	SignalName string
	Role       SignalRole
}

// Receives reports whether the element receives the signal rather than throwing it.
func (s SignalPoint) Receives() bool { return s.Role != SignalThrows }

// SignalPoints lists every element of the process that throws or receives a signal:
// what the event catalogue (ADR-0435) holds a system process to, and how a server
// finds who listens to an event.
func (p *CompiledProcess) SignalPoints() []SignalPoint {
	var out []SignalPoint
	add := func(id int, name string, role SignalRole) {
		out = append(out, SignalPoint{Element: p.ElementBpmnId(int32(id)), SignalName: name, Role: role})
	}
	for id := range p.nodes {
		n := &p.nodes[id]
		switch n.Type {
		case TypeSignalThrowEvent, TypeSignalEndEvent:
			add(id, p.SignalThrow(n.Detail).SignalName, SignalThrows)
		case TypeSignalStartEvent:
			// An event subprocess's signal start is reported with its subprocess below,
			// not as a start of the process.
			if n.FlowScope == -1 {
				add(id, p.SignalStart(n.Detail).SignalName, SignalStarts)
			}
		case TypeSignalCatchEvent:
			add(id, p.SignalCatch(n.Detail).SignalName, SignalCatches)
		case TypeBoundaryEvent:
			if b := p.BoundaryEvent(n.Detail); b.Kind == BoundarySignal {
				add(id, b.SignalName, SignalBoundary)
			}
		}
		if n.EventSub >= 0 {
			// Named by its start event, where the signal is chosen in the Modeler.
			if e := p.EventSubProcess(n.EventSub); e.Kind == BoundarySignal {
				add(int(e.StartNode), e.SignalName, SignalEventSubProcess)
			}
		}
	}
	return out
}

// MessagePoint is one element of a process that receives a message: a message start,
// a catch event, a receive task or a message boundary.
type MessagePoint struct {
	Element     string
	MessageName string
	Start       bool
}

// MessageReceivers lists every element that receives a message, starts first.
func (p *CompiledProcess) MessageReceivers() []MessagePoint {
	var out []MessagePoint
	for _, ms := range p.MessageStartEvents() {
		out = append(out, MessagePoint{Element: p.ElementBpmnId(ms.ElementId), MessageName: ms.MessageName, Start: true})
	}
	for _, c := range p.MessageCatchPoints() {
		out = append(out, MessagePoint{Element: c.Element, MessageName: c.MessageName})
	}
	return out
}
