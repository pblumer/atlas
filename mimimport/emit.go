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
	fmt.Fprintf(&s, `             id="defs_%s" targetNamespace=%q>`+"\n", procID, nsMIM)
	fmt.Fprintf(&s, `  <process id=%q name=%q isExecutable="true">`+"\n", procID, attr(b.name))

	fmt.Fprintf(&s, "    <documentation>%s</documentation>\n", text(fmt.Sprintf(
		"Aus MIM/FIM-XOML konvertiert (Wurzel-Aktivität %s). %d nativ, %d erhalten, %d manuell zu prüfen. Nicht übersetzte Konstrukte sind in atlas:mimSource erhalten.",
		root.local(), b.report.Count(StatusNative), b.report.Count(StatusPreserved), b.report.Count(StatusManualReview))))

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
		if n.raw == "" {
			fmt.Fprintf(s, "    <%s id=%q name=%q%s/>\n", n.kind, n.id, attr(n.name), def)
			return
		}
		fmt.Fprintf(s, "    <%s id=%q name=%q%s>\n", n.kind, n.id, attr(n.name), def)
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
		fmt.Fprintf(s, "    </%s>\n", n.kind)
	}
}

// emitExtensions writes an <extensionElements> block combining an optional
// leading fragment (e.g. a zeebe:taskDefinition) with the preserved XOML source.
func emitExtensions(s *strings.Builder, n bnode, lead string) {
	if lead == "" && n.raw == "" {
		return
	}
	s.WriteString("      <extensionElements>\n")
	if lead != "" {
		s.WriteString(lead)
	}
	if n.raw != "" {
		fmt.Fprintf(s, "        <atlas:mimSource activity=%q>%s</atlas:mimSource>\n", attr(n.rawName), cdata(n.raw))
	}
	s.WriteString("      </extensionElements>\n")
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

// text escapes a string for use as XML character data.
func text(s string) string { return attr(s) }

// cdata wraps preserved markup in a CDATA section, falling back to escaped text
// only if the payload itself contains the CDATA terminator.
func cdata(s string) string {
	if strings.Contains(s, "]]>") {
		return text(s)
	}
	return "<![CDATA[" + s + "]]>"
}
