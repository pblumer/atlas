package archsketch

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/panorama"
)

// Writing a snapshot as an ArchiMate Open Exchange document (ADR-0471 §3).
//
// The document is generated, never authored, and it says so the way the landscape's
// export does (ADR-0211 §8): its documentation opens with the sentence, every element
// carries atlas.* provenance, and the same snapshot produces the same bytes, so the
// file can be committed and diffed and a change in it is a change in the release.
//
// The mapping, at [MappingVersion]:
//
//   - the Atlas installation is a Node composed of System Software "Atlas";
//   - the release is an Artifact the Node is assigned to, realising the application's
//     Application Component;
//   - Atlas's API is a Technology Interface composed in the Node, serving the
//     component, and each message the application receives is an Application
//     Interface composed in the component;
//   - each worker the application depends on is an Application Service serving the
//     component — "the mail worker serves the invoice application", which is the
//     statement ADR-0211 already makes for a worker;
//   - a call into another application's process is that application's Application
//     Component serving this one.
//
// Every relationship is one Panorama's own authoring rules accept
// (panorama.MayConnect), so a document exported here and imported into Panorama is
// valid there too. Local decisions are not written: they are evaluated inside the
// engine and are not part of the application's boundary, and the documentation counts
// them instead.

type archElement struct {
	id, typ, name, doc string
	props              map[string]string
}

type archRelationship struct {
	typ, source, target string
}

// ArchiMate renders the snapshot as an ArchiMate Model Exchange document.
func ArchiMate(s Snapshot, m Meta) []byte {
	var elements []archElement
	var relationships []archRelationship
	add := func(e archElement) string {
		elements = append(elements, e)
		return e.id
	}
	relate := func(typ, source, target string) {
		relationships = append(relationships, archRelationship{typ, source, target})
	}

	node := add(archElement{id: "id-atlas-node", typ: "Node", name: "Atlas",
		doc:   "The Atlas server the release was recorded on.",
		props: map[string]string{"atlas.derivedKind": "node"}})
	software := add(archElement{id: "id-atlas-software", typ: "SystemSoftware", name: "Atlas",
		doc:   "The Atlas workflow engine.",
		props: map[string]string{"atlas.derivedKind": "systemSoftware"}})
	relate("Composition", node, software)

	app := add(archElement{id: "id-application", typ: "ApplicationComponent", name: m.Application,
		doc: "The process application, as release v" + fmt.Sprint(m.Version) + " shipped it.",
		props: map[string]string{
			"atlas.derivedKind":       "application",
			panorama.KeyApplicationID: m.ApplicationID,
		}})
	release := add(archElement{id: "id-release", typ: "Artifact", name: fmt.Sprintf("%s v%d", m.Application, m.Version),
		doc: "The release: what was published together.",
		props: map[string]string{
			"atlas.derivedKind":   "release",
			panorama.KeyReleaseID: m.ReleaseID,
		}})
	relate("Assignment", node, release)
	relate("Realization", release, app)

	api := add(archElement{id: "id-atlas-api", typ: "TechnologyInterface", name: "Atlas API",
		doc:   "The HTTP API every way into the application goes through: starting instances, publishing messages, completing tasks.",
		props: map[string]string{"atlas.derivedKind": "api"}})
	relate("Composition", node, api)
	relate("Serving", api, app)

	for i, e := range s.EntryPoints {
		effect := "Continues a running instance"
		if e.Starts {
			effect = "Starts an instance"
		}
		id := add(archElement{id: fmt.Sprintf("id-entry-%d", i+1), typ: "ApplicationInterface", name: "Message " + e.Name,
			doc: effect + ". Received at " + usesText(e.UsedBy) + ".",
			props: map[string]string{
				"atlas.derivedKind": "entryPoint",
				"atlas.messageName": e.Name,
			}})
		relate("Composition", app, id)
	}

	for i, d := range s.Outbound() {
		var e archElement
		switch d.Kind {
		case DependencyWorker:
			e = archElement{typ: "ApplicationService", name: d.Name,
				doc: "A worker of type " + workerTypeName(d.WorkerType) + " the application hands work to. Used at " + usesText(d.UsedBy) + ".",
				props: map[string]string{
					"atlas.derivedKind": "worker",
					"atlas.workerName":  d.Name,
					"atlas.workerType":  d.WorkerType,
				}}
		case DependencyRuntimeWorker:
			e = archElement{typ: "ApplicationService", name: "Worker decided at run time",
				doc: "A worker of type " + workerTypeName(d.WorkerType) + " whose name the model computes per instance (" + d.Name + "). Used at " + usesText(d.UsedBy) + ".",
				props: map[string]string{
					"atlas.derivedKind":      "runtimeWorker",
					"atlas.workerExpression": d.Name,
					"atlas.workerType":       d.WorkerType,
				}}
		case DependencyCall:
			name := "Process " + d.Name
			props := map[string]string{"atlas.derivedKind": "call", panorama.KeyProcessID: d.Name}
			doc := "The application holding process " + d.Name + ", which this application calls. Called at " + usesText(d.UsedBy) + "."
			if d.TargetApplication != "" {
				name = d.TargetApplication
				props[panorama.KeyApplicationID] = d.TargetApplicationID
			} else {
				doc += " Nothing on the recording installation held that process when the snapshot was taken."
			}
			e = archElement{typ: "ApplicationComponent", name: name, doc: doc, props: props}
		default:
			continue
		}
		e.id = fmt.Sprintf("id-dependency-%d", i+1)
		relate("Serving", add(e), app)
	}

	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<model xmlns="` + panorama.ExchangeNamespace + `"` +
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xsi:schemaLocation="` + panorama.ExchangeNamespace + " " + panorama.ExchangeNamespace + `archimate3_Model.xsd"` +
		` identifier="id-atlas-release-sketch">` + "\n")
	out.WriteString(`  <name xml:lang="en">` + esc(title(m)) + "</name>\n")
	out.WriteString(`  <documentation xml:lang="en">` + esc(archiProvenance(s, m)) + "</documentation>\n")

	used := map[string]bool{}
	out.WriteString("  <elements>")
	for _, e := range elements {
		fmt.Fprintf(&out, "\n    <element identifier=%q xsi:type=%q>", e.id, e.typ)
		out.WriteString("\n      <name xml:lang=\"en\">" + esc(e.name) + "</name>")
		if e.doc != "" {
			out.WriteString("\n      <documentation xml:lang=\"en\">" + esc(e.doc) + "</documentation>")
		}
		keys := sortedKeys(e.props)
		if len(keys) > 0 {
			out.WriteString("\n      <properties>")
			for _, k := range keys {
				used[k] = true
				fmt.Fprintf(&out, "\n        <property propertyDefinitionRef=%q><value xml:lang=\"en\">%s</value></property>",
					propertyDefinitionID(k), esc(e.props[k]))
			}
			out.WriteString("\n      </properties>")
		}
		out.WriteString("\n    </element>")
	}
	out.WriteString("\n  </elements>\n")

	out.WriteString("  <relationships>")
	for i, r := range relationships {
		fmt.Fprintf(&out, "\n    <relationship identifier=%q source=%q target=%q xsi:type=%q/>",
			fmt.Sprintf("id-rel-%d", i+1), r.source, r.target, r.typ)
	}
	out.WriteString("\n  </relationships>\n")

	out.WriteString("  <propertyDefinitions>")
	for _, k := range sortedKeys(used) {
		fmt.Fprintf(&out, "\n    <propertyDefinition identifier=%q type=\"string\"><name>%s</name></propertyDefinition>",
			propertyDefinitionID(k), esc(k))
	}
	out.WriteString("\n  </propertyDefinitions>\n")
	out.WriteString("</model>\n")
	return []byte(out.String())
}

