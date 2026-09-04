package panorama

import (
	"fmt"
	"strings"
	"time"
)

// Generating an ArchiMate Open Exchange document from the derived landscape
// (ADR-0211 §8).
//
// The landscape can be *drawn* in ArchiMate's vocabulary; this writes that same
// mapping out as a file another tool can open. It is the step from a picture to
// something an architect can take into Archi, BiZZdesign or MID — and it is the step
// §8 originally ruled out, on the grounds that a generated document could be
// mistaken for one somebody authored.
//
// That risk is real and it is answered here rather than avoided:
//
//   - **The document says what it is, in three places.** Its documentation opens
//     with the sentence, every element carries atlas.* provenance, and the model
//     identifier is derived from the instance rather than random. A reader who opens
//     it in any tool is told on the first screen.
//   - **It carries no observation state.** No severity, no incidents, no reachability
//     — which also settles §10's question about it: this is a *model* export, the
//     safe class, not a live one. A file that froze this morning's health into an
//     architecture document would be the undated green picture in another wrapper,
//     and health is what the SVG/PNG export carries, stamped.
//   - **It is deterministic.** The same landscape produces byte-identical output, so
//     the file can be committed and diffed and a change in it means a change in the
//     estate.
//
// What it deliberately does not produce: a diagram. The landscape's arrangement is
// computed in the browser and belongs to whoever arranged it, not to the model; a
// server-side grid would be a worse layout than the importing tool's own.

// ArchiMateExport is what a generated document says about where it came from.
type ArchiMateExport struct {
	// Instance names the Atlas the landscape was read from — a host, as the reader
	// knows it. Never a credential and never a peer's address: this is the server the
	// reader is already looking at.
	Instance string
	// GeneratedAt is when, in Unix seconds. Zero says so rather than inventing one.
	GeneratedAt int64
}

// ExportArchiMate writes the graph as an ArchiMate Model Exchange document.
//
// The graph is whatever the caller was already allowed to see: this generates from a
// derived graph rather than re-reading anything, so the scope filtering, the
// restricted placeholders and the size budget all apply exactly as they did to the
// picture. There is nothing here that could disclose more than the landscape did.
func ExportArchiMate(g Graph, opts ArchiMateExport) []byte {
	notation, _ := NotationByID(NotationArchiMate32)

	// One pass to decide what is exportable, and to name it. Ids are assigned in the
	// graph's own order, which DeriveGraph already sorts, so the output is stable.
	ids := map[string]string{}
	taken := map[string]bool{}
	var exported []Node
	restricted := 0
	for _, n := range g.Nodes {
		if n.Kind == KindRestricted {
			// It stands for a resource this reader may not see. There is no ArchiMate
			// element for "something is here and it is not yours to know", and the
			// honest thing is to leave it out and say how many — which the model's
			// documentation does.
			restricted++
			continue
		}
		if archiTypeOf(n, notation) == "" {
			continue
		}
		ids[n.ID] = uniqueID(n.ID, taken)
		exported = append(exported, n)
	}

	var elements strings.Builder
	usedKeys := map[string]bool{}
	for _, n := range exported {
		elements.WriteString(elementXML(n, ids[n.ID], notation, usedKeys))
	}

	var relationships strings.Builder
	dropped := 0
	for i, e := range g.Edges {
		rel, known := archiRelations[e.Kind]
		from, haveFrom := ids[e.From]
		to, haveTo := ids[e.To]
		if !known || !haveFrom || !haveTo {
			// An edge with an end that was not exported. Counted rather than dropped
			// quietly: a process whose only dependency is invisible would otherwise
			// appear in the document as one that depends on nothing.
			dropped++
			continue
		}
		source, target := from, to
		if rel.Flip {
			source, target = to, from
		}
		relationships.WriteString(fmt.Sprintf(
			"\n    <relationship identifier=%q source=%q target=%q xsi:type=%q/>",
			fmt.Sprintf("id-rel-%d", i), source, target, rel.Type))
	}

	var definitions strings.Builder
	for _, key := range sortedKeys(usedKeys) {
		definitions.WriteString(fmt.Sprintf(
			"\n    <propertyDefinition identifier=%q type=\"string\"><name>%s</name></propertyDefinition>",
			propertyDefinitionID(key), escapeXML(key)))
	}

	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<model xmlns="` + ExchangeNamespace + `"` +
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xsi:schemaLocation="` + ExchangeNamespace + " " + ExchangeNamespace + `archimate3_Model.xsd"` +
		` identifier="id-atlas-starmap">` + "\n")
	out.WriteString("  <name xml:lang=\"en\">" + escapeXML(modelName(opts)) + "</name>\n")
	out.WriteString("  <documentation xml:lang=\"en\">" +
		escapeXML(provenance(opts, notation, len(exported), restricted, dropped)) + "</documentation>\n")
	if elements.Len() > 0 {
		out.WriteString("  <elements>" + elements.String() + "\n  </elements>\n")
	}
	if relationships.Len() > 0 {
		out.WriteString("  <relationships>" + relationships.String() + "\n  </relationships>\n")
	}
	if definitions.Len() > 0 {
		out.WriteString("  <propertyDefinitions>" + definitions.String() + "\n  </propertyDefinitions>\n")
	}
	out.WriteString("</model>\n")
	return []byte(out.String())
}

