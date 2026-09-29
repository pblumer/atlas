package compiler

// What publishing asks of a per-position lifecycle process
// (ADR-draft-a-product-lifecycle-may-run-as-one-instance-per-position).
//
// A product whose lifecycle runs as one instance per order position has its later
// operations delivered to that instance, so the catalogue has to know two things the
// model decides: which messages the process waits for, and under which key; and
// whether the process can circle forever without waiting for anything. Both are
// read from the compiled graph at publish, never at runtime (I5).

// MessageCatchPoint is one element that waits for a message: an intermediate message
// catch event, a receive task, or a message boundary event.
type MessageCatchPoint struct {
	// Element is the BPMN element id.
	Element string
	// MessageName is the message it waits for.
	MessageName string
	// Correlated is whether it declares a correlation key. A catch without one is
	// matched by a name-only publish, which is how a stray message would reach every
	// position of a product at once.
	Correlated bool
}

// MessageCatchPoints lists every element of the process that waits for a message, in
// element order.
func (p *CompiledProcess) MessageCatchPoints() []MessageCatchPoint {
	var out []MessageCatchPoint
	for id := range p.nodes {
		n := &p.nodes[id]
		var d *MessageDetail
		switch n.Type {
		case TypeMessageCatchEvent:
			d = p.MessageCatch(n.Detail)
		case TypeReceiveTask:
			d = p.ReceiveTask(n.Detail)
		case TypeBoundaryEvent:
			if b := p.BoundaryEvent(n.Detail); b.Kind == BoundaryMessage {
				d = &MessageDetail{MessageName: b.MessageName, CorrelationKey: b.CorrelationKey}
			}
		}
		if d == nil {
			continue
		}
		out = append(out, MessageCatchPoint{
			Element:     p.ElementBpmnId(int32(id)),
			MessageName: d.MessageName,
			Correlated:  d.CorrelationKey != nil,
		})
	}
	return out
}

// waitsForOutside reports whether a token reaching a node of this type stops until
// something outside the token moves it: a message, a person, a timer, a signal or a
// condition over data somebody else writes. A cycle through such a node advances
// once per outside event and cannot run away on its own.
func waitsForOutside(t BpmnType) bool {
	switch t {
	case TypeMessageCatchEvent, TypeReceiveTask, TypeUserTask, TypeTimerCatchEvent,
		TypeSignalCatchEvent, TypeConditionalCatchEvent:
		return true
	}
	return false
}

// WaitlessCycle returns the element ids of a cycle in the process that passes no
// element waiting for something outside the token, or nil when every cycle waits.
// ADR-0272 already stops such a cycle at runtime with an incident; a per-position
// lifecycle process is refused at publish instead, because its instance lives as
// long as the right it carries and an incident on it stops every later operation.
//
// The graph is the sequence flows, plus an edge from each activity to its boundary
// events and from each subprocess to its start events, so a cycle that leaves
// through a boundary or runs through a subprocess is seen.
func (p *CompiledProcess) WaitlessCycle() []string {
	n := len(p.nodes)
	next := make([][]int32, n)
	for i := range p.flows {
		f := &p.flows[i]
		next[f.Source] = append(next[f.Source], f.Target)
	}
	for id := range p.nodes {
		next[id] = append(next[id], p.BoundaryEvents(int32(id))...)
		next[id] = append(next[id], p.ScopeStartEvents(int32(id))...)
	}
	const (
		unseen = iota
		onPath
		done
	)
	color := make([]uint8, n)
	var path []int32
	var cycle []string
	var visit func(id int32) bool
	visit = func(id int32) bool {
		color[id] = onPath
		path = append(path, id)
		for _, to := range next[id] {
			if waitsForOutside(p.nodes[to].Type) {
				continue
			}
			switch color[to] {
			case onPath:
				for i := len(path) - 1; i >= 0; i-- {
					cycle = append([]string{p.ElementBpmnId(path[i])}, cycle...)
					if path[i] == to {
						break
					}
				}
				return true
			case unseen:
				if visit(to) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		color[id] = done
		return false
	}
	for id := range p.nodes {
		if color[id] == unseen && !waitsForOutside(p.nodes[id].Type) && visit(int32(id)) {
			return cycle
		}
	}
	return nil
}
