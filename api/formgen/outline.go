package formgen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Reading a process for its own account of itself.
//
// The generator's second source of material is the diagram (the first is what the author
// typed). Everything it takes from one is the modeller's own words — the process
// documentation, each step's `<bpmn:documentation>`, the names its mappings and
// conditions use for its data — which is the argument for reading the model at all
// rather than asking an author to retype it into a prompt: they already wrote it down,
// once, in the place the next reader will look.
//
// It is a tolerant walk and not a compile, deliberately. A draft under an author's hands
// is routinely not deployable — that is what a draft is (ADR-0021) — and needing a valid
// model to generate a form for it would withhold the feature exactly when it helps most.
// What cannot be read is left out; nothing here ever fails.

const (
	// maxOutlineElements bounds how many steps travel into a prompt. A 400-element
	// landscape would crowd out the author's own brief, which is the part that
	// actually says what the form is for.
	maxOutlineElements = 120
	// maxDocRunes bounds one piece of prose. Documentation is written for people and
	// occasionally runs to pages; the first paragraphs carry what a form needs.
	maxDocRunes = 600
	// maxOutlineVariables bounds the name list. Past this it stops being a vocabulary
	// and starts being a dump.
	maxOutlineVariables = 60
)

// nsBPMN is the BPMN 2.0 model namespace. Membership of it is how a step is told from
// diagram interchange, a Zeebe extension or an Atlas one — a whitelist, because the set
// of things that are *not* the process grows with every extension and a blacklist would
// have to be edited each time one appears.
const nsBPMN = "http://www.omg.org/spec/BPMN/20100524/MODEL"

// Element is one step as the outline carries it: what it is, what it is called, and what
// the modeller wrote about it.
type Element struct {
	// ID is the BPMN element id — what a form binding, a token overlay and an
	// incident all name it by, so it is what the author sees elsewhere too.
	ID string
	// Kind is the BPMN local name (userTask, startEvent, sequenceFlow …). It is the
	// vocabulary, unmapped: a model that knows BPMN reads it directly.
	Kind string
	// Name and Documentation are the modeller's own.
	Name          string
	Documentation string
	// Condition is a sequence flow's `<conditionExpression>`. It rides here because a
	// gateway's outgoing conditions are where a process says what it decides on, and
	// therefore which of a form's answers actually matter.
	Condition string
	// FormID is the form the step already binds (zeebe:formDefinition). Empty for a
	// step that binds none — and non-empty is worth saying out loud, because it means
	// a generation is a replacement rather than a first draft.
	FormID string
}

// Process is a BPMN model's own account of itself, as much of it as a form generator has
// any use for. The zero value is "nothing could be read", which every caller treats as
// "the author's prose is the whole brief" rather than as an error.
type Process struct {
	ID            string
	Name          string
	Documentation string
	Elements      []Element
	// Variables are the names the model already uses for its data, in document order
	// and deduplicated: input/output mapping targets, data objects, and result
	// variables. They matter because a form whose keys match them needs no mapping
	// afterwards, while one that invents `vacationDays` for a process that says
	// `urlaubstage` has made work for somebody.
	Variables []string
	// truncated records that the model had more steps than [maxOutlineElements]. The
	// description says so rather than simply stopping, which would read as a process
	// that ends there.
	truncated bool
}

// Element finds a step by its BPMN id.
func (p Process) Element(id string) (Element, bool) {
	for _, e := range p.Elements {
		if e.ID == id {
			return e, true
		}
	}
	return Element{}, false
}

// ReadProcess walks BPMN XML for what a form generator can use. It never fails: a
// truncated or malformed document yields whatever was readable before the break, because
// the alternative — refusing to generate a form for a draft that does not parse — would
// withhold the feature from the author who most needs it.
func ReadProcess(src []byte) Process {
	var p Process
	if len(src) == 0 {
		return p
	}
	d := xml.NewDecoder(bytes.NewReader(src))
	// A draft is authored by a browser toolkit and re-saved by hand often enough that
	// strictness buys nothing here: an unknown entity or an unbalanced tag should cost
	// the rest of the walk, not the part already read.
	d.Strict = false
	// open is the stack of outline elements currently open, innermost last, so prose
	// and extensions attach to the step that contains them.
	var open []int
	seen := map[string]bool{}
	skipDepth := -1
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			if err != io.EOF {
				// Malformed from here on: keep what was read. See the doc comment.
				break
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if skipDepth >= 0 {
				continue
			}
			local := t.Name.Local
			// Diagram interchange describes the picture, not the process. Its shapes
			// carry ids of their own and would otherwise list every element twice.
			if local == "BPMNDiagram" {
				skipDepth = depth
				continue
			}
			switch local {
			case "process":
				if p.ID == "" {
					p.ID, p.Name = attr(t, "id"), attr(t, "name")
				}
				continue
			case "documentation":
				text, ok := elementText(d, &t)
				depth--
				if ok {
					p.setDoc(open, text)
				}
				continue
			case "conditionExpression":
				text, ok := elementText(d, &t)
				depth--
				if ok && len(open) > 0 {
					p.Elements[open[len(open)-1]].Condition = clip(text)
				}
				continue
			case "formDefinition":
				if len(open) > 0 {
					p.Elements[open[len(open)-1]].FormID = attr(t, "formId")
				}
				continue
			case "input", "output":
				p.addVariable(seen, attr(t, "target"))
				continue
			case "dataObject", "dataObjectReference", "property":
				p.addVariable(seen, attr(t, "name"))
				continue
			}
			if v := attr(t, "resultVariable"); v != "" {
				p.addVariable(seen, v)
			}
			if !isStep(t) {
				continue
			}
			if len(p.Elements) >= maxOutlineElements {
				p.truncated = true
				continue
			}
			p.Elements = append(p.Elements, Element{
				ID: attr(t, "id"), Kind: local, Name: attr(t, "name"),
			})
			open = append(open, len(p.Elements)-1)
		case xml.EndElement:
			if skipDepth >= 0 && depth == skipDepth {
				skipDepth = -1
			}
			// An element only ever appears on the stack at its own depth, and the
			// outline is built from balanced input often enough that this is exact;
			// when it is not, popping on the matching local name keeps the stack from
			// drifting.
			if skipDepth < 0 && len(open) > 0 && p.Elements[open[len(open)-1]].Kind == t.Name.Local {
				open = open[:len(open)-1]
			}
			depth--
		}
	}
	return p
}

