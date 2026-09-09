package mimimport

import (
	"encoding/xml"
	"fmt"
	"strings"
)

const (
	nsAtlas = "http://atlas/schema/1.0"
	nsMIM   = "http://atlas/mim"
)

// emitBPMN renders the accumulated nodes and flows as a BPMN 2.0 <definitions>
// document, including a diagram-interchange (DI) plane laid out left-to-right by
// layout(). The DI matters: the Modeler's canvas renders from it, so an import
// without DI opens blank — the model is complete but invisible until laid out.
func (b *builder) emitBPMN(root xnode) []byte {
	procID := b.report.ProcessID
	var s strings.Builder
	s.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&s, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"`+"\n")
	fmt.Fprintf(&s, `             xmlns:atlas=%q`+"\n", nsAtlas)
	fmt.Fprintf(&s, `             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"`+"\n")
	fmt.Fprintf(&s, `             xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"`+"\n")
	fmt.Fprintf(&s, `             xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"`+"\n")
	fmt.Fprintf(&s, `             xmlns:di="http://www.omg.org/spec/DD/20100524/DI"`+"\n")
	fmt.Fprintf(&s, `             id=%q targetNamespace=%q>`+"\n", b.claim("defs_"+procID), nsMIM)
	fmt.Fprintf(&s, `  <process id=%q name=%q isExecutable="true">`+"\n", procID, attr(b.name))

	// The three numbers count worksheet items, not BPMN elements: a MIMWAL
	// activity with five assignments is one node and six pieces of work, and the
	// number on the model has to be the one a migration is planned with. The
	// sentence says so, because "3 erhalten" would otherwise read as three steps.
	doc := fmt.Sprintf(
		"Aus MIM/FIM-XOML konvertiert (Wurzel-Aktivität %s). Arbeitsblatt: %d nativ, %d erhalten, %d manuell zu prüfen — gezählt werden Positionen, also Knoten und die dekodierten Zeilen ihrer MIMWAL-Tabellen. Nicht übersetzte Konstrukte sind in atlas:mimSource erhalten; die dekodierten Zeilen stehen zusätzlich als atlas:mimCollection am jeweiligen Element.",
		root.local(), b.report.Count(StatusNative), b.report.Count(StatusPreserved), b.report.Count(StatusManualReview))
	if len(b.report.Warnings) > 0 {
		doc += " Hinweis: Die Eingabe war nicht wohlgeformt und wurde vor dem Parsen repariert; Einzelheiten im Konvertierungsbericht."
	}
	fmt.Fprintf(&s, "    <documentation>%s</documentation>\n", text(doc))

	for _, n := range b.nodes {
		b.emitNode(&s, n)
	}
	for _, f := range b.flows {
		emitFlow(&s, f)
	}
	s.WriteString("  </process>\n")

	b.emitDI(&s, b.layout())

	s.WriteString("</definitions>\n")
	return []byte(s.String())
}

func (b *builder) emitNode(s *strings.Builder, n bnode) {
	switch n.kind {
	case "startEvent", "endEvent":
		fmt.Fprintf(s, "    <%s id=%q name=%q/>\n", n.kind, n.id, attr(n.name))
	case "exclusiveGateway", "parallelGateway":
		def := ""
		if n.def != "" {
			def = fmt.Sprintf(" default=%q", n.def)
		}
		if n.raw == "" && n.doc == "" {
			fmt.Fprintf(s, "    <%s id=%q name=%q%s/>\n", n.kind, n.id, attr(n.name), def)
			return
		}
		fmt.Fprintf(s, "    <%s id=%q name=%q%s>\n", n.kind, n.id, attr(n.name), def)
		if n.doc != "" { // a guard gateway documents the MIM condition it stands for
			fmt.Fprintf(s, "      <documentation>%s</documentation>\n", text(n.doc))
		}
		emitExtensions(s, n, "")
		fmt.Fprintf(s, "    </%s>\n", n.kind)
	default: // userTask, serviceTask, task
		fmt.Fprintf(s, "    <%s id=%q name=%q>\n", n.kind, n.id, attr(n.name))
		if n.doc != "" {
			fmt.Fprintf(s, "      <documentation>%s</documentation>\n", text(n.doc))
		}
		taskDef := ""
		if n.kind == "serviceTask" && n.jobType != "" {
			taskDef = fmt.Sprintf("        <zeebe:taskDefinition type=%q/>\n", attr(n.jobType))
		}
		emitExtensions(s, n, taskDef)
		emitMultiInstance(s, n)
		fmt.Fprintf(s, "    </%s>\n", n.kind)
	}
}

