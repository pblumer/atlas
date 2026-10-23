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
	// Interrupting is, for a boundary or an event subprocess, whether firing cancels
	// the work it guards; false for every other role.
	Interrupting bool
}

// Receives reports whether the element receives the signal rather than throwing it.
func (s SignalPoint) Receives() bool { return s.Role != SignalThrows }

// SignalPoints lists every element of the process that throws or receives a signal:
// what the event catalogue (ADR-0435) holds a system process to, how a server finds
// who listens to an event, and what the inventory of the installation's own signals
// is made of (ADR-0447).
func (p *CompiledProcess) SignalPoints() []SignalPoint {
	var out []SignalPoint
	add := func(id int, name string, role SignalRole, interrupting bool) {
		out = append(out, SignalPoint{Element: p.ElementBpmnId(int32(id)), SignalName: name, Role: role, Interrupting: interrupting})
	}
	for id := range p.nodes {
		n := &p.nodes[id]
		switch n.Type {
		case TypeSignalThrowEvent, TypeSignalEndEvent:
			add(id, p.SignalThrow(n.Detail).SignalName, SignalThrows, false)
		case TypeSignalStartEvent:
			// An event subprocess's signal start is reported with its subprocess below,
			// not as a start of the process.
			if n.FlowScope == -1 {
				add(id, p.SignalStart(n.Detail).SignalName, SignalStarts, false)
			}
		case TypeSignalCatchEvent:
			add(id, p.SignalCatch(n.Detail).SignalName, SignalCatches, false)
		case TypeBoundaryEvent:
			if b := p.BoundaryEvent(n.Detail); b.Kind == BoundarySignal {
				add(id, b.SignalName, SignalBoundary, b.Interrupting)
			}
		}
		if n.EventSub >= 0 {
			// Named by its start event, where the signal is chosen in the Modeler.
			if e := p.EventSubProcess(n.EventSub); e.Kind == BoundarySignal {
				add(int(e.StartNode), e.SignalName, SignalEventSubProcess, e.Interrupting)
			}
		}
	}
	return out
}

// MessageRole says what an element does with a message. The words are the signal
// roles' (SignalRole), so the inventories of both channels read alike (ADR-0446).
type MessageRole string

const (
	// MessageSends is an intermediate throw event, an end event or a send task that
	// sends the message; a send task naming a message compiles to a throw (ADR-0112).
	MessageSends MessageRole = "throw"
	// MessageStarts is a start event of the process that starts an instance on the message.
	MessageStarts MessageRole = "start"
	// MessageCatches is an intermediate catch event or a receive task that waits for it.
	MessageCatches MessageRole = "catch"
	// MessageBoundary is a boundary event that fires on it.
	MessageBoundary MessageRole = "boundary"
	// MessageEventSubProcess is an event subprocess the message starts, named by its
	// start event.
	MessageEventSubProcess MessageRole = "event-subprocess"
)

// MessagePoint is one element of a process that sends or receives a message: a
// message start, a catch event, a receive task, a message boundary, an event
// subprocess's message start, or a throw.
type MessagePoint struct {
	Element     string
	MessageName string
	Role        MessageRole
	// Interrupting is, for a boundary or an event subprocess, whether firing cancels
	// the work it guards; false for every other role.
	Interrupting bool
}

// Receives reports whether the element receives the message rather than sending it.
func (m MessagePoint) Receives() bool { return m.Role != MessageSends }

// MessageReceivers lists every element that receives a message, starts first. An
// event subprocess's message start is a receiver too: it is reported with its
// subprocess, named by its start event, as SignalPoints reports a signal one
// (ADR-0446 §1).
func (p *CompiledProcess) MessageReceivers() []MessagePoint {
	var out []MessagePoint
	for _, ms := range p.MessageStartEvents() {
		out = append(out, MessagePoint{Element: p.ElementBpmnId(ms.ElementId), MessageName: ms.MessageName, Role: MessageStarts})
	}
	for id := range p.nodes {
		n := &p.nodes[id]
		switch n.Type {
		case TypeMessageCatchEvent:
			out = append(out, MessagePoint{Element: p.ElementBpmnId(int32(id)), MessageName: p.MessageCatch(n.Detail).MessageName, Role: MessageCatches})
		case TypeReceiveTask:
			out = append(out, MessagePoint{Element: p.ElementBpmnId(int32(id)), MessageName: p.ReceiveTask(n.Detail).MessageName, Role: MessageCatches})
		case TypeBoundaryEvent:
			if b := p.BoundaryEvent(n.Detail); b.Kind == BoundaryMessage {
				out = append(out, MessagePoint{Element: p.ElementBpmnId(int32(id)), MessageName: b.MessageName, Role: MessageBoundary, Interrupting: b.Interrupting})
			}
		}
		if n.EventSub >= 0 {
			if e := p.EventSubProcess(n.EventSub); e.Kind == BoundaryMessage {
				out = append(out, MessagePoint{Element: p.ElementBpmnId(e.StartNode), MessageName: e.MessageName, Role: MessageEventSubProcess, Interrupting: e.Interrupting})
			}
		}
	}
	return out
}

// MessageSenders lists every element that sends a message: a message throw event, a
// message end event, and a send task that names a message, which compiles to a
// throw. All three share the throw detail table (ADR-0446 §1).
func (p *CompiledProcess) MessageSenders() []MessagePoint {
	var out []MessagePoint
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type == TypeMessageThrowEvent || n.Type == TypeMessageEndEvent {
			out = append(out, MessagePoint{Element: p.ElementBpmnId(int32(id)), MessageName: p.MessageThrow(n.Detail).MessageName, Role: MessageSends})
		}
	}
	return out
}