// archiTypeOf is the ArchiMate element type a node is written as.
//
// An unresolved placeholder is written as the type of the thing that is *missing* —
// its id names that kind — because "the model declares this and Atlas does not have
// it" is a statement about architecture, and an architecture tool is exactly where
// it belongs. Its own element says so in a property.
func archiTypeOf(n Node, notation Notation) string {
	kind := n.Kind
	if kind == KindUnresolved {
		parts := strings.Split(n.ID, ":")
		if len(parts) < 2 {
			return ""
		}
		kind = parts[1]
	}
	return notation.Types[kind].Type
}

// elementXML writes one element with its name, what is known about it, and the
// atlas.* properties that let the document be read back.
//
// The property keys are ADR-0189 §4's own binding keys, deliberately: a model
// exported from the landscape and imported back into Panorama arrives with its
// bindings already resolving, because the properties are the same contract Panorama
// reads. Nothing here is a credential or an address — a stable opaque Atlas id is
// what a binding is allowed to carry, and it is all these carry.
func elementXML(n Node, id string, notation Notation, usedKeys map[string]bool) string {
	var body strings.Builder
	body.WriteString(fmt.Sprintf("\n    <element identifier=%q xsi:type=%q>", id, archiTypeOf(n, notation)))
	body.WriteString("\n      <name xml:lang=\"en\">" + escapeXML(elementName(n)) + "</name>")
	if doc := elementDocumentation(n); doc != "" {
		body.WriteString("\n      <documentation xml:lang=\"en\">" + escapeXML(doc) + "</documentation>")
	}

	properties := map[string]string{}
	if key, value := bindingFor(n); key != "" && value != "" {
		properties[key] = value
	}
	// Where this element came from, on the element itself. A model tree in somebody
	// else's tool is a long way from this sentence in the documentation, and an
	// element lifted out of it into another model should still say what it is.
	properties["atlas.derivedKind"] = n.Kind
	if n.Kind == KindUnresolved {
		properties["atlas.unresolved"] = "nothing on this server provides it"
	}

	if len(properties) > 0 {
		body.WriteString("\n      <properties>")
		for _, key := range sortedKeys(properties) {
			usedKeys[key] = true
			body.WriteString(fmt.Sprintf(
				"\n        <property propertyDefinitionRef=%q><value xml:lang=\"en\">%s</value></property>",
				propertyDefinitionID(key), escapeXML(properties[key])))
		}
		body.WriteString("\n      </properties>")
	}
	body.WriteString("\n    </element>")
	return body.String()
}

// bindingFor is the ADR-0189 §4 binding key and value that identify this node's
// Atlas resource, or nothing where no key exists for its kind.
//
// A decision has no key: P3 did not define one, and inventing a spelling here would
// put a key on the wire that nothing reads back. The document says what the element
// is through atlas.derivedKind either way.
func bindingFor(n Node) (string, string) {
	switch n.Kind {
	case KindApplication:
		return KeyApplicationID, strings.TrimPrefix(n.ID, KindApplication+":")
	case KindProcess:
		return KeyProcessID, n.ProcessID
	case KindWorker:
		return KeyConnectorID, strings.TrimPrefix(n.ID, KindWorker+":")
	case KindTarget:
		return KeyDeploymentTargetID, strings.TrimPrefix(n.ID, KindTarget+":")
	}
	return "", ""
}

