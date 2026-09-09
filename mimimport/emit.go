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

	doc := fmt.Sprintf(
		"Aus MIM/FIM-XOML konvertiert (Wurzel-Aktivität %s). %d nativ, %d erhalten, %d manuell zu prüfen. Nicht übersetzte Konstrukte sind in atlas:mimSource erhalten.",
		root.local(), b.report.Count(StatusNative), b.report.Count(StatusPreserved), b.report.Count(StatusManualReview))
	// What MIM knows about the workflow and the XOML does not say — the phase it
	// runs in above all — belongs on the process, not only in the import response.
	if s := b.report.Source.describe(); s != "" {
		doc += " Aus der MIM-WorkflowDefinition: " + s
	}
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
// leading fragment (e.g. a zeebe:taskDefinition) with the preserved XOML source.
//
// The source is written as ordinary escaped character data, never inside a
// CDATA section. CDATA suppresses entity resolution, so an activity whose
// attribute holds a quoted MIM expression — ActivityExecutionCondition,
// Iteration, ConflictFilter all routinely do — would be preserved with the
// literal text &#34; where the workflow had a quotation mark, silently
// changing the expression. Escaping once here means a consumer that unescapes
// the element text gets the activity's markup back exactly as MIM wrote it.
func emitExtensions(s *strings.Builder, n bnode, lead string) {
	if lead == "" && n.raw == "" {
		return
	}
	s.WriteString("      <extensionElements>\n")
	if lead != "" {
		s.WriteString(lead)
	}
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