// emitExtensions writes an <extensionElements> block combining an optional
// leading fragment (e.g. a zeebe:taskDefinition), the decoded MIMWAL collections
// and the preserved XOML source.
//
// The source is written as ordinary escaped character data, never inside a
// CDATA section. CDATA suppresses entity resolution, so an activity whose
// attribute holds a quoted MIM expression — ActivityExecutionCondition,
// Iteration, ConflictFilter all routinely do — would be preserved with the
// literal text &#34; where the workflow had a quotation mark, silently
// changing the expression. Escaping once here means a consumer that unescapes
// the element text gets the activity's markup back exactly as MIM wrote it.
func emitExtensions(s *strings.Builder, n bnode, lead string) {
	if lead == "" && n.raw == "" && len(n.tables) == 0 {
		return
	}
	s.WriteString("      <extensionElements>\n")
	if lead != "" {
		s.WriteString(lead)
	}
	emitCollections(s, n.tables)
	if n.raw != "" {
		// type and assembly name what the local activity name alone cannot: which
		// library a MIMWAL and a stock MIM activity of the same name came from,
		// and the version it was authored against.
		qualified := ""
		if n.rawType != "" {
			qualified = fmt.Sprintf(" type=%q", attr(n.rawType))
		}
		if n.rawAsm != "" {
			qualified += fmt.Sprintf(" assembly=%q", attr(n.rawAsm))
		}
		fmt.Fprintf(s, "        <atlas:mimSource activity=%q%s>%s</atlas:mimSource>\n",
			attr(n.rawName), qualified, text(n.raw))
	}
	s.WriteString("      </extensionElements>\n")
}

// emitCollections writes the decoded MIMWAL collections of an activity as
// extension elements, so the rows a reviewer has to work through are addressable
// by a tool without re-parsing the XOML in atlas:mimSource.
//
// The elements state structure and nothing else. A cell says which column it sat
// in, never what that column means — that is the restraint tables.go documents,
// and it is the difference between a worksheet and an invented mapping. Cell text
// is written verbatim (outer whitespace trimmed), because a MIM expression can
// hold a string literal whose spacing is part of its value; the collapsing that
// keeps the documentation table on one line is a rendering, not the value.
//
// An ArrayList is emitted with kind="list" and one single-cell row per entry, so
// a consumer walks both shapes the same way.
func emitCollections(s *strings.Builder, cols []mimCollection) {
	for _, c := range cols {
		fmt.Fprintf(s, "        <atlas:mimCollection property=%q kind=%q count=\"%d\">\n",
			attr(c.property), attr(c.kind), len(c.rows))
		for _, r := range c.rows {
			fmt.Fprintf(s, "          <atlas:mimRow index=\"%d\">\n", r.index)
			for _, cell := range r.cells {
				fmt.Fprintf(s, "            <atlas:mimCell column=\"%d\">%s</atlas:mimCell>\n",
					cell.column, text(cell.text))
			}
			s.WriteString("          </atlas:mimRow>\n")
		}
		for _, n := range c.notes {
			fmt.Fprintf(s, "          <atlas:mimNote>%s</atlas:mimNote>\n", text(n))
		}
		s.WriteString("        </atlas:mimCollection>\n")
	}
}

// miPlaceholder is the input collection of an activity whose MIM Iteration was
// not translated: a one-element list, so the activity runs exactly once — what it
// did before the iteration was modelled. The MIM expression itself
// (SplitString of a delimited attribute, typically) reads MIM data through
// references FEEL has no counterpart for, so translating it would risk a model
// that looks right and is not; the original is on the activity's documentation
// and flagged in the Report. mimValue names the current value, standing in for
// MIM's [//Value].
const (
	miPlaceholder = "=[1]"
	miElement     = "mimValue"
)

// emitMultiInstance writes the loop marker of an activity MIM iterates. It is
// sequential because MIMWAL walks the values in order, and it comes after
// <extensionElements> because that is where BPMN puts loopCharacteristics.
func emitMultiInstance(s *strings.Builder, n bnode) {
	if n.iterate == "" {
		return
	}
	s.WriteString(`      <multiInstanceLoopCharacteristics isSequential="true">` + "\n")
	s.WriteString("        <extensionElements>\n")
	fmt.Fprintf(s, "          <zeebe:loopCharacteristics inputCollection=%q inputElement=%q/>\n",
		attr(miPlaceholder), attr(miElement))
	s.WriteString("        </extensionElements>\n")
	s.WriteString("      </multiInstanceLoopCharacteristics>\n")
}

func emitFlow(s *strings.Builder, f bflow) {
	name := ""
	if f.name != "" {
		name = fmt.Sprintf(" name=%q", attr(f.name))
	}
	if f.cond == "" {
		fmt.Fprintf(s, "    <sequenceFlow id=%q sourceRef=%q targetRef=%q%s/>\n", f.id, f.from, f.to, name)
		return
	}
	fmt.Fprintf(s, "    <sequenceFlow id=%q sourceRef=%q targetRef=%q%s>\n", f.id, f.from, f.to, name)
	fmt.Fprintf(s, "      <conditionExpression>%s</conditionExpression>\n", text(f.cond))
	s.WriteString("    </sequenceFlow>\n")
}

// attr escapes a string for use in a double-quoted XML attribute.
func attr(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// text escapes a string for use as XML character data. Only &, < and > have to
// be escaped there, so — unlike attr, which also turns newlines, tabs and quotes
// into numeric references because an attribute value normalises them — preserved
// markup and a multi-line documentation stay readable in the generated file
// while still round-tripping exactly.
func text(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