// archiProvenance is the paragraph that keeps the document from being mistaken for one
// somebody drew, and that lists what it does not carry.
func archiProvenance(s Snapshot, m Meta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generated by Atlas from release v%d of %s (release %s), snapshot taken %s. ",
		m.Version, m.Application, m.ReleaseID, formatTime(s.TakenAt))
	fmt.Fprintf(&b, "DERIVED, NOT AUTHORED: nothing in this model was drawn by a person. It is the "+
		"release's logical architecture in ArchiMate's vocabulary at mapping version %d — the same "+
		"release always produces this document, and editing it here changes nothing in Atlas.\n\n", MappingVersion)
	if n := s.LocalDecisions(); n > 0 {
		fmt.Fprintf(&b, "Local decisions evaluated inside the engine (%d) are not written as elements: they do not cross the application's boundary. ", n)
	}
	if n := s.UnreadProcesses(); n > 0 {
		fmt.Fprintf(&b, "Incomplete: %s could not be read when the snapshot was taken, so what they offer and need is missing. ",
			plural(n, "process", "processes"))
	}
	b.WriteString("No diagram: the importing tool's own layout is the better one.\n\nNot shown:\n")
	for _, gap := range NotShown() {
		b.WriteString("  • " + gap + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func workerTypeName(t string) string {
	if t == "" {
		return "unknown"
	}
	return fmt.Sprintf("%q", t)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if s, ok := any(v).(string); ok && s == "" {
			// An empty property says nothing and would be written as one.
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// propertyDefinitionID is stable for a key, the same spelling the landscape's export
// uses, so a property means one definition across both kinds of generated document.
func propertyDefinitionID(key string) string {
	return "propid-" + strings.ReplaceAll(strings.TrimPrefix(key, "atlas."), ".", "-")
}
