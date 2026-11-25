package interaction

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// A BPMN file read as a collaboration (ADR-0559, amendment of 2026-11-25): what each
// of its pools is, which message flows it draws between them, and how that drawing
// compares with what is deployed.
//
// No file is a collaboration by declaration, and no pool is flagged. A pool's kind is
// read from its content every time: modelled here when its process holds flow
// elements, a reference when it holds none and names a process deployed elsewhere, an
// external party otherwise. The reader is namespace-blind, as the engine is
// (ADR-0269): elements and attributes are matched by local name.

// Pool kinds.
const (
	// PoolModelled is a pool whose process is in the file and holds flow elements.
	PoolModelled = "modelled"
	// PoolReference is a pool without content that names a process deployed
	// elsewhere by atlas:processId.
	PoolReference = "reference"
	// PoolExternal is a pool with neither: an external party, or a pool somebody
	// dragged in and has not decided about yet.
	PoolExternal = "external"
)

// Pool is one participant of the file.
type Pool struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
	// ProcessID is the process the pool stands for: its own for a pool modelled here,
	// the one it names for a reference, empty for an external party.
	ProcessID string `json:"processId,omitempty"`
}

// Flow is one message flow, between the pools its ends lie in.
type Flow struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	// Message is the message name the flow carries: its messageRef's, else the one the
	// element it docks on sends or waits for. Empty when the drawing names none.
	Message string `json:"message,omitempty"`
	// Name is the flow's own caption.
	Name string `json:"name,omitempty"`
}

// Document is a BPMN file as a collaboration.
type Document struct {
	// Collaboration is whether the file is listed as a collaboration: it holds two or
	// more participants, the line ADR-0023's sibling pools draw.
	Collaboration bool
	Pools         []Pool
	Flows         []Flow
	// points is, per pool modelled here, the message events drawn in it.
	points map[string][]Point
}

type rawProcess struct {
	content bool
	points  []rawPoint
}

type rawPoint struct{ element, messageRef, role string }

// flowNode reports whether an element is something a token passes: the content that
// makes a pool "modelled here". Documentation, lanes and extension elements are not.
func flowNode(local string) bool {
	switch local {
	case "task", "subProcess", "callActivity", "transaction", "adHocSubProcess":
		return true
	}
	return strings.HasSuffix(local, "Event") || strings.HasSuffix(local, "Task") || strings.HasSuffix(local, "Gateway")
}

// messageRole is the role of the element a message event definition sits in.
func messageRole(parent string) string {
	switch parent {
	case "startEvent":
		return "start"
	case "boundaryEvent":
		return "boundary"
	case "intermediateThrowEvent", "endEvent":
		return "throw"
	}
	return "catch"
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// ParseDocument reads a BPMN file's participants, message flows and the message
// events of the processes modelled in it.
func ParseDocument(body []byte) (Document, error) {
	type frame struct{ name, id string }
	type rawPart struct{ id, name, processRef, processID string }
	type rawFlow struct{ id, name, source, target, messageRef string }
	var (
		stack    []frame
		parts    []rawPart
		flows    []rawFlow
		messages = map[string]string{}
		procs    = map[string]*rawProcess{}
		owner    = map[string]string{} // element id → the process it is drawn in
		current  string                // the process being read, "" outside one
	)
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Document{}, fmt.Errorf("interaction: read the model: %w", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			local, id := e.Name.Local, attr(e, "id")
			switch {
			case current != "":
				p := procs[current]
				if id != "" {
					owner[id] = current
				}
				if flowNode(local) {
					p.content = true
				}
				switch {
				case local == "messageEventDefinition" && len(stack) > 0:
					parent := stack[len(stack)-1]
					p.points = append(p.points, rawPoint{parent.id, attr(e, "messageRef"), messageRole(parent.name)})
				case local == "receiveTask":
					p.points = append(p.points, rawPoint{id, attr(e, "messageRef"), "catch"})
				case local == "sendTask":
					p.points = append(p.points, rawPoint{id, attr(e, "messageRef"), "throw"})
				}
			case local == "process" && len(stack) == 1:
				current = id
				procs[id] = &rawProcess{}
			case local == "message" && len(stack) == 1:
				messages[id] = attr(e, "name")
			case local == "participant":
				parts = append(parts, rawPart{id, attr(e, "name"), attr(e, "processRef"), attr(e, "processId")})
			case local == "messageFlow":
				flows = append(flows, rawFlow{id, attr(e, "name"), attr(e, "sourceRef"), attr(e, "targetRef"), attr(e, "messageRef")})
			}
			stack = append(stack, frame{local, id})
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if e.Name.Local == "process" && len(stack) == 1 {
				current = ""
			}
		}
	}

	d := Document{Collaboration: len(parts) >= 2, Pools: []Pool{}, Flows: []Flow{}, points: map[string][]Point{}}
	poolOfProcess := map[string]string{}
	isPool := map[string]bool{}
	elementMessage := map[string]Point{} // element id → what it sends or waits for
	for _, p := range parts {
		pool := Pool{ID: p.id, Name: p.name, Kind: PoolExternal}
		proc := procs[p.processRef]
		switch {
		case proc != nil && proc.content:
			pool.Kind, pool.ProcessID = PoolModelled, p.processRef
			for _, rp := range proc.points {
				name := messages[rp.messageRef]
				if name == "" || rp.element == "" {
					continue // a message event nobody gave a message yet
				}
				pt := Point{Element: rp.element, Message: name, Role: rp.role}
				d.points[p.id] = append(d.points[p.id], pt)
				elementMessage[rp.element] = pt
			}
		case p.processID != "":
			pool.Kind, pool.ProcessID = PoolReference, p.processID
		}
		if p.processRef != "" {
			poolOfProcess[p.processRef] = p.id
		}
		isPool[p.id] = true
		d.Pools = append(d.Pools, pool)
	}
	// An end of a flow is a pool or an element drawn in one.
	poolOf := func(ref string) string {
		if isPool[ref] {
			return ref
		}
		return poolOfProcess[owner[ref]]
	}
	for _, f := range flows {
		from, to := poolOf(f.source), poolOf(f.target)
		if from == "" || to == "" {
			continue // a flow whose end is nowhere in the file
		}
		fl := Flow{ID: f.id, From: from, To: to, Name: f.name, Message: messages[f.messageRef]}
		if fl.Message == "" {
			if pt, ok := elementMessage[f.source]; ok && pt.Sends() {
				fl.Message = pt.Message
			} else if pt, ok := elementMessage[f.target]; ok && !pt.Sends() {
				fl.Message = pt.Message
			}
		}
		d.Flows = append(d.Flows, fl)
	}
	return d, nil
}