// setDoc attaches prose to the innermost open step, or to the process when none is open.
func (p *Process) setDoc(open []int, text string) {
	text = clip(text)
	if text == "" {
		return
	}
	if len(open) == 0 {
		if p.Documentation == "" {
			p.Documentation = text
		}
		return
	}
	if e := &p.Elements[open[len(open)-1]]; e.Documentation == "" {
		e.Documentation = text
	}
}

// addVariable records a name the model uses for its data, once and in document order.
func (p *Process) addVariable(seen map[string]bool, name string) {
	name = strings.TrimSpace(name)
	if name == "" || seen[name] || len(p.Variables) >= maxOutlineVariables {
		return
	}
	seen[name] = true
	p.Variables = append(p.Variables, name)
}

// isStep reports whether a start element is one of the process's own steps: in the BPMN
// namespace (or in none, which a hand-written model still is), carrying an id, and not
// one of the containers and declarations that have ids without being anything a form is
// ever for.
func isStep(t xml.StartElement) bool {
	if t.Name.Space != nsBPMN && t.Name.Space != "" {
		return false
	}
	if attr(t, "id") == "" {
		return false
	}
	switch t.Name.Local {
	case "definitions", "process", "collaboration", "participant", "laneSet", "lane",
		"extensionElements", "ioSpecification", "dataInputAssociation", "dataOutputAssociation",
		"multiInstanceLoopCharacteristics", "standardLoopCharacteristics", "loopCharacteristics",
		"signal", "message", "error", "escalation", "category", "categoryValue", "resource",
		"itemDefinition", "dataStore", "dataStoreReference", "dataObject", "dataObjectReference":
		return false
	}
	// The event definitions (timerEventDefinition, messageEventDefinition …) say what
	// kind of event their parent is; the parent is the step.
	return !strings.HasSuffix(t.Name.Local, "EventDefinition")
}

// attr reads an attribute by local name, ignoring its prefix — the same
// namespace-tolerant reading processIdentity does, and for the same reason: a model may
// or may not prefix, and both are the same model.
func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// elementText consumes an element and returns its character data. A failure to decode is
// reported so the caller can leave the field empty rather than record an error string.
func elementText(d *xml.Decoder, start *xml.StartElement) (string, bool) {
	var s string
	if err := d.DecodeElement(&s, start); err != nil {
		return "", false
	}
	return s, true
}

// clip normalizes prose for a prompt: collapsed whitespace, bounded length. A cut is
// marked, because prose that simply stops reads as prose that said no more.
func clip(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	r := []rune(s)
	if len(r) <= maxDocRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxDocRunes])) + "…"
}

// Describe renders the outline as the paragraphs that go into a prompt. elementID names
// the step the form is for and may be empty, which is the start-form case: the form
// starts the process, so the process as a whole is what it is for.
//
// It returns the empty string when there is nothing to say, so a caller can append it
// unconditionally and a model never reads a heading over an empty section.
func (p Process) Describe(elementID string) string {
	if p.ID == "" && len(p.Elements) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Process: %s", p.ID)
	if p.Name != "" && p.Name != p.ID {
		fmt.Fprintf(&b, " — %s", p.Name)
	}
	if p.Documentation != "" {
		fmt.Fprintf(&b, "\nWhat it is for: %s", p.Documentation)
	}
	if e, ok := p.Element(elementID); ok {
		fmt.Fprintf(&b, "\n\nThe form is for this step: %s", describeElement(e))
		if e.FormID != "" {
			fmt.Fprintf(&b, "\nIt already binds the form %q, so what you write replaces one that exists.", e.FormID)
		}
	}
	if len(p.Elements) > 0 {
		b.WriteString("\n\nEvery step in the process, in the order the file lists them:")
		for _, e := range p.Elements {
			fmt.Fprintf(&b, "\n- %s", describeElement(e))
		}
		if p.truncated {
			b.WriteString("\n- (further steps not listed)")
		}
	}
	if len(p.Variables) > 0 {
		fmt.Fprintf(&b, "\n\nNames this process already uses for its data: %s.\n"+
			"Where a field means one of these, use that name as its key.", strings.Join(p.Variables, ", "))
	}
	return b.String()
}

// describeElement is one line of the outline: what the step is, what it is called, and
// what the modeller wrote about it.
func describeElement(e Element) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", e.Kind, e.ID)
	if e.Name != "" {
		fmt.Fprintf(&b, " %q", e.Name)
	}
	if e.Documentation != "" {
		fmt.Fprintf(&b, " — %s", e.Documentation)
	}
	if e.Condition != "" {
		fmt.Fprintf(&b, " — taken when: %s", e.Condition)
	}
	return b.String()
}