func elementName(n Node) string {
	if strings.TrimSpace(n.Name) != "" {
		return n.Name
	}
	return n.ID
}

// elementDocumentation is what Atlas knows about the element that its type does not
// already say. Never its state: see the note at the top of this file.
func elementDocumentation(n Node) string {
	switch n.Kind {
	case KindProcess:
		if n.ProcessID != "" {
			return fmt.Sprintf("Deployed BPMN process %q, version %d.", n.ProcessID, n.Version)
		}
	case KindWorker:
		if n.WorkerType != "" {
			return fmt.Sprintf("A configured worker of type %q. Its endpoint and credentials stay on the server.", n.WorkerType)
		}
	case KindTarget:
		return "A deployment target: a peer Atlas this server can promote to. Its address stays on the server."
	case KindUnresolved:
		return "Referenced by a deployed process, and nothing on this server provides it. Work reaching it would park."
	}
	return ""
}

func modelName(opts ArchiMateExport) string {
	if strings.TrimSpace(opts.Instance) != "" {
		return "Atlas starmap — " + opts.Instance
	}
	return "Atlas starmap"
}

// provenance is the paragraph that keeps this document from being mistaken for one
// somebody drew, and the one that reports what the mapping dropped.
func provenance(opts ArchiMateExport, notation Notation, elements, restricted, dropped int) string {
	var b strings.Builder
	b.WriteString("Generated by Atlas from its own resources")
	if strings.TrimSpace(opts.Instance) != "" {
		b.WriteString(" on " + opts.Instance)
	}
	if opts.GeneratedAt > 0 {
		b.WriteString(", " + time.Unix(opts.GeneratedAt, 0).UTC().Format("2006-01-02 15:04 UTC"))
	}
	b.WriteString(". DERIVED, NOT AUTHORED: nothing in this model was drawn by a person. " +
		"It is a projection of what this server runs, in ArchiMate's vocabulary, at " +
		fmt.Sprintf("mapping version %d", notation.MappingVersion) + " — re-generating it " +
		"reproduces it, and editing it here does not change anything in Atlas.\n\n")

	b.WriteString(fmt.Sprintf("%d element(s). ", elements))
	if restricted > 0 {
		b.WriteString(fmt.Sprintf("%d dependency of this starmap points at a resource the "+
			"exporting reader may not see; there is no ArchiMate element for that, so it and "+
			"its relationships are absent. This model is a part of the starmap, not all of "+
			"it. ", restricted))
	}
	if dropped > 0 {
		b.WriteString(fmt.Sprintf("%d relationship(s) had an end that is not in this model and "+
			"are absent with it. ", dropped))
	}
	b.WriteString("No diagram: the starmap's arrangement is computed in the browser and " +
		"belongs to whoever arranged it, so your tool's own layout is the better one. " +
		"No observation state either — health, incidents and reachability are what the " +
		"starmap's image export carries, dated; an architecture document that froze them " +
		"would go on asserting them.\n\n")

	b.WriteString("What this vocabulary cannot carry:\n")
	for _, loss := range notation.Loss {
		b.WriteString("  • " + loss + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// uniqueID turns a mesh node id into an xsd:ID. Colons are the reason this exists —
// a mesh id is namespaced with them and an NCName may not contain one — and the
// counter is the reason it is safe: two different ids must never collapse into one
// element, which in an exchange document would silently merge two resources.
func uniqueID(nodeID string, taken map[string]bool) string {
	var b strings.Builder
	b.WriteString("id-")
	for _, r := range nodeID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	candidate := b.String()
	unique := candidate
	for i := 2; taken[unique]; i++ {
		unique = fmt.Sprintf("%s-%d", candidate, i)
	}
	taken[unique] = true
	return unique
}

// propertyDefinitionID is stable for a key, because the property that references it
// is written before the definitions block is.
func propertyDefinitionID(key string) string {
	return "propid-" + strings.ReplaceAll(strings.TrimPrefix(key, "atlas."), ".", "-")
}