// The findings (ADR-0559 §5). They are hints, not errors: each depends on other
// deployments, and the order of deploys is free.
const (
	// FindingParticipantUnresolved is a reference to a process with no deployed
	// version on this server.
	FindingParticipantUnresolved = "collaboration.participant-unresolved"
	// FindingFlowNotSent is a flow whose source is a white box that sends no message
	// of that name.
	FindingFlowNotSent = "collaboration.flow-not-sent"
	// FindingFlowNotReceived is a flow whose target is a white box that waits for no
	// message of that name.
	FindingFlowNotReceived = "collaboration.flow-not-received"
	// FindingFlowUnnamed is a flow that names no message, so it is not checked.
	FindingFlowUnnamed = "collaboration.flow-unnamed"
	// FindingUndrawnExchange is a name one white box sends and another waits for
	// that no flow between them shows.
	FindingUndrawnExchange = "collaboration.undrawn-exchange"
	// FindingReachesOutside is a flow's name that a deployed process outside the
	// collaboration also waits for: every match is delivered (ADR-0446).
	FindingReachesOutside = "collaboration.reaches-outside"
)

// Finding is one thing the drawing and the deployments disagree about, or that is
// worth knowing: its rule, the sentence that says it, and the pool or flow it is on.
//
// Message is the sentence in English. Subject, Other and MessageName are its parts — the
// pool it is about, the counterpart (a pool, a process id, or the processes outside),
// and the message name — so a client can word the finding in its reader's language.
type Finding struct {
	Rule        string `json:"rule"`
	Message     string `json:"message"`
	Pool        string `json:"pool,omitempty"`
	Flow        string `json:"flow,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Other       string `json:"other,omitempty"`
	MessageName string `json:"messageName,omitempty"`
}

// Resolved is the deployed version a reference stands for: the newest.
type Resolved struct {
	DefinitionKey uint64 `json:"definitionKey"`
	Version       int32  `json:"version"`
	Name          string `json:"name,omitempty"`
	Inactive      bool   `json:"inactive,omitempty"`
}

// PoolView is a pool with what its reference resolves to and, for a white box, the
// message names it sends and waits for: what a message flow at this pool can carry.
type PoolView struct {
	Pool
	Resolved *Resolved `json:"resolved,omitempty"`
	Sends    []string  `json:"sends,omitempty"`
	Receives []string  `json:"receives,omitempty"`
}

// Report is a file read as a collaboration and checked against the deployments.
type Report struct {
	Collaboration bool       `json:"collaboration"`
	Pools         []PoolView `json:"pools"`
	Flows         []Flow     `json:"flows"`
	Findings      []Finding  `json:"findings"`
}

// Check compares a document with the deployed definitions the caller may read. A
// pool modelled here is read from the drawing itself; a reference from the newest
// deployed version of the process it names; an external party is not read at all,
// because nothing is known of its inside.
func Check(d Document, defs []Definition) Report {
	r := Report{Collaboration: d.Collaboration, Pools: []PoolView{}, Flows: d.Flows, Findings: []Finding{}}
	if r.Flows == nil {
		r.Flows = []Flow{}
	}
	newest := map[string]Definition{}
	for _, def := range defs {
		if def.Newest {
			newest[def.ProcessID] = def
		}
	}
	var (
		points = map[string][]Point{} // per white box
		white  = map[string]bool{}
		bound  = map[string]bool{} // process ids the pools stand for
		label  = map[string]string{}
	)
	add := func(f Finding) { r.Findings = append(r.Findings, f) }
	for _, p := range d.Pools {
		view := PoolView{Pool: p}
		label[p.ID] = p.Name
		if p.Name == "" {
			label[p.ID] = p.ID
		}
		if p.ProcessID != "" {
			bound[p.ProcessID] = true
		}
		switch p.Kind {
		case PoolModelled:
			white[p.ID], points[p.ID] = true, d.points[p.ID]
		case PoolReference:
			def, ok := newest[p.ProcessID]
			if !ok {
				add(Finding{Rule: FindingParticipantUnresolved, Pool: p.ID, Subject: label[p.ID], Other: p.ProcessID,
					Message: fmt.Sprintf("%s names the process %s, which is not deployed on this server.", label[p.ID], p.ProcessID)})
				break
			}
			view.Resolved = &Resolved{DefinitionKey: def.Key, Version: def.Version, Name: def.Name, Inactive: def.Inactive}
			white[p.ID], points[p.ID] = true, def.Points
		}
		view.Sends, view.Receives = names(points[p.ID], true), names(points[p.ID], false)
		r.Pools = append(r.Pools, view)
	}
	has := func(pool, message string, sends bool) bool {
		for _, pt := range points[pool] {
			if pt.Message == message && pt.Sends() == sends {
				return true
			}
		}
		return false
	}

	drawn := map[exchangeKey]bool{}
	for _, f := range d.Flows {
		if f.Message == "" {
			add(Finding{Rule: FindingFlowUnnamed, Flow: f.ID, Subject: label[f.From], Other: label[f.To],
				Message: fmt.Sprintf("The flow from %s to %s names no message, so it is not checked.", label[f.From], label[f.To])})
			continue
		}
		drawn[exchangeKey{f.From, f.To, f.Message}] = true
		if white[f.From] && !has(f.From, f.Message, true) {
			add(Finding{Rule: FindingFlowNotSent, Flow: f.ID, Subject: label[f.From], MessageName: f.Message,
				Message: fmt.Sprintf("%s sends no message %s.", label[f.From], f.Message)})
		}
		if white[f.To] && !has(f.To, f.Message, false) {
			add(Finding{Rule: FindingFlowNotReceived, Flow: f.ID, Subject: label[f.To], MessageName: f.Message,
				Message: fmt.Sprintf("%s waits for no message %s.", label[f.To], f.Message)})
		}
		var outside []string
		for _, def := range newest {
			if bound[def.ProcessID] || def.Inactive {
				continue
			}
			for _, pt := range def.Points {
				if pt.Message == f.Message && !pt.Sends() {
					name := def.Name
					if name == "" {
						name = def.ProcessID
					}
					outside = append(outside, name)
					break
				}
			}
		}
		if len(outside) > 0 {
			sort.Strings(outside)
			add(Finding{Rule: FindingReachesOutside, Flow: f.ID, MessageName: f.Message, Other: strings.Join(outside, ", "),
				Message: fmt.Sprintf("%s is also received outside this collaboration: %s.", f.Message, strings.Join(outside, ", "))})
		}
	}

	// What two white boxes could exchange and no flow shows.
	for _, a := range d.Pools {
		for _, b := range d.Pools {
			if a.ID == b.ID || !white[a.ID] || !white[b.ID] {
				continue
			}
			seen := map[string]bool{}
			var names []string
			for _, pt := range points[a.ID] {
				if pt.Sends() && !seen[pt.Message] && has(b.ID, pt.Message, false) && !drawn[exchangeKey{a.ID, b.ID, pt.Message}] {
					seen[pt.Message] = true
					names = append(names, pt.Message)
				}
			}
			sort.Strings(names)
			for _, m := range names {
				add(Finding{Rule: FindingUndrawnExchange, Pool: a.ID, Subject: label[a.ID], Other: label[b.ID], MessageName: m,
					Message: fmt.Sprintf("%s sends %s and %s waits for it, but no flow shows it.", label[a.ID], m, label[b.ID])})
			}
		}
	}
	return r
}

// names is the message names of the points that send, or of those that wait, each
// once and in order.
func names(points []Point, sends bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, pt := range points {
		if pt.Sends() == sends && !seen[pt.Message] {
			seen[pt.Message] = true
			out = append(out, pt.Message)
		}
	}
	sort.Strings(out)
	return out
}
